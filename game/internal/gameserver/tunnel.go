// internal/gameserver/tunnel.go
package gameserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"simple_game/game/api/protos/pt"
	"simple_game/game/internal/actor"
	core "simple_game/game/internal/actor/core"
	"simple_game/game/internal/rpc"
	"simple_game/game/pkg/codec"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/network"
)

// gatewayLink gateway 长连接：一条连接上多路复用多个玩家隧道。
type gatewayLink struct {
	conn    network.NetConn
	writeMu sync.Mutex
	tunnels sync.Map // tunnelID -> *TunnelConn
}

func (l *gatewayLink) writeFrame(typ byte, tunnelID string, data []byte) error {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	return l.conn.WriteMessage(codec.PackTunnelFrame(typ, tunnelID, data))
}

// rpcTransport 实现 rpc.Transport：把 RpcFrame 序列化字节打包为 TunnelRPC 隧道帧写回 gateway。
type rpcTransport struct {
	link *gatewayLink
}

func (t *rpcTransport) WriteFrame(payload []byte) error {
	return t.link.writeFrame(codec.TunnelRPC, "", payload)
}

// TunnelConn 虚拟玩家连接：实现 network.NetConn，写操作封装为隧道帧发往 gateway 长连接。
type TunnelConn struct {
	link     *gatewayLink
	tunnelID string
	remote   string
	closed   atomic.Bool
}

func (c *TunnelConn) WriteMessage(data []byte) error {
	if c.closed.Load() {
		return errors.New("tunnel closed")
	}
	return c.link.writeFrame(codec.TunnelData, c.tunnelID, data)
}

func (c *TunnelConn) ReadMessage() ([]byte, error) {
	return nil, errors.New("tunnel conn read not supported")
}

func (c *TunnelConn) RemoteAddr() string { return c.remote }

func (c *TunnelConn) Close() error {
	c.closed.Store(true)
	return nil
}

func (c *TunnelConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *TunnelConn) SetWriteDeadline(t time.Time) error { return nil }

var _ network.NetConn = (*TunnelConn)(nil)

// tunnelHandshake gateway 转发来的身份握手。
type tunnelHandshake struct {
	PlayerName string `json:"player_name"`
	UID        int64  `json:"uid"`
	Version    int    `json:"version"`
}

// handleGatewayLink 处理一条 gateway 长连接：读隧道帧并分发。
func (s *Server) handleGatewayLink(link *gatewayLink) {
	defer link.conn.Close()
	// 挂载内部 RPC transport（当前单 gateway：直接用这条连接作为跨进程路由写路径）。
	if s.rpcClient != nil {
		s.rpcClient.SetTransport(&rpcTransport{link: link})
		defer s.rpcClient.OnDisconnect()
	}
	for {
		data, err := link.conn.ReadMessage()
		if err != nil {
			break
		}
		if err := s.handleTunnelFrame(link, data); err != nil {
			logger.ERROR("handle tunnel frame failed:", err)
		}
	}
	// 长连接断开：其上所有玩家隧道进入休眠（actor 保留，等待重连复用）
	logger.INFO("gateway link closed, detach tunnels")
	link.tunnels.Range(func(key, value interface{}) bool {
		s.detachTunnel(key.(string), value.(*TunnelConn))
		return true
	})
	s.links.Delete(link)
}

func (s *Server) handleTunnelFrame(link *gatewayLink, data []byte) error {
	typ, tunnelID, payload, err := codec.UnpackTunnelFrame(data)
	if err != nil {
		return err
	}
	switch typ {
	case codec.TunnelHandshake:
		return s.handleTunnelHandshake(link, tunnelID, payload)
	case codec.TunnelData:
		if _, ok := link.tunnels.Load(tunnelID); !ok {
			return fmt.Errorf("unknown tunnel: %s", tunnelID)
		}
		if err := s.handleMsg(payload, tunnelID); err != nil {
			logger.ERROR("handleMsg error:", err, "tunnel:", tunnelID)
		}
		return nil
	case codec.TunnelKick:
		if tc, ok := link.tunnels.Load(tunnelID); ok {
			s.detachTunnel(tunnelID, tc.(*TunnelConn))
		}
		return nil
	case codec.TunnelPing:
		return link.writeFrame(codec.TunnelPong, "", nil)
	case codec.TunnelPong:
		return nil
	case codec.TunnelRPC:
		return s.handleRPCFrame(link, payload)
	}
	return fmt.Errorf("unknown tunnel frame type: %d", typ)
}

// handleRPCFrame 处理 gateway 转发来的内部 RPC 帧。
// RPC_REQUEST 走统一 worker pool 投递到本地 actor（与客户端消息同池，保证同 actor 有序）；
// RPC_RESPONSE 唤醒等待中的本地调用。
func (s *Server) handleRPCFrame(link *gatewayLink, payload []byte) error {
	frame := &pt.RpcFrame{}
	if err := rpc.UnmarshalFrame(payload, frame); err != nil {
		return err
	}
	switch frame.Kind {
	case pt.RpcKind_RPC_REQUEST:
		s.Dispatch(frame.Target, func() {
			if err := rpc.Serve(s.ActorManner, frame, replyRPC(link)); err != nil {
				logger.ERROR("rpc serve failed:", err, "method:", frame.Method)
			}
		})
		return nil
	case pt.RpcKind_RPC_RESPONSE:
		if s.rpcClient != nil {
			s.rpcClient.OnResponse(frame)
		}
		return nil
	}
	return fmt.Errorf("unknown rpc frame kind: %d", frame.Kind)
}

