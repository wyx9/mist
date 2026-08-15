package controller

import (
	"simple_game/game/api/protos/pt"
	"simple_game/game/internal/actor"
	"simple_game/game/internal/rpc"
)

func RegisterRpc() {
	// 内部消息路由（method -> handler，跨进程透明）
	rpc.Register[*actor.Room, *pt.RoomPlayerJoin]("room.join", RoomPlayerJoinHandler)
	rpc.Register[*actor.Room, *pt.RoomPlayerLeave]("room.leave", RoomPlayerLeaveHandler)
	rpc.Register[*actor.Room, *pt.LobbyChatMessage]("lobby.chat", LobbyChatHandler)
	rpc.Register[*actor.Player, *pt.LobbyBroadcast]("lobby.broadcast", LobbyBroadcastHandler)
	rpc.RegisterCall[*actor.Room, *pt.RoomGetPlayersReq, *pt.RoomGetPlayersRes]("room.get_players", RoomGetPlayersHandler)
	rpc.RegisterCall[*actor.Player, *pt.PlayerInfoReq, *pt.PlayerInfoRes]("player.get_info", PlayerInfoInternalHandler)
}
