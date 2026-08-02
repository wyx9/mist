// internal/gameserver/server.go
package gameserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"simple_game/game/internal/actor"
	"simple_game/game/internal/dispatch"
	"simple_game/game/internal/route"
	"simple_game/game/pkg/codec"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/mysql"
	"simple_game/game/pkg/network"
	"simple_game/game/pkg/redis"
	"simple_game/game/rpc"
)

// Server Game 逻辑服单例。聚合所有组件，统一生命周期。
type Server struct {
	cfg *GameConfig

	// 网络层
	listener network.Listener

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
		instance = &Server{}
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
	route.Init()
	route.RegisteredRoute()
	actor.ActorManner.RegisterAll()
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

// serve 监听 Agent 连接并分发给 handler。
func (s *Server) serve(addr string, tokenSecret string) {
	parts := strings.Split(addr, ":")
	if len(parts) != 2 {
		logger.ERROR("invalid game listen addr:", addr)
		return
	}

	l, err := network.NewListener("tcp", parts[0], parts[1])
	if err != nil {
		logger.ERROR("game listener create failed:", err)
		return
	}
	s.listener = l
	logger.INFO("game server listening on", addr)

	for {
		conn, err := l.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "closed") {
				return
			}
			logger.ERROR("game accept error:", err)
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleAgentConn(conn, []byte(tokenSecret))
		}()
	}
}

// handshakeMsg 客户端握手消息。
type handshakeMsg struct {
	Token   string `json:"token"`
	Version int    `json:"version"`
}

// handleAgentConn 处理单条 Agent 隧道连接。
func (s *Server) handleAgentConn(conn network.NetConn, secret []byte) {
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	data, err := conn.ReadMessage()
	if err != nil {
		logger.ERROR("read handshake from agent failed:", err)
		return
	}

	hs := &handshakeMsg{}
	if err := json.Unmarshal(data, hs); err != nil {
		logger.ERROR("parse handshake failed:", err)
		return
	}

	playerName, err := s.verifyToken(hs.Token, secret)
	if err != nil {
		logger.ERROR("token verify failed:", err)
		_ = conn.WriteMessage([]byte(`{"error":"invalid token"}`))
		return
	}

	tunnelID := playerName
	s.agentConns.Store(tunnelID, conn)
	defer func() {
		s.ClosePlayer(tunnelID)
		s.agentConns.Delete(tunnelID)
	}()

	_, err = actor.StartNewPlayerActor(tunnelID, conn)
	if err != nil {
		logger.ERROR("start player actor failed:", err)
		return
	}

	_ = conn.WriteMessage(codec.PackTunnelRaw("HandshakeOk", nil, tunnelID))

	for {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if err := s.handleMsg(conn, data, tunnelID); err != nil {
			logger.ERROR("handleMsg error:", err)
		}
	}
}

// verifyToken 验证 JWT token，返回 player_name。
func (s *Server) verifyToken(tokenStr string, secret []byte) (string, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return "", err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return "", fmt.Errorf("invalid token claims")
	}
	sub, _ := claims.GetSubject()
	if sub == "" {
		return "", fmt.Errorf("missing sub claim")
	}
	return sub, nil
}

// ---- Facade 方法 ----

// CastMsg 异步消息投递到 Actor。
func (s *Server) CastMsg(name string, msg interface{}) error {
	return actor.ActorManner.CastMsg(name, msg)
}

// CallMsg 同步调用 Actor。
func (s *Server) CallMsg(name string, msg interface{}) (interface{}, error) {
	return actor.ActorManner.CallMsg(name, msg)
}

// Dispatch 按 tunnelID 哈希投递任务到 worker pool。
func (s *Server) Dispatch(tunnelID string, task func()) {
	if s.workerPool != nil {
		s.workerPool.Submit(tunnelID, task)
		return
	}
	task()
}

// ClosePlayer 关闭玩家 Actor。
func (s *Server) ClosePlayer(name string) {
	ab := actor.ActorManner.FindByCategory("player", name)
	if ab != nil {
		_ = ab.Shutdown(5 * time.Second)
	}
}

// RegisterActor 注册 Actor 到管理器。
func (s *Server) RegisterActor(a *actor.ActorBase) error {
	return actor.ActorManner.Add(a)
}

// ReplyToAgent 通过隧道回复 Agent。
func (s *Server) ReplyToAgent(tunnelID string, data []byte) error {
	val, ok := s.agentConns.Load(tunnelID)
	if !ok {
		return fmt.Errorf("agent conn not found: %s", tunnelID)
	}
	return val.(network.NetConn).WriteMessage(data)
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
	actor.ActorManner.ShutdownActors(5 * time.Second)

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
