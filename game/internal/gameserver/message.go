// internal/gameserver/message.go
package gameserver

import (
	"errors"

	"simple_game/game/pkg/codec"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/network"
)

// handleMsg 解包 Agent 消息并投递到 worker pool，由 worker 转发到 Actor 系统。
func (s *Server) handleMsg(conn network.NetConn, msg []byte, tunnelID string) error {
	tm := codec.UnpackTunnel(msg)
	if tm == nil {
		return errors.New("unpack tunnel msg error")
	}

	codePack := &codec.PacketMsg{
		Name: tm.Name,
		Data: tm.Data,
	}

	// 通过 Server.Dispatch 投递到 worker pool，保证同一玩家消息有序
	s.Dispatch(tunnelID, func() {
		if err := s.CastMsg(tunnelID, codec.EnCodePack(codePack)); err != nil {
			logger.ERROR("CastMsg error:", err, "tunnelID:", tunnelID)
		}
	})
	return nil
}
