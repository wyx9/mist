// internal/gameserver/message.go
package gameserver

import (
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/protocol"
)

// handleMsg 将 Agent 转发来的业务帧投递到 worker pool，由 worker 转发到 Actor 系统。
func (s *Server) handleMsg(msg []byte, tunnelID string) error {
	// 校验帧格式：至少包含 2 字节 cmd
	if _, _, err := protocol.DecodeFrame(msg); err != nil {
		return err
	}
	// 通过 Server.Dispatch 投递到 worker pool，保证同一玩家消息有序
	s.Dispatch(tunnelID, func() {
		if err := s.Deliver(tunnelID, msg); err != nil {
			logger.ERROR("Deliver error:", err, "tunnelID:", tunnelID)
		}
	})
	return nil
}
