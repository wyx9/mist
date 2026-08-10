// Package configs 提供配置表产物与 Redis 热更管理。
// 生成代码位于 generated/（由 game/configs/gen 生成，勿手改），
// 本文件为手写的运行时管理器：优先从 Redis 加载配置，订阅 config:changed 热更。
package configs

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"

	"simple_game/game/pkg/logger"
)

// Redis key 与 pub/sub 通道约定（gen -push 推送时使用相同约定）。
const (
	KeyPrefix = "config:"        // config:{表名} 存放该表的 JSON 数组
	ChangedCh = "config:changed" // 热更通知通道，payload 为表名
)

//go:embed data/*.json
var dataFS embed.FS

// Table 配置表统一接口：Name 返回表名，Decode 从 JSON 字节构建并原子替换快照。
type Table interface {
	Name() string
	Decode([]byte) error
}

// Manager 配置热更管理器。单例，生命周期由 game server 驱动。
type Manager struct {
	rdb    *redis.Client
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

var mgr = &Manager{}

// Init 初始化全部配置表：优先从 Redis 加载；Redis 缺表时读内嵌数据并回填；
// Redis 故障时回退内嵌数据（记 ERROR，不阻断启动）。内嵌数据损坏才返回错误。
func Init(rdb *redis.Client) error {
	mgr.rdb = rdb
	for _, t := range All {
		var err error
		if rdb == nil {
			err = loadEmbedded(nil, t, false)
		} else {
			err = loadTable(rdb, t)
		}
		if err != nil {
			return err
		}
	}
	logger.INFO("configs loaded:", len(All), "tables")
	return nil
}

func loadTable(rdb *redis.Client, t Table) error {
	ctx := context.Background()
	key := KeyPrefix + t.Name()
	raw, err := rdb.Get(ctx, key).Bytes()
	switch {
	case err == nil:
		if derr := t.Decode(raw); derr != nil {
			return fmt.Errorf("config %s: redis data decode: %w", t.Name(), derr)
		}
		logger.INFO("config loaded from redis:", t.Name())
		return nil
	case errors.Is(err, redis.Nil):
		return loadEmbedded(rdb, t, true)
	default:
		logger.ERROR("config redis get failed, fallback embedded:", t.Name(), err)
		return loadEmbedded(rdb, t, false)
	}
}

func loadEmbedded(rdb *redis.Client, t Table, backfill bool) error {
	raw, err := dataFS.ReadFile("data/" + t.Name() + ".json")
	if err != nil {
		return fmt.Errorf("config %s: embedded data missing: %w", t.Name(), err)
	}
	if err := t.Decode(raw); err != nil {
		return fmt.Errorf("config %s: embedded data decode: %w", t.Name(), err)
	}
	if backfill && rdb != nil {
		if err := rdb.Set(context.Background(), KeyPrefix+t.Name(), raw, 0).Err(); err != nil {
			logger.ERROR("config backfill redis failed:", t.Name(), err)
		}
	}
	logger.INFO("config loaded from embedded:", t.Name())
	return nil
}

// StartWatch 启动热更监听：订阅 config:changed，收到表名即从 Redis 重载该表。
func StartWatch() {
	if mgr.rdb == nil {
		logger.WARNING("config watch skipped: redis not ready")
		return
	}
	mgr.ctx, mgr.cancel = context.WithCancel(context.Background())
	mgr.wg.Add(1)
	go mgr.watchLoop()
}

func (m *Manager) watchLoop() {
	defer m.wg.Done()
	sub := m.rdb.Subscribe(m.ctx, ChangedCh)
	defer sub.Close()
	ch := sub.Channel()
	for {
		select {
		case <-m.ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			m.reload(msg.Payload)
		}
	}
}

func (m *Manager) reload(name string) {
	var t Table
	for _, x := range All {
		if x.Name() == name {
			t = x
			break
		}
	}
	if t == nil {
		logger.WARNING("config reload: unknown table:", name)
		return
	}
	raw, err := m.rdb.Get(context.Background(), KeyPrefix+name).Bytes()
	if err != nil {
		logger.ERROR("config reload failed:", name, err)
		return
	}
	if err := t.Decode(raw); err != nil {
		logger.ERROR("config reload decode failed:", name, err)
		return
	}
	logger.INFO("config reloaded:", name)
}

// StopWatch 停止热更监听并等待协程退出。
func StopWatch() {
	if mgr.cancel == nil {
		return
	}
	mgr.cancel()
	done := make(chan struct{})
	go func() {
		mgr.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		logger.ERROR("config watch stop timeout")
	}
}
