// game/handle_msg.go
package engine

import (
	"errors"
	"simple_game/game/libs"
	"simple_game/game/pkg"
	"simple_game/game/tunnel"
)

// handleMsg 处理来自 Agent 的单条消息。
// msg 是 TunnelMsg JSON 原始字节，conn 是该 Agent 隧道连接。
func handleMsg(conn pkg.NetConn, msg []byte, tunnelID string) error {
	tm := tunnel.UnpackTunnel(msg)
	if tm == nil {
		return errors.New("unpack tunnel msg error")
	}

	// 还原为 PacketMsg 交给 Actor 系统处理
	codePack := &libs.PacketMsg{
		Name: tm.Name,
		Data: tm.Data,
	}

	// 已认证的消息直接路由到 Actor
	return ActorManner.CastMsg(tunnelID, libs.EnCodePack(codePack))
}
