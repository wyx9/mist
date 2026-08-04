package persist

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"

	"simple_game/game/internal/actor"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/storage"
)

// CategorySpec 某类 actor 的持久化配置。
type CategorySpec struct {
	Category       string
	RedisKeyPrefix string
	Table          string
	DBName         string // 空则使用默认库
	Codec          string // "json"（后续支持 "proto"）
	FlushInterval  time.Duration
}

// Options PersistManager 构造参数。
type Options struct {
	AM             *actor.AManager
	RDB            *redis.Client
	Specs          map[string]CategorySpec
	DefaultDBName  string
	RedisQueueSize int                      // 异步写队列容量，<=0 使用默认 4096
	MySQLCfg       *storage.MysqlSyncConfig // nil 则跳过 DB 同步（测试或纯 Redis 模式）
}

// Manager 持久化管理器：按类别定时向可持久化 actor 投递 PersistTick，
// 并驱动 Redis -> MySQL 的 DB 写线程。
type Manager struct {
	am      *actor.AManager
	rdb     *redis.Client
	specs   map[string]CategorySpec
	codecs  map[string]storage.Codec
	redis   *storage.RedisStore
	writer  *storage.RedisWriter
	mysql   *storage.MysqlSync
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
	mu      sync.Mutex
}

// NewManager 校验并规范化 spec，构造持久化管理器。
func NewManager(opts Options) (*Manager, error) {
	if opts.AM == nil {
		return nil, errors.New("persist: nil AManager")
	}
	if opts.RDB == nil {
		return nil, errors.New("persist: nil redis client")
	}

	specs := make(map[string]CategorySpec, len(opts.Specs))
	for category, spec := range opts.Specs {
		spec.Category = category
		if spec.DBName == "" {
			spec.DBName = opts.DefaultDBName
		}
		if spec.Category == "" || spec.RedisKeyPrefix == "" || spec.Table == "" || spec.FlushInterval <= 0 {
			return nil, fmt.Errorf("persist: invalid spec for category %q", category)
		}
		if spec.Codec == "" {
			spec.Codec = "json"
		}
		specs[category] = spec
	}

	codecs := map[string]storage.Codec{
		"json": storage.JSONCodec{},
	}
	for category, spec := range specs {
		if _, ok := codecs[spec.Codec]; !ok {
			return nil, fmt.Errorf("persist: unknown codec %q for category %q", spec.Codec, category)
		}
	}

	var mysql *storage.MysqlSync
	if opts.MySQLCfg != nil {
		if opts.MySQLCfg.OpenStore == nil {
			return nil, errors.New("persist: nil OpenStore in MySQLCfg")
		}
		cfg := *opts.MySQLCfg
		cfg.Categories = make(map[string]storage.CategoryTable, len(specs))
		for category, spec := range specs {
			cfg.Categories[category] = storage.CategoryTable{
				Category:       category,
				Table:          spec.Table,
				DBName:         spec.DBName,
				RedisKeyPrefix: spec.RedisKeyPrefix,
			}
		}
		mysql = storage.NewMysqlSync(cfg)
	}

	redisStore := storage.NewRedisStore(opts.RDB)
	return &Manager{
		am:     opts.AM,
		rdb:    opts.RDB,
		specs:  specs,
		codecs: codecs,
		redis:  redisStore,
		writer: storage.NewRedisWriter(redisStore, opts.RedisQueueSize),
		mysql:  mysql,
	}, nil
}

// Start 启动各类别 ticker 与 DB 写线程。
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
	if m.mysql != nil {
		if err := m.mysql.Start(); err != nil {
			m.Stop(2 * time.Second)
			return err
		}
	}
	for _, spec := range m.specs {
		m.wg.Add(1)
		go m.tickLoop(spec)
	}
	return nil
}

// Stop 停止 ticker，等待 DB 写线程做最终同步后关闭连接。
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
	}
}

// HandleFor 返回某类别 actor 的落地句柄（用于创建 actor 时注入）。
func (m *Manager) HandleFor(category string) actor.PersistHandle {
	spec, ok := m.specs[category]
	if !ok {
		logger.ERROR("persist: handle for unknown category:", category)
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
	return &handle{
		spec:   spec,
		codec:  m.codecs[spec.Codec],
		redis:  m.redis,
		mysql:  mysql,
		writer: m.writer,
	}
}

func (m *Manager) tickLoop(spec CategorySpec) {
	defer m.wg.Done()
	ticker := time.NewTicker(spec.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.tickCategory(spec.Category)
		}
	}
}

func (m *Manager) tickCategory(category string) {
	m.am.RangeByCategory(category, func(name string, ab *actor.ActorBase) bool {
		if _, ok := ab.Actor().(actor.Persistable); !ok {
			return true
		}
		if err := m.am.Send(name, "persist", actor.PersistTick{}); err != nil {
			logger.ERROR("persist tick send failed:", err, "actor:", name)
		}
		return true
	})
}
