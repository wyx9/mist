package gameserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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

// tunnelUID 解析隧道 id（uid 的十进制字符串）为 uid。
func tunnelUID(tunnelID string) (int64, bool) {
	uid, err := strconv.ParseInt(tunnelID, 10, 64)
	return uid, err == nil && uid > 0
}

// gatewayLink gateway 长连接：一条连接上多路复用多个玩家隧道。
// 隧道统一登记在 Server.tunnels（全局表，TunnelConn 持有 link 引用），此处无独立索引。
type gatewayLink struct {
	conn    network.NetConn
	writeMu sync.Mutex
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
	s.tunnels.Range(func(key, value interface{}) bool {
		tc := value.(*TunnelConn)
		if tc.link == link {
			s.detachTunnel(key.(string), tc)
		}
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
		if _, ok := s.tunnels.Load(tunnelID); !ok {
			return fmt.Errorf("unknown tunnel: %s", tunnelID)
		}
		if err := s.handleMsg(payload, tunnelID); err != nil {
			switch {
			case errors.Is(err, core.ErrActorNotFound):
				// actor 不存在 = 会话状态失效：清理隧道（通知 gateway 踢连接），WARNING 级
				logger.WARNING("actor not found, detach tunnel:", tunnelID, "err:", err)
				if tc, ok := s.tunnels.Load(tunnelID); ok {
					s.detachTunnel(tunnelID, tc.(*TunnelConn))
				}
				return nil
			case errors.Is(err, core.ErrActorClose):
				// actor 关闭竞态：丢弃该消息，连接视为正常
				logger.WARNING("actor closing, drop message, tunnel:", tunnelID)
				return nil
			default:
				// 其余错误（帧/消息解析失败、uid 策略、投递其他错误）统一上抛，
				// 由 handleGatewayLink 记一条 ERROR（错误日志唯一出口）
				return err
			}
		}
		return nil
	case codec.TunnelKick:
		if tc, ok := s.tunnels.Load(tunnelID); ok {
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
// RPC_REQUEST 每请求一个 goroutine（serveRPCFrame）隔离同步等待（CallLocal 最长阻塞 timeout），
// 避免慢 RPC 卡住网关长连接的读循环（该连接承载所有玩家流量）；
// 有序性由目标 actor 的消息队列保证。RPC_RESPONSE 唤醒等待中的本地调用。
func (s *Server) handleRPCFrame(link *gatewayLink, payload []byte) error {
	frame := &pt.RpcFrame{}
	if err := rpc.UnmarshalFrame(payload, frame); err != nil {
		return err
	}
	switch frame.Kind {
	case pt.RpcKind_RPC_REQUEST:
		go s.serveRPCFrame(link, frame)
		return nil
	case pt.RpcKind_RPC_RESPONSE:
		if s.rpcClient != nil {
			s.rpcClient.OnResponse(frame)
		}
		return nil
	}
	return fmt.Errorf("unknown rpc frame kind: %d", frame.Kind)
}

// serveRPCFrame 在独立 goroutine 处理远端 RPC 请求（隔离同步等待，避免卡住读循环）。
func (s *Server) serveRPCFrame(link *gatewayLink, frame *pt.RpcFrame) {
	if err := rpc.Serve(s.ActorManner, frame, replyRPC(link)); err != nil {
		logger.ERROR("rpc serve failed:", err, "method:", frame.Method)
	}
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
	uid, ok := tunnelUID(tunnelID)
	if !ok || (hs.UID != 0 && hs.UID != uid) {
		return s.ackHandshake(link, tunnelID, false, "invalid tunnel uid")
	}
	playerName := hs.PlayerName

	// 复用策略：休眠中重连直接 Attach（不重建、不重载）；在线顶替关旧建新；不存在则新建/复活。
	ab := s.ActorManner.Find(uid)
	// 所有权校正：本地实例若已非 owner（位置被其他节点接管，跨节点顶替的孤儿）→ 关闭后按新建处理。
	if ab != nil {
		owner, err := s.ActorManner.IsOwner(uid)
		if err != nil {
			logger.ERROR("check actor ownership failed:", err, "uid:", uid)
		} else if !owner {
			logger.INFO("local actor not owner, close orphan and rebuild, uid:", uid)
			_ = s.ActorManner.CloseActor(uid, 5*time.Second)
			ab = nil
		}
	}
	reuse := ab != nil && !ab.Attached()
	if ab != nil && ab.Attached() {
		logger.INFO("replacing online actor (relogin), uid:", uid)
		if old, ok := s.tunnels.Load(tunnelID); ok {
			s.closeTunnel(tunnelID, old.(*TunnelConn))
		} else {
			_ = s.ActorManner.CloseActor(uid, 5*time.Second)
		}
	}
	if reuse && ab.ID() != uid {
		// 身份不一致（正常情况不会发生），硬关闭后重建，避免数据错绑
		logger.ERROR("reattach uid mismatch, rebuild actor, uid:", uid)
		_ = s.ActorManner.CloseActor(uid, 5*time.Second)
		reuse = false
	}

	tc := &TunnelConn{link: link, tunnelID: tunnelID, remote: link.conn.RemoteAddr()}
	if reuse {
		if err := s.ActorManner.Attach(uid, tc); err != nil {
			logger.ERROR("reattach actor failed, create new instead:", err, "uid:", uid)
			reuse = false
		}
	}
	if !reuse {
		// 接管所有权（覆盖写位置，幂等）后按需启动：未初始化→工厂新建；已回收→复活重载；name 由握手补齐。
		if err := s.ActorManner.ClaimActorLoc(uid); err != nil {
			logger.WARNING("claim actor location failed:", err, "uid:", uid)
		}
		ab, err := s.ActorManner.EnsureRunning(uid, "player")
		if err != nil {
			return s.ackHandshake(link, tunnelID, false, err.Error())
		}
		if p, ok := ab.Actor().(*actor.Player); ok {
			p.SetName(playerName)
		}
		if err := s.ActorManner.Attach(uid, tc); err != nil {
			logger.ERROR("attach new actor failed:", err, "uid:", uid)
		}
	}
	s.tunnels.Store(tunnelID, tc)
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

// closeTunnel 硬关闭指定隧道：关 actor、删全局索引，并通知 gateway 关闭客户端连接。
// 用于故障踢线等需要立即销毁 actor 的路径。
func (s *Server) closeTunnel(tunnelID string, tc *TunnelConn) {
	if uid, ok := tunnelUID(tunnelID); ok {
		_ = s.ActorManner.CloseActor(uid, 5*time.Second)
	}
	tc.Close()
	s.tunnels.Delete(tunnelID)
	if tc.link != nil {
		// 通知 gateway 关闭该玩家客户端连接
		_ = tc.link.writeFrame(codec.TunnelKick, tunnelID, nil)
	}
}

// detachTunnel 连接断开路径：关隧道、删索引，actor 保留进入休眠，等待重连复用。
func (s *Server) detachTunnel(tunnelID string, tc *TunnelConn) {
	tc.Close()
	s.tunnels.Delete(tunnelID)
	if uid, ok := tunnelUID(tunnelID); ok {
		if err := s.ActorManner.Detach(uid); err != nil &&
			!errors.Is(err, core.ErrActorNotFound) && !errors.Is(err, core.ErrActorClose) {
			logger.ERROR("detach actor error:", err, "uid:", uid)
		}
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
