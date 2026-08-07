// gateway/game_link.go
package main

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-redis/redis/v8"

	"simple_game/game/pkg/codec"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/network"
	"simple_game/game/pkg/registry"
)

const (
	gamePollInterval = 3 * time.Second
	linkPingInterval = 5 * time.Second
	linkReadTimeout  = 15 * time.Second
	handshakeTimeout = 5 * time.Second
)

// gameLinks gateway 当前已连接的 game 长连接表 —— addr → *gameLink
var gameLinks sync.Map

type handshakeResult struct {
	ok    bool
	error string
}

// gameLink 到单个 game 的长连接，上面多路复用多个玩家隧道。
type gameLink struct {
	addr     string
	conn     network.NetConn
	writeMu  sync.Mutex
	sessions sync.Map // tunnelID -> *Session
	pending  sync.Map // tunnelID -> chan handshakeResult
	closed   atomic.Bool
}

func (l *gameLink) writeFrame(typ byte, tunnelID string, data []byte) error {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	return l.conn.WriteMessage(codec.PackTunnelFrame(typ, tunnelID, data))
}

func (l *gameLink) sendData(tunnelID string, data []byte) error {
	return l.writeFrame(codec.TunnelData, tunnelID, data)
}

func (l *gameLink) sendKick(tunnelID string) error {
	return l.writeFrame(codec.TunnelKick, tunnelID, nil)
}

func (l *gameLink) sendPing() error {
	return l.writeFrame(codec.TunnelPing, "", nil)
}

// gamePollLoop 轮询注册表：连接新发现的 game，断开已消失的 game。
func gamePollLoop(rdb *redis.Client) {
	ticker := time.NewTicker(gamePollInterval)
	defer ticker.Stop()
	for range ticker.C {
		addrs, err := registry.ListFresh(context.Background(), rdb)
		if err != nil {
			logger.ERROR("list game servers failed:", err)
			continue
		}
		fresh := make(map[string]bool, len(addrs))
		for _, addr := range addrs {
			fresh[addr] = true
			if _, ok := gameLinks.Load(addr); ok {
				continue
			}
			go connectGame(addr)
		}
		gameLinks.Range(func(key, value interface{}) bool {
			addr := key.(string)
			if !fresh[addr] {
				logger.INFO("game server gone, disconnect:", addr)
				_ = value.(*gameLink).conn.Close()
			}
			return true
		})
	}
}

func connectGame(addr string) {
	conn, err := network.Dial("tcp", addr)
	if err != nil {
		logger.ERROR("dial game failed:", addr, err)
		return
	}
	logger.INFO("game connected:", addr)
	link := &gameLink{addr: addr, conn: conn}
	gameLinks.Store(addr, link)
	go link.readerLoop()
	go link.pingLoop()
}

func (l *gameLink) readerLoop() {
	for {
		_ = l.conn.SetReadDeadline(time.Now().Add(linkReadTimeout))
		data, err := l.conn.ReadMessage()
		if err != nil {
			break
		}
		typ, tunnelID, payload, err := codec.UnpackTunnelFrame(data)
		if err != nil {
			logger.ERROR("parse tunnel frame failed:", err, "game:", l.addr)
			continue
		}
		switch typ {
		case codec.TunnelHandshakeAck:
			if ch, ok := l.pending.Load(tunnelID); ok {
				l.pending.Delete(tunnelID)
				var ack struct {
					OK    bool   `json:"ok"`
					Error string `json:"error,omitempty"`
				}
				_ = json.Unmarshal(payload, &ack)
				ch.(chan handshakeResult) <- handshakeResult{ok: ack.OK, error: ack.Error}
			}
		case codec.TunnelData:
			if s, ok := l.sessions.Load(tunnelID); ok {
				sess := s.(*Session)
				logger.DEBUG("forward game->client:", len(payload), "player:", tunnelID)
				if err := sess.ClientConn.WriteMessage(payload); err != nil {
					closeClientSession(sess)
				}
			}
		case codec.TunnelKick:
			if s, ok := l.sessions.Load(tunnelID); ok {
				closeClientSession(s.(*Session))
			}
		case codec.TunnelPing:
			_ = l.writeFrame(codec.TunnelPong, "", nil)
		case codec.TunnelPong:
			// 保活信号：读超时已覆盖断链检测
		}
	}
	l.closeAllSessions()
}

func (l *gameLink) pingLoop() {
	ticker := time.NewTicker(linkPingInterval)
	defer ticker.Stop()
	for range ticker.C {
		if l.closed.Load() {
			return
		}
		if err := l.sendPing(); err != nil {
			_ = l.conn.Close()
			return
		}
	}
}

// closeAllSessions 长连接断开：关闭其上所有客户端会话并删除连接。
func (l *gameLink) closeAllSessions() {
	l.sessions.Range(func(key, value interface{}) bool {
		closeClientSession(value.(*Session))
		return true
	})
	l.closed.Store(true)
	gameLinks.Delete(l.addr)
	_ = l.conn.Close()
}

// closeClientSession 关闭一个客户端会话（幂等；仅在仍是最新会话时删除，避免顶替竞态）。
func closeClientSession(s *Session) {
	if cur, ok := sessionMap.Load(s.TunnelID); ok && cur == s {
		sessionMap.Delete(s.TunnelID)
	}
	if l, ok := gameLinks.Load(s.GameAddr); ok {
		link := l.(*gameLink)
		if cur, ok := link.sessions.Load(s.TunnelID); ok && cur == s {
			link.sessions.Delete(s.TunnelID)
		}
	}
	_ = s.ClientConn.Close()
}
