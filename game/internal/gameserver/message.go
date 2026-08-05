// internal/gameserver/message.go
package gameserver

import (
	"fmt"

	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/protocol"
)

// handleMsg 将 Agent 转发来的业务帧解析为消息信封，校验后投递到 worker pool。
func (s *Server) handleMsg(msg []byte, tunnelID string) error {
	m, err := protocol.DecodeMsg(msg)
	if err != nil {
		return err
	}
	if err := s.authorizeMsg(tunnelID, m); err != nil {
		return err
	}
	// 通过 Server.Dispatch 投递到 worker pool，保证同一玩家消息有序
	s.Dispatch(tunnelID, func() {
		if err := s.Deliver(tunnelID, m); err != nil {
			logger.ERROR("Deliver error:", err, "tunnelID:", tunnelID)
		}
	})
	return nil
}

// authorizeMsg 只做廉价的 uid 一致性检查：
// token 鉴权已收敛到 agent（握手时一次验签），业务消息不再校验 token；
// 消息 uid 非 0 且与握手绑定的 uid 不一致时丢弃，心跳等未携带 uid 的消息不受限。
func (s *Server) authorizeMsg(tunnelID string, m *protocol.Msg) error {
	if bound, ok := s.uidByTunnel.Load(tunnelID); ok {
		if m.UID != 0 && m.UID != bound.(int64) {
			return fmt.Errorf("uid mismatch: tunnel=%s uid=%d bound=%d", tunnelID, m.UID, bound)
		}
	}
	return nil
}
