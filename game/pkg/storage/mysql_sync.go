package storage

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"

	"simple_game/game/pkg/logger"
)

// CategoryTable 某类 actor 对应的 MySQL 表与 Redis 前缀配置。
type CategoryTable struct {
	Category       string
	Table          string
	DBName         string
	RedisKeyPrefix string
}

// MysqlSyncConfig MysqlSync 构造参数。
type MysqlSyncConfig struct {
	Redis      *redis.Client
	Interval   time.Duration
	BatchSize  int
	Categories map[string]CategoryTable
	OpenStore  func(dbName string) (MysqlStore, error) // 测试可注入 fake
}

// MysqlSync DB 写线程：周期扫描各类别脏集合，把 Redis 数据增量同步到 MySQL。
type MysqlSync struct {
	rdb        *redis.Client
	interval   time.Duration
	batchSize  int
	categories map[string]CategoryTable
	openStore  func(dbName string) (MysqlStore, error)
	stores     map[string]MysqlStore
	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	started    bool
}

func NewMysqlSync(cfg MysqlSyncConfig) *MysqlSync {
	batch := cfg.BatchSize
	if batch <= 0 {
		batch = 100
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &MysqlSync{
		rdb:        cfg.Redis,
		interval:   interval,
		batchSize:  batch,
		categories: cfg.Categories,
		openStore:  cfg.OpenStore,
		stores:     make(map[string]MysqlStore),
	}
}

// WriterFor 返回指定库（已规范化的 DBName）的写入句柄，懒加载并缓存。
func (s *MysqlSync) WriterFor(dbName string) (MysqlStore, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.stores[dbName]; ok {
		return st, nil
	}
	st, err := s.openStore(dbName)
	if err != nil {
		return nil, err
	}
	s.stores[dbName] = st
	return st, nil
}

// Start 启动 DB 同步循环，并按类别确保表结构存在。
func (s *MysqlSync) Start() error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.started = true
	s.mu.Unlock()

	for _, ct := range s.categories {
		st, err := s.WriterFor(ct.DBName)
		if err != nil {
			s.Stop(2 * time.Second)
			return err
		}
		if err := st.EnsureTable(context.Background(), ct.Table); err != nil {
			// 建表失败不阻断启动：Redis 仍是热源，DB 写线程每轮重试并记录错误
			logger.ERROR("ensure table failed:", err, "table:", ct.Table)
		}
	}

	s.wg.Add(1)
	go s.loop()
	return nil
}

func (s *MysqlSync) loop() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.drainAll(s.ctx)
		}
	}
}

// Stop 停止同步循环，做最后一次 drain 后关闭所有连接。
func (s *MysqlSync) Stop(timeout time.Duration) {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	s.cancel()
	s.started = false
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		logger.ERROR("mysql sync stop timeout")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	s.drainAll(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	for dbName, st := range s.stores {
		if err := st.Close(); err != nil {
			logger.ERROR("close mysql store failed:", err, "db:", dbName)
		}
	}
	s.stores = make(map[string]MysqlStore)
}

// drainAll 处理所有类别的脏集合；单个类别失败记录日志并继续，错误由下轮重试。
func (s *MysqlSync) drainAll(ctx context.Context) {
	for _, ct := range s.categories {
		if err := s.drainCategory(ctx, ct); err != nil {
			logger.ERROR("mysql sync category failed:", err, "category:", ct.Category)
		}
	}
}

func (s *MysqlSync) drainCategory(ctx context.Context, ct CategoryTable) error {
	uids, err := s.rdb.SMembers(ctx, DirtySetKey(ct.Category)).Result()
	if err != nil {
		return err
	}
	if len(uids) == 0 {
		return nil
	}
	st, err := s.WriterFor(ct.DBName)
	if err != nil {
		return err
	}
	for i := 0; i < len(uids); i += s.batchSize {
		end := i + s.batchSize
		if end > len(uids) {
			end = len(uids)
		}
		if err := s.drainChunk(ctx, st, ct, uids[i:end]); err != nil {
			return err
		}
	}
	return nil
}

func (s *MysqlSync) drainChunk(ctx context.Context, st MysqlStore, ct CategoryTable, uids []string) error {
	pipe := s.rdb.Pipeline()
	cmds := make([]*redis.StringCmd, len(uids))
	for i, uid := range uids {
		cmds[i] = pipe.Get(ctx, ct.RedisKeyPrefix+uid)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	dirtyKey := DirtySetKey(ct.Category)
	for i, uid := range uids {
		actorID, err := strconv.ParseInt(uid, 10, 64)
		if err != nil {
			logger.ERROR("invalid uid in dirty set:", uid, "category:", ct.Category)
			_ = s.rdb.SRem(ctx, dirtyKey, uid).Err()
			continue
		}
		raw, err := cmds[i].Bytes()
		switch {
		case errors.Is(err, redis.Nil):
			// Redis 中已无数据（删除）：DB 侧删除行
			if err := st.Delete(ctx, ct.Table, actorID); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			if err := st.Upsert(ctx, ct.Table, actorID, raw); err != nil {
				return err
			}
		}
		if err := s.rdb.SRem(ctx, dirtyKey, uid).Err(); err != nil {
			logger.ERROR("dirty set srem failed:", err, "uid:", uid)
		}
	}
	return nil
}
