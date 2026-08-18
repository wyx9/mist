package controller

import (
	"simple_game/game/api/protos/pt"
	"simple_game/game/internal/actor"
	"simple_game/game/internal/rpc"
)

func RegisterRpc() {
	// 内部消息路由（method -> handler，跨进程透明）
	// category 为消息归属的 actor 类别：RPC 接收方在目标未运行时据此按需创建/复活。
	rpc.Register[*actor.Room, *pt.RoomPlayerJoin]("room.join", actor.RoomActorName, RoomPlayerJoinHandler)
	rpc.Register[*actor.Room, *pt.RoomPlayerLeave]("room.leave", actor.RoomActorName, RoomPlayerLeaveHandler)
	rpc.Register[*actor.Room, *pt.LobbyChatMessage]("lobby.chat", actor.RoomActorName, LobbyChatHandler)
	rpc.Register[*actor.Player, *pt.LobbyBroadcast]("lobby.broadcast", actor.PlayerActorName, LobbyBroadcastHandler)
	rpc.RegisterCall[*actor.Room, *pt.RoomGetPlayersReq, *pt.RoomGetPlayersRes]("room.get_players", actor.RoomActorName, RoomGetPlayersHandler)
	rpc.RegisterCall[*actor.Player, *pt.PlayerInfoReq, *pt.PlayerInfoRes]("player.get_info", actor.PlayerActorName, PlayerInfoInternalHandler)
}
