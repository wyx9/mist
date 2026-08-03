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
	"simple_game/game/internal/route"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/mysql"
	"simple_game/game/pkg/redis"
	"simple_game/game/rpc"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"simple_game/game/internal/actor"
	"simple_game/game/internal/dispatch"
	"simple_game/game/pkg/network"
)

// Server Game 逻辑服单例。聚合所有组件，统一生命周期。
type Server struct {
	cfg *GameConfig

	// 网络层
	listener network.Listener

	// Actor管理器（全局单例，跟随 Server 生命周期）
	ActorManner *actor.AManager

	// Agent 连接表 — tunnelID → network.NetConn
	agentConns sync.Map

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
			ActorManner: &actor.AManager{},
		}
	})
	return instance
}

// Instance 返回全局 Server 单例。未初始化时返回 nil。
func Instance() *Server { return instance }

var cfgPath = "game/config/config.yaml"

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

	// Phase 3: 业务初始化
	route.RegisteredRoute()
	s.ActorManner.RegisterAll()
	s.workerPool = dispatch.NewWorkerPool(0) // CPU 核心数

	// Phase 4: 对外服务 (goroutine)
	s.ctx, s.cancel = context.WithCancel(context.Background())

	go rpc.StartGRPC()

	addr := fmt.Sprintf("%s:%s", cfg.Listen.Addr, cfg.Listen.Port)
	go s.serve(addr, cfg.TokenSecret)

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

	// 2. 关闭所有 Agent 隧道
	s.agentConns.Range(func(key, val interface{}) bool {
		_ = val.(network.NetConn).Close()
		return true
	})

	// 3. 关闭所有 Actor
	s.ActorManner.ShutdownActors(5 * time.Second)

	// 4. 关闭 worker pool
	if s.workerPool != nil {
		s.workerPool.Shutdown()
	}

	// 5. 取消 context
	if s.cancel != nil {
		s.cancel()
	}

	// 6. 等待所有连接处理完成
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

	logger.INFO("Game server stopped")
	return nil
}
