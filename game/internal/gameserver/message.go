// internal/gameserver/message.go
package gameserver

import (
	"fmt"

	"simple_game/game/pkg/protocol"
)

// heartMsgName 心跳请求消息名：系统消息，允许缺省 uid。
const heartMsgName = "HeartReq"

// handleMsg 解析 Gateway 转发的业务帧并投递给本地 actor；错误由上层统一记录。
// 同一 tunnel 的帧由单条连接的读协程序行处理，actor 队列保证串行有序；
// Deliver 仅在队列满时阻塞，即背压。
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

// authorizeMsg 校验客户端消息 UID：必须为请求帧，且 UID 与握手绑定的 uid 一致；
// 心跳为系统消息，允许缺省 uid。token 鉴权已在 gateway 完成。
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
