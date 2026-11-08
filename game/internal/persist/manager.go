package persist

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"

	core "simple_game/game/internal/actor/core"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/storage"
)

const (
	defaultActorFlushInterval = 2 * time.Second // actor -> Redis 默认落盘周期
	mysqlFlushInterval        = 5 * time.Second // Redis -> MySQL 增量同步周期
	mysqlBatchSize            = 100             // 每次同步的批大小
)

// CategorySpec 由 RegisterCategory 按类别约定派生的落地配置。
type CategorySpec struct {
	Category       string
	RedisKeyPrefix string
	Table          string
	DBName         string // 空串 = 使用配置默认库
}

// Options Manager 构造参数。
type Options struct {
	AM            *core.AManager
	RDB           *redis.Client
	FlushInterval time.Duration                                   // actor -> Redis 落盘周期，<=0 用默认 2s
	OpenStore     func(dbName string) (storage.MysqlStore, error) // nil 则跳过 MySQL 同步
}

// Manager 持久化管理器：定时向已绑定后端的 actor 投递落盘消息（PersistSave），
// 并驱动 Redis -> MySQL 的 DB 写线程。
type Manager struct {
	am            *core.AManager
	rdb           *redis.Client
	openStore     func(dbName string) (storage.MysqlStore, error)
	specs         map[string]CategorySpec
	codec         storage.Codec
	redis         *storage.RedisStore
	writer        *storage.RedisWriter
	mysql         *storage.MysqlSync
	flushInterval time.Duration
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	started       bool
	mu            sync.Mutex
}

// NewManager 构造持久化管理器；落地类别由后续 RegisterCategory 提供。
func NewManager(opts Options) (*Manager, error) {
	if opts.AM == nil {
		return nil, errors.New("persist: nil AManager")
	}
	if opts.RDB == nil {
		return nil, errors.New("persist: nil redis client")
	}
	flush := opts.FlushInterval
	if flush <= 0 {
		flush = defaultActorFlushInterval
	}
	redisStore := storage.NewRedisStore(opts.RDB)
	return &Manager{
		am:            opts.AM,
		rdb:           opts.RDB,
		openStore:     opts.OpenStore,
		specs:         make(map[string]CategorySpec),
		codec:         storage.JSONCodec{},
		redis:         redisStore,
		writer:        storage.NewRedisWriter(redisStore, 0),
		flushInterval: flush,
	}, nil
}

// RegisterCategory 登记需要落地的 actor 类别；表名/Redis 前缀按类别约定派生，须在 Start 之前调用。
func (m *Manager) RegisterCategory(category string) error {
	if category == "" {
		return errors.New("persist: empty category")
	}
	spec := defaultSpecFor(category)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.specs[category]; ok {
		return fmt.Errorf("persist: category %q already registered", category)
	}
	m.specs[category] = spec
	return nil
}

// defaultSpecFor 按类别约定派生落地配置。
func defaultSpecFor(category string) CategorySpec {
	return CategorySpec{
		Category:       category,
		RedisKeyPrefix: "actor_data:" + category + ":",
		Table:          category + "_data",
	}
}

// Resolve 实现 core.PersistResolver：返回该类别 actor 的落地后端。
func (m *Manager) Resolve(category string) core.PersistStore {
	return m.HandleFor(category)
}

// Start 启动写协程、DB 写线程与全局落盘 ticker。
func (m *Manager) Start() error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.started = true
	m.mu.Unlock()

	m.writer.Start()
	if m.openStore != nil {
		m.mysql = newMysqlSync(m.rdb, m.openStore, m.specs)
		if err := m.mysql.Start(); err != nil {
			m.Stop(2 * time.Second)
			return err
		}
	}
	m.wg.Add(1)
	go m.tickLoop()
	return nil
}

// Stop 停止 ticker，先 drain Redis 写队列，再做 MySQL 最终同步后关闭连接。
func (m *Manager) Stop(timeout time.Duration) {
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return
	}
	m.cancel()
	m.started = false
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		logger.ERROR("persist manager stop timeout")
	}

	if m.writer != nil {
		m.writer.Stop(timeout)
	}
	if m.mysql != nil {
		m.mysql.Stop(timeout)
		m.mysql = nil
	}
}

// HandleFor 返回某类别 actor 的落地句柄（用于创建 actor 时注入）。
func (m *Manager) HandleFor(category string) core.PersistStore {
	m.mu.Lock()
	spec, ok := m.specs[category]
	m.mu.Unlock()
	if !ok {
		logger.ERROR("persist: store for unknown category:", category)
		return nil
	}
	var mysql storage.MysqlStore
	if m.mysql != nil {
		var err error
		mysql, err = m.mysql.WriterFor(spec.DBName)
		if err != nil {
			logger.ERROR("persist: mysql writer unavailable:", err, "category:", category)
			return nil
		}
	}
	return &store{
		spec:   spec,
		codec:  m.codec,
		redis:  m.redis,
		mysql:  mysql,
		writer: m.writer,
	}
}

func (m *Manager) tickLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(m.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.tickAll()
		}
	}
}

// tickAll 周期全量落盘：每个已绑定后端的 actor 投递一次 PersistSave。
func (m *Manager) tickAll() {
	m.am.Range(func(ab *core.ActorBase) bool {
		if !ab.Persistent() {
			return true
		}
		err := m.am.SendPersist(ab.ID())
		if err != nil && !errors.Is(err, core.ErrActorNotFound) && !errors.Is(err, core.ErrActorClose) {
			logger.ERROR("persist tick send failed:", err, "actor:", ab.ID())
		}
		return true
	})
}

func newMysqlSync(rdb *redis.Client, openStore func(dbName string) (storage.MysqlStore, error), specs map[string]CategorySpec) *storage.MysqlSync {
	tables := make(map[string]storage.CategoryTable, len(specs))
	for category, spec := range specs {
		tables[category] = storage.CategoryTable{
			Category:       category,
			Table:          spec.Table,
			DBName:         spec.DBName,
			RedisKeyPrefix: spec.RedisKeyPrefix,
		}
	}
	return storage.NewMysqlSync(storage.MysqlSyncConfig{
		Redis:      rdb,
		Interval:   mysqlFlushInterval,
		BatchSize:  mysqlBatchSize,
		Categories: tables,
		OpenStore:  openStore,
	})
}
