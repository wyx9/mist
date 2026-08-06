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
	actorFlushInterval = 2 * time.Second // actor -> Redis 落盘周期
	mysqlFlushInterval = 5 * time.Second // Redis -> MySQL 增量同步周期
	mysqlBatchSize     = 100             // 每次同步的批大小
)

// CategorySpec 由 Register 从 actor 原型登记的类别落地配置。
type CategorySpec struct {
	Category       string
	RedisKeyPrefix string
	Table          string
	DBName         string // 空串 = 使用配置默认库
}

// Options Manager 构造参数。
type Options struct {
	AM        *core.AManager
	RDB       *redis.Client
	OpenStore func(dbName string) (storage.MysqlStore, error) // nil 则跳过 MySQL 同步
}

// Manager 持久化管理器：按注册类别定时向可持久化 actor 投递 PersistTick，
// 并驱动 Redis -> MySQL 的 DB 写线程。
type Manager struct {
	am        *core.AManager
	rdb       *redis.Client
	openStore func(dbName string) (storage.MysqlStore, error)
	specs     map[string]CategorySpec
	codec     storage.Codec
	redis     *storage.RedisStore
	writer    *storage.RedisWriter
	mysql     *storage.MysqlSync
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	started   bool
	mu        sync.Mutex
}

// NewManager 构造持久化管理器；落地配置由后续 Register 提供。
func NewManager(opts Options) (*Manager, error) {
	if opts.AM == nil {
		return nil, errors.New("persist: nil AManager")
	}
	if opts.RDB == nil {
		return nil, errors.New("persist: nil redis client")
	}
	redisStore := storage.NewRedisStore(opts.RDB)
	return &Manager{
		am:        opts.AM,
		rdb:       opts.RDB,
		openStore: opts.OpenStore,
		specs:     make(map[string]CategorySpec),
		codec:     storage.JSONCodec{},
		redis:     redisStore,
		writer:    storage.NewRedisWriter(redisStore, 0),
	}, nil
}

// Register 登记某类别 actor 的落地配置（表名/前缀/库名来自原型实现），须在 Start 之前调用。
func (m *Manager) Register(prototype core.IActor) error {
	p, ok := prototype.(core.Persistable)
	if !ok {
		return fmt.Errorf("persist: %T does not implement Persistable", prototype)
	}
	category := prototype.Category()
	if category == "" {
		return fmt.Errorf("persist: empty category for %T", prototype)
	}
	spec := CategorySpec{
		Category:       category,
		RedisKeyPrefix: p.PersistKeyPrefix(),
		Table:          p.PersistTable(),
		DBName:         p.PersistDBName(),
	}
	if spec.RedisKeyPrefix == "" || spec.Table == "" {
		return fmt.Errorf("persist: invalid spec for category %q (empty key prefix or table)", category)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.specs[category]; ok {
		return fmt.Errorf("persist: category %q already registered", category)
	}
	m.specs[category] = spec
	return nil
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
	ticker := time.NewTicker(actorFlushInterval)
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

func (m *Manager) tickAll() {
	m.mu.Lock()
	categories := make([]string, 0, len(m.specs))
	for category := range m.specs {
		categories = append(categories, category)
	}
	m.mu.Unlock()
	for _, category := range categories {
		m.am.RangeByCategory(category, func(name string, ab *core.ActorBase) bool {
			if _, ok := ab.Actor().(core.Persistable); !ok {
				return true
			}
			if err := m.am.Send(name, "persist", core.PersistTick{}); err != nil {
				logger.ERROR("persist tick send failed:", err, "actor:", name)
			}
			return true
		})
	}
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
