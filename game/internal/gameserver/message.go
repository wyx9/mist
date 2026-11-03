// internal/gameserver/message.go
package gameserver

import (
	"fmt"

	"simple_game/game/pkg/protocol"
)

// heartMsgName 心跳请求消息名：系统消息，允许缺省 uid。
const heartMsgName = "HeartReq"

// handleMsg 将 Gateway 转发来的业务帧解析为消息信封，校验后投递到本地 actor。
// 只返回错误不记日志：日志统一由 handleTunnelFrame / handleGatewayLink 分层处理，
// 避免同一错误在多层重复输出。
// 同一 tunnel 的帧由单条连接的读协程序序处理，actor 队列自身保证串行有序；
// Deliver 仅在 actor 队列满时阻塞（背压），符合流量控制语义。
func (s *Server) handleMsg(msg []byte, tunnelID string) error {
	uid, ok := tunnelUID(tunnelID)
	if !ok {
		return fmt.Errorf("invalid tunnel uid: %s", tunnelID)
	}
	m, err := protocol.DecodeMsg(msg)
	if err != nil {
		return err
	}
	if err := s.authorizeMsg(uid, m); err != nil {
		return err
	}
	return s.ActorManner.Deliver(uid, m)
}

// authorizeMsg 统一客户端消息 UID 策略：
//   - 消息类型必须为请求（MsgTypeReq），客户端只发请求；
//   - bound 为握手绑定的 uid（由 tunnelID 解析，tunnelID 恒等于 uid 十进制串）；
//   - 心跳（heartMsgName）为系统消息：允许缺省 uid（UID=0 放行），非 0 时须与 bound 一致；
//   - 其余业务消息：必须携带非 0 且与 bound 一致的 UID，否则拒绝。
//
// token 鉴权已收敛到 gateway（握手时一次验签），业务消息不再校验 token。
func (s *Server) authorizeMsg(bound int64, m *protocol.Msg) error {
	if m.MsgType != protocol.MsgTypeReq {
		return fmt.Errorf("invalid msg type: %d, uid=%d", m.MsgType, bound)
	}
	if m.Name == heartMsgName {
		if m.UID != 0 && m.UID != bound {
			return fmt.Errorf("uid mismatch: uid=%d bound=%d", m.UID, bound)
		}
		return nil
	}
	if m.UID == 0 {
		return fmt.Errorf("uid missing: uid=%d", bound)
	}
	if m.UID != bound {
		return fmt.Errorf("uid mismatch: uid=%d bound=%d", m.UID, bound)
	}
	return nil
}
