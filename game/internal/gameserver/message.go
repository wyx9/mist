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

// authorizeMsg 校验消息头并维护 uid 绑定：
//   - CmdLogin：若携带 token 则 JWT 校验且 sub 必须等于握手绑定的 playerName，成功后绑定 header.UID 到隧道；
//   - 其他消息：若隧道已绑定 uid，则要求 header.UID 一致，不一致丢弃；
//   - 未登录前的消息（如心跳）不受限，保持现有测试客户端直连流程可用。
func (s *Server) authorizeMsg(tunnelID string, m *protocol.Msg) error {
	if m.Cmd == protocol.CmdLogin {
		if m.Token != "" {
			playerName, _, err := s.verifyToken(m.Token, []byte(s.cfg.TokenSecret))
			if err != nil || playerName != tunnelID {
				return fmt.Errorf("login token verify failed: tunnel=%s err=%v", tunnelID, err)
			}
		}
		if m.UID > 0 {
			s.uidByTunnel.Store(tunnelID, m.UID)
		}
		return nil
	}
	if bound, ok := s.uidByTunnel.Load(tunnelID); ok {
		if m.UID != bound.(int64) {
			return fmt.Errorf("uid mismatch: tunnel=%s uid=%d bound=%d", tunnelID, m.UID, bound)
		}
	}
	return nil
}