// replyRPC 回写 RPC 响应帧到当前 gateway 长连接。
func replyRPC(link *gatewayLink) func(*pt.RpcFrame) error {
	return func(frame *pt.RpcFrame) error {
		data, err := rpc.MarshalFrame(frame)
		if err != nil {
			return err
		}
		return link.writeFrame(codec.TunnelRPC, "", data)
	}
}

func (s *Server) handleTunnelHandshake(link *gatewayLink, tunnelID string, payload []byte) error {
	hs := &tunnelHandshake{}
	if err := json.Unmarshal(payload, hs); err != nil {
		return s.ackHandshake(link, tunnelID, false, "invalid handshake payload")
	}
	playerName := hs.PlayerName
	if playerName == "" || playerName != tunnelID {
		return s.ackHandshake(link, tunnelID, false, "invalid player_name")
	}
	uid := hs.UID
	if uid == 0 {
		logger.WARNING("handshake without uid, player:", playerName)
	}

	// 复用策略：休眠中重连直接 Attach（不重建、不重载）；在线顶替关旧建新；不存在则新建。
	ab := s.ActorManner.Find(tunnelID)
	reuse := ab != nil && !ab.Attached()
	if ab != nil && ab.Attached() {
		logger.INFO("replacing online actor (relogin):", tunnelID)
		if old, ok := s.tunnels.Load(tunnelID); ok {
			s.closeTunnel(tunnelID, old.(*TunnelConn))
		} else {
			_ = s.ActorManner.CloseActor(tunnelID, 5*time.Second)
		}
	}
	if reuse && uid != 0 && uid != ab.ID() {
		// 身份不一致（正常情况不会发生），硬关闭后重建，避免数据错绑
		logger.ERROR("reattach uid mismatch, rebuild actor, player:", tunnelID)
		_ = s.ActorManner.CloseActor(tunnelID, 5*time.Second)
		reuse = false
	}

	tc := &TunnelConn{link: link, tunnelID: tunnelID, remote: link.conn.RemoteAddr()}
	s.uidByTunnel.Store(tunnelID, uid)
	var store core.PersistStore
	if s.persist != nil {
		store = s.persist.HandleFor("player")
	}
	if reuse {
		if err := s.ActorManner.Attach(tunnelID, tc); err != nil {
			logger.ERROR("reattach actor failed, create new instead:", err, "tunnel:", tunnelID)
			reuse = false
		}
	}
	if !reuse {
		// 先清理可能残留的同名旧 actor，再新建
		if err := s.ActorManner.CloseActor(tunnelID, 5*time.Second); err != nil &&
			!errors.Is(err, core.ErrActorNotFound) && !errors.Is(err, core.ErrActorClose) {
			logger.WARNING("close stale actor before handshake:", err, "tunnel:", tunnelID)
		}
		if _, err := actor.StartPlayer(s.ActorManner, tunnelID, uid, store); err != nil {
			s.uidByTunnel.Delete(tunnelID)
			return s.ackHandshake(link, tunnelID, false, err.Error())
		}
		if err := s.ActorManner.Attach(tunnelID, tc); err != nil {
			logger.ERROR("attach new actor failed:", err, "tunnel:", tunnelID)
		}
	}
	s.tunnels.Store(tunnelID, tc)
	link.tunnels.Store(tunnelID, tc)
	return s.ackHandshake(link, tunnelID, true, "")
}

func (s *Server) ackHandshake(link *gatewayLink, tunnelID string, ok bool, errMsg string) error {
	type ack struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	payload, _ := json.Marshal(&ack{OK: ok, Error: errMsg})
	return link.writeFrame(codec.TunnelHandshakeAck, tunnelID, payload)
}

// closeTunnel 硬关闭指定隧道：关 actor、删全局与所属 link 索引，并通知 gateway 关闭客户端连接。
// 用于故障踢线等需要立即销毁 actor 的路径。
func (s *Server) closeTunnel(tunnelID string, tc *TunnelConn) {
	_ = s.ActorManner.CloseActor(tunnelID, 5*time.Second)
	tc.Close()
	s.tunnels.Delete(tunnelID)
	if tc.link != nil {
		tc.link.tunnels.Delete(tunnelID)
		// 通知 gateway 关闭该玩家客户端连接
		_ = tc.link.writeFrame(codec.TunnelKick, tunnelID, nil)
	}
	s.uidByTunnel.Delete(tunnelID)
}

// detachTunnel 连接断开路径：关隧道、删索引，actor 保留进入休眠，等待重连复用。
func (s *Server) detachTunnel(tunnelID string, tc *TunnelConn) {
	tc.Close()
	s.tunnels.Delete(tunnelID)
	if tc.link != nil {
		tc.link.tunnels.Delete(tunnelID)
	}
	s.uidByTunnel.Delete(tunnelID)
	if err := s.ActorManner.Detach(tunnelID); err != nil &&
		!errors.Is(err, core.ErrActorNotFound) && !errors.Is(err, core.ErrActorClose) {
		logger.ERROR("detach actor error:", err, "tunnel:", tunnelID)
	}
}

// kickFaultedTunnel actor 故障（handler 超时）时踢掉其隧道连接，由故障回调异步调用。
func (s *Server) kickFaultedTunnel(name string) {
	val, ok := s.tunnels.Load(name)
	if !ok {
		return
	}
	s.closeTunnel(name, val.(*TunnelConn))
}
