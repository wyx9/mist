package gameserver

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/mysql"
	"simple_game/game/pkg/redis"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"simple_game/game/configs"
	"simple_game/game/internal/actor"
	core "simple_game/game/internal/actor/core"
	"simple_game/game/internal/controller"
	"simple_game/game/internal/persist"
	"simple_game/game/internal/rpc"
	"simple_game/game/pkg/network"
	"simple_game/game/pkg/registry"
	"simple_game/game/pkg/timer"
	"simple_game/game/pkg/uid"
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

	// 内部 RPC 客户端（跨进程透明路由）
	rpcClient *rpc.Client

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

// Start 启动 Game 服务：按依赖顺序初始化各组件；opts 留空时自动探测配置、无覆盖。
func (s *Server) Start(opts StartOptions) error {
	// Phase 1: 配置 + 日志
	cfgPath := opts.ConfigPath
	if cfgPath == "" {
		cfgPath = findConfigPath("game")
	}
	cfg, err := LoadGameConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if opts.ListenPort != "" {
		cfg.Listen.Port = opts.ListenPort
	}
	if opts.RegisterAddr != "" {
		cfg.RegisterAddr = opts.RegisterAddr
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
	uid.Init(redis.RedisClient)
	if err := configs.Init(redis.RedisClient); err != nil {
		return fmt.Errorf("init configs: %w", err)
	}
	configs.StartWatch()

	// 本节点注册地址：须在启动任何 actor 前注入 AManager，供位置认领/注册使用。
	addr := fmt.Sprintf("%s:%s", cfg.Listen.Addr, cfg.Listen.Port)
	s.regAddr = cfg.RegisterAddr
	if s.regAddr == "" {
		s.regAddr = addr
	}
	s.ActorManner.SetAddr(s.regAddr)

	// Phase 3: 业务初始化
	controller.RegisterAll()
	actor.RegisterAll(s.ActorManner)
	actor.StartDaemonActor(s.ActorManner)

	// actor 故障（handler 超时）→ 异步踢掉该玩家连接
	s.ActorManner.SetFaultHandler(s.onActorFault)
	s.ActorManner.Start()
	persistMgr, err := newPersistManager(cfg, s.ActorManner)
	if err != nil {
		return fmt.Errorf("init persistence: %w", err)
	}
	if err := persistMgr.Start(); err != nil {
		return fmt.Errorf("start persistence: %w", err)
	}
	s.persist = persistMgr

	// 注册 player 工厂：按需启动/复活玩家 actor 时使用（持久化句柄来自 persist 管理器）
	if err := s.ActorManner.RegisterFactory(actor.ActorTypePlayer, s.newPlayerActor); err != nil {
		return fmt.Errorf("register player factory: %w", err)
	}

	// Phase 4: 对外服务 (goroutine)
	s.ctx, s.cancel = context.WithCancel(context.Background())

	// 内部 RPC 客户端：注入 actor 管理器（远程路由 + 位置注册 + 初始化记录）
	s.rpcClient = rpc.NewClient(s.regAddr)
	s.ActorManner.SetRemoteRouter(s.rpcClient)

	// 全局定时器轮询协程：每秒从 Redis 取到期任务投递给目标 actor
	s.wg.Add(1)
	go s.runGlobalTimer()

	go s.serve(addr)

	// 注册 game 地址到 Redis 并启动心跳
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

	// 5. 停配置热更监听（须在关闭 Redis 连接之前）
	configs.StopWatch()

	// 6. 取消 context
	if s.cancel != nil {
		s.cancel()
	}

	// 7. 注销 game 注册（心跳协程已随 cancel 退出）
	if s.regAddr != "" {
		if err := registry.Unregister(context.Background(), redis.RedisClient, s.regAddr); err != nil {
			logger.ERROR("game registry unregister failed:", err)
		}
	}

	// 8. 等待所有连接处理完成
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

	// 9. 关闭基础设施连接
	if mysql.DB != nil {
		_ = mysql.DB.Close()
	}
	if redis.RedisClient != nil {
		_ = redis.RedisClient.Close()
	}

	logger.INFO("Game server stopped")
	return nil
}

// runGlobalTimer 全局定时器轮询协程入口（随 s.ctx 取消而停止）。
func (s *Server) runGlobalTimer() {
	defer s.wg.Done()
	timer.RunGlobal(s.ctx, redis.RedisClient, timer.GlobalPollInterval, s.deliverGlobalTimer)
}

// deliverGlobalTimer 全局定时器到期投递：向目标 actor 投递 GlobalFire 包装消息。
func (s *Server) deliverGlobalTimer(t *timer.GlobalTask) error {
	return s.ActorManner.Cast(t.Target, "timer", &timer.GlobalFire{ID: t.ID, Tag: t.Tag, Payload: t.Payload})
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

// onActorFault actor 故障回调（handler 超时等）：异步踢掉该玩家的隧道连接。
func (s *Server) onActorFault(uid int64) {
	go s.kickFaultedTunnel(uid)
}

// newPlayerActor player 工厂：按需启动/复活玩家 actor 时创建实例并注入持久化句柄。
func (s *Server) newPlayerActor(uid int64) (core.IActor, error) {
	return actor.NewPlayer(s.ActorManner, uid, s.persist.HandleFor(string(actor.ActorTypePlayer))), nil
}
