// internal/gameserver/tunnel.go
package gameserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"simple_game/game/internal/actor"
	core "simple_game/game/internal/actor/core"
	"simple_game/game/pkg/codec"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/network"
)

// gatewayLink agent 长连接：一条连接上多路复用多个玩家隧道。
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

// TunnelConn 虚拟玩家连接：实现 network.NetConn，写操作封装为隧道帧发往 agent 长连接。
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

// tunnelHandshake agent 转发来的身份握手。
type tunnelHandshake struct {
	PlayerName string `json:"player_name"`
	UID        int64  `json:"uid"`
	Version    int    `json:"version"`
}

// handleGatewayLink 处理一条 agent 长连接：读隧道帧并分发。
func (s *Server) handleGatewayLink(link *gatewayLink) {
	defer link.conn.Close()
	for {
		data, err := link.conn.ReadMessage()
		if err != nil {
			break
		}
		if err := s.handleTunnelFrame(link, data); err != nil {
			logger.ERROR("handle tunnel frame failed:", err)
		}
	}
	// 长连接断开：清理其上所有隧道与玩家
	logger.INFO("gateway link closed, cleanup tunnels")
	link.tunnels.Range(func(key, value interface{}) bool {
		tid := key.(string)
		s.closeTunnel(tid, value.(*TunnelConn))
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
			s.closeTunnel(tunnelID, tc.(*TunnelConn))
		}
		return nil
	case codec.TunnelPing:
		return link.writeFrame(codec.TunnelPong, "", nil)
	case codec.TunnelPong:
		return nil
	}
	return fmt.Errorf("unknown tunnel frame type: %d", typ)
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
	// 同名顶替防御：先关旧隧道/旧 actor 再建新
	if old, ok := s.tunnels.Load(tunnelID); ok {
		logger.INFO("replacing old tunnel (relogin):", tunnelID)
		s.closeTunnel(tunnelID, old.(*TunnelConn))
	}
	uid := hs.UID
	if uid == 0 {
		logger.WARNING("handshake without uid, player:", playerName)
	}

	tc := &TunnelConn{link: link, tunnelID: tunnelID, remote: link.conn.RemoteAddr()}
	s.uidByTunnel.Store(tunnelID, uid)
	var store core.PersistStore
	if s.persist != nil {
		store = s.persist.HandleFor("player")
	}
	if _, err := actor.StartPlayer(s.ActorManner, tunnelID, uid, tc, store); err != nil {
		s.uidByTunnel.Delete(tunnelID)
		return s.ackHandshake(link, tunnelID, false, err.Error())
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

// closeTunnel 关闭指定隧道：关 actor、删全局与所属 link 索引。
func (s *Server) closeTunnel(tunnelID string, tc *TunnelConn) {
	_ = s.ActorManner.CloseActor(tunnelID, 5*time.Second)
	tc.Close()
	s.tunnels.Delete(tunnelID)
	if tc.link != nil {
		tc.link.tunnels.Delete(tunnelID)
		// 通知 agent 关闭该玩家客户端连接
		_ = tc.link.writeFrame(codec.TunnelKick, tunnelID, nil)
	}
	s.uidByTunnel.Delete(tunnelID)
}

// kickFaultedTunnel actor 故障（handler 超时）时踢掉其隧道连接，由故障回调异步调用。
func (s *Server) kickFaultedTunnel(name string) {
	val, ok := s.tunnels.Load(name)
	if !ok {
		return
	}
	s.closeTunnel(name, val.(*TunnelConn))
}
