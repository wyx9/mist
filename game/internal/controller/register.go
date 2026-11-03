package controller

import (
	"simple_game/game/api/protos/pt"
	"simple_game/game/internal/actor"
	"simple_game/game/internal/route"
	"simple_game/game/internal/rpc"
)

// RegisterAll 注册所有业务路由，新增协议在此加一行即可。
// 路由键由请求消息类型反射得到（Protobuf 消息名），不再维护命令号枚举；
// A 为处理该协议的 actor 具体类型，注册到 controller 层以避免
// controller → actor → route → controller 的 import 环。
func RegisterAll() {
	// 内部消息路由（请求消息名 -> handler，跨进程透明；category 供接收端按需创建/复活 actor）
	rpc.Register[*actor.Room, *pt.RoomPlayerJoin](actor.RoomActorName, RoomPlayerJoinHandler)
	rpc.Register[*actor.Room, *pt.RoomPlayerLeave](actor.RoomActorName, RoomPlayerLeaveHandler)
	rpc.Register[*actor.Room, *pt.LobbyChatMessage](actor.RoomActorName, LobbyChatHandler)
	rpc.Register[*actor.Player, *pt.LobbyBroadcast](actor.PlayerActorName, LobbyBroadcastHandler)
	rpc.RegisterCall[*actor.Room, *pt.RoomGetPlayersReq, *pt.RoomGetPlayersRes](actor.RoomActorName, RoomGetPlayersHandler)
	rpc.RegisterCall[*actor.Player, *pt.PlayerInfoReq, *pt.PlayerInfoRes](actor.PlayerActorName, PlayerInfoInternalHandler)

	// 客户端协议路由（请求消息名 -> handler，响应消息名由 handler 返回值反射得到）
	route.Register[*actor.Player, *pt.LoginReq](LoginController)
	route.Register[*actor.Player, *pt.HeartReq](TestController)

	route.Register[*actor.Player, *pt.SendChatReq](ChatController)
	route.Register[*actor.Player, *pt.LobbyInfoReq](LobbyController)
	route.Register[*actor.Player, *pt.ChatHistoryReq](ChatHistoryController)
	route.Register[*actor.Player, *pt.PlayerInfoReq](PlayerInfoController)

}
