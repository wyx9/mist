// internal/gameserver/server.go
// Server 聚合根与全局单例：持有所有组件。
// 职责拆分：生命周期见 lifecycle.go，网络接入见 network.go，
// 认证见 auth.go，Actor/消息门面见 facade.go，消息处理见 message.go。
package gameserver

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/mysql"
	"simple_game/game/pkg/redis"
	"simple_game/game/rpc"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"simple_game/game/configs"
	"simple_game/game/internal/actor"
	core "simple_game/game/internal/actor/core"
	"simple_game/game/internal/controller"
	"simple_game/game/internal/dispatch"
	"simple_game/game/internal/persist"
	"simple_game/game/pkg/network"
	"simple_game/game/pkg/registry"
)

// Server Game 逻辑服单例。聚合所有组件，统一生命周期。
type Server struct {
	cfg *GameConfig

	// 网络层
	listener network.Listener

	// Actor管理器（全局单例，跟随 Server 生命周期）
	ActorManner *core.AManager

	// 持久化管理器（生命周期跟随 Server）
	persist *persist.Manager

	// Gateway 连接表 — tunnelID → network.NetConn
	// 玩家隧道表 —— tunnelID → *TunnelConn
	tunnels sync.Map

	// Gateway 长连接表 —— *gatewayLink → *gatewayLink
	links sync.Map

	// 注册到 Redis 的 game 地址
	regAddr string

	// 登录后绑定的玩家 uid 表 — tunnelID → int64(uid)
	uidByTunnel sync.Map

	// 消息路由层
	workerPool *dispatch.WorkerPool

	// 生命周期
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	started atomic.Bool
	stopped atomic.Bool
}

// 单例
var (
	instance *Server
	once     sync.Once
)

// NewServer 创建 Server 单例。重复调用返回同一实例。
func NewServer() *Server {
	once.Do(func() {
		instance = &Server{
			ActorManner: &core.AManager{},
		}
	})
	return instance
}

// Instance 返回全局 Server 单例。未初始化时返回 nil。
func Instance() *Server { return instance }

var cfgPath = findConfigPath("game")

// Start 启动 Game 服务。按依赖顺序初始化所有组件。
func (s *Server) Start() error {
	// Phase 1: 配置 + 日志
	cfg, err := LoadGameConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	s.cfg = cfg

	logger.StartLog()
	if cfg.SaveLog {
		_ = logger.InitLogFile()
	}

	// Phase 2: 基础设施
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s",
		cfg.MySql.User, cfg.MySql.PassWord, cfg.MySql.Addr, cfg.MySql.Port, cfg.MySql.DBName)
	mysql.MysqlStart(dsn)
	redis.RedisStart(cfg.Redis.Addr, cfg.Redis.PassWord, cfg.Redis.DB)
	if err := configs.Init(redis.RedisClient); err != nil {
		return fmt.Errorf("init configs: %w", err)
	}
	configs.StartWatch()

	// Phase 3: 业务初始化
	controller.RegisterAll()
	actor.RegisterAll(s.ActorManner)
	// actor 故障（handler 超时）→ 异步踢掉该玩家连接
	s.ActorManner.SetFaultHandler(func(name string) {
		go s.kickFaultedTunnel(name)
	})
	s.ActorManner.Start()
	persistMgr, err := newPersistManager(cfg, s.ActorManner)
	if err != nil {
		return fmt.Errorf("init persistence: %w", err)
	}
	if err := persistMgr.Start(); err != nil {
		return fmt.Errorf("start persistence: %w", err)
	}
	s.persist = persistMgr
	s.workerPool = dispatch.NewWorkerPool(0) // CPU 核心数

	// Phase 4: 对外服务 (goroutine)
	s.ctx, s.cancel = context.WithCancel(context.Background())

	go rpc.StartGRPC()

	addr := fmt.Sprintf("%s:%s", cfg.Listen.Addr, cfg.Listen.Port)
	go s.serve(addr)

	// 注册 game 地址到 Redis 并启动心跳
	s.regAddr = cfg.RegisterAddr
	if s.regAddr == "" {
		s.regAddr = addr
	}
	if err := registry.Register(context.Background(), redis.RedisClient, s.regAddr); err != nil {
		logger.ERROR("game registry register failed:", err)
	}
	s.wg.Add(1)
	go s.gameHeartbeat()

	// Phase 5: 等待信号
	s.started.Store(true)
	logger.INFO("Game server started on", addr)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	return s.Shutdown(30 * time.Second)
}

// Shutdown 优雅关闭。按启动逆序清理资源。
func (s *Server) Shutdown(timeout time.Duration) error {
	if s.stopped.Swap(true) {
		return nil
	}
	logger.INFO("Game server shutting down...")

	// 1. 停止接受新连接
	if s.listener != nil {
		_ = s.listener.Close()
	}

	// 2. 关闭所有 Gateway 长连接（触发其上隧道清理）
	s.links.Range(func(key, val interface{}) bool {
		_ = val.(*gatewayLink).conn.Close()
		return true
	})

	// 3. 关闭所有 Actor
	s.ActorManner.Stop()
	s.ActorManner.ShutdownActors(5 * time.Second)

	// 4. 停持久化：先停 ticker，再做 Redis->MySQL 最终同步
	if s.persist != nil {
		s.persist.Stop(10 * time.Second)
	}

	// 4.5 停配置热更监听（须在关闭 Redis 连接之前）
	configs.StopWatch()

	// 5. 关闭 worker pool
	if s.workerPool != nil {
		s.workerPool.Shutdown()
	}

	// 6. 取消 context
	if s.cancel != nil {
		s.cancel()
	}

	// 6.5 注销 game 注册（心跳协程已随 cancel 退出）
	if s.regAddr != "" {
		if err := registry.Unregister(context.Background(), redis.RedisClient, s.regAddr); err != nil {
			logger.ERROR("game registry unregister failed:", err)
		}
	}

	// 7. 等待所有连接处理完成
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		logger.ERROR("shutdown timeout after", timeout)
	}

	// 8. 关闭基础设施连接
	if mysql.DB != nil {
		_ = mysql.DB.Close()
	}
	if redis.RedisClient != nil {
		_ = redis.RedisClient.Close()
	}

	logger.INFO("Game server stopped")
	return nil
}

// gameHeartbeat 周期向 Redis 续期 game 地址。
func (s *Server) gameHeartbeat() {
	defer s.wg.Done()
	ticker := time.NewTicker(registry.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if err := registry.Register(context.Background(), redis.RedisClient, s.regAddr); err != nil {
				logger.ERROR("game registry heartbeat failed:", err)
			}
		}
	}
}
