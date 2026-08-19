// gateway/game_link.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-redis/redis/v8"
	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
	"simple_game/game/pkg/codec"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/network"
	"simple_game/game/pkg/registry"
	"simple_game/game/pkg/tool"
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
// 同时作为内部 RPC 的转发通道：game 之间的 RPC 请求/响应经此长连接转发。
type gameLink struct {
	addr     string
	conn     network.NetConn
	writeMu  sync.Mutex
	rdb      *redis.Client
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
			go connectGame(rdb, addr)
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

func connectGame(rdb *redis.Client, addr string) {
	conn, err := network.Dial("tcp", addr)
	if err != nil {
		logger.ERROR("dial game failed:", addr, err)
		return
	}
	logger.INFO("game connected:", addr)
	link := &gameLink{addr: addr, conn: conn, rdb: rdb}
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
		case codec.TunnelRPC:
			l.handleRPC(payload)
		}
	}
	l.closeAllSessions()
}

// handleRPC 处理 game 转发来的内部 RPC 帧：请求按 target 解析节点转发，响应按 origin_node 回程。
func (l *gameLink) handleRPC(payload []byte) {
	frame := &pt.RpcFrame{}
	if err := proto.Unmarshal(payload, frame); err != nil {
		logger.ERROR("gateway parse rpc frame failed:", err)
		return
	}
	switch frame.Kind {
	case pt.RpcKind_RPC_REQUEST:
		l.routeRequest(frame)
	case pt.RpcKind_RPC_RESPONSE:
		l.routeResponse(frame)
	}
}

// routeRequest 解析目标 actor（按 uid）所在节点并转发请求；失败时回错误响应。
func (l *gameLink) routeRequest(frame *pt.RpcFrame) {
	addr, found, err := registry.ResolveActorLoc(context.Background(), l.rdb, frame.UID)
	if err != nil {
		l.replyError(frame, "resolve actor location: "+err.Error())
		return
	}
	if !found {
		l.replyError(frame, fmt.Sprintf("actor not found: uid=%d", frame.UID))
		return
	}
	target, ok := gameLinks.Load(addr)
	if !ok {
		l.replyError(frame, "game unavailable: "+addr)
		return
	}
	if err := target.(*gameLink).writeRPC(frame); err != nil {
		l.replyError(frame, "forward rpc failed: "+err.Error())
	}
}

// routeResponse 按 origin_node 把响应转发回发起方节点。
func (l *gameLink) routeResponse(frame *pt.RpcFrame) {
	target, ok := gameLinks.Load(frame.OriginNode)
	if !ok {
		logger.ERROR("gateway rpc response origin node unavailable:", frame.OriginNode)
		return
	}
	if err := target.(*gameLink).writeRPC(frame); err != nil {
		logger.ERROR("gateway forward rpc response failed:", err)
	}
}

// writeRPC 把 RpcFrame 序列化后经本长连接写回。
func (l *gameLink) writeRPC(frame *pt.RpcFrame) error {
	data, err := proto.Marshal(frame)
	if err != nil {
		return err
	}
	return l.writeFrame(codec.TunnelRPC, "", data)
}

// replyError 对失败的请求回错误响应（仅 Call 需要回包）。
func (l *gameLink) replyError(frame *pt.RpcFrame, msg string) {
	if !frame.IsCall {
		logger.ERROR("gateway rpc request failed:", msg, "uid:", frame.UID)
		return
	}
	resp := &pt.RpcFrame{
		Kind:        pt.RpcKind_RPC_RESPONSE,
		ReqId:       frame.ReqId,
		OriginNode:  frame.OriginNode,
		OriginActor: tool.ToString(frame.UID),
		Error:       msg,
	}
	target, ok := gameLinks.Load(frame.OriginNode)
	if !ok {
		logger.ERROR("gateway reply error origin node unavailable:", frame.OriginNode)
		return
	}
	if err := target.(*gameLink).writeRPC(resp); err != nil {
		logger.ERROR("gateway reply error failed:", err)
	}
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

// closeAllSessions 长连接断开：关闭其上所有客户端会话、清理该节点的 actor 位置并删除连接。
func (l *gameLink) closeAllSessions() {
	l.sessions.Range(func(key, value interface{}) bool {
		closeClientSession(value.(*Session))
		return true
	})
	// 节点失联：清理指向该节点的 actor 位置记录（best-effort，最终一致）
	if l.rdb != nil {
		registry.SweepActorLoc(context.Background(), l.rdb, l.addr)
	}
	l.closed.Store(true)
	gameLinks.Delete(l.addr)
	_ = l.conn.Close()
}

// closeClientSession 关闭一个客户端会话（幂等；仅在仍是最新会话时删除，避免顶替竞态）。
// 返回该会话在被关闭时是否仍是当前会话：true 表示由它负责通知 game 拆除隧道。
func closeClientSession(s *Session) bool {
	current := false
	if cur, ok := sessionMap.Load(s.TunnelID); ok && cur == s {
		sessionMap.Delete(s.TunnelID)
		current = true
	}
	if l, ok := gameLinks.Load(s.GameAddr); ok {
		link := l.(*gameLink)
		if cur, ok := link.sessions.Load(s.TunnelID); ok && cur == s {
			link.sessions.Delete(s.TunnelID)
		}
	}
	_ = s.ClientConn.Close()
	return current
}
