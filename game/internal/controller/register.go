package controller

import (
	"simple_game/game/api/protos/pt"
	"simple_game/game/internal/actor"
	"simple_game/game/internal/route"
	"simple_game/game/pkg/protocol"
)

// RegisterAll 注册所有业务路由，新增协议在此加一行即可。
// A 为处理该协议的 actor 具体类型，注册到 controller 层以避免
// controller → actor → route → controller 的 import 环。
func RegisterAll() {
	route.Register[*actor.Player, *pt.LoginReq](protocol.CmdLogin, LoginController)
	route.Register[*actor.Player, *pt.HeartReq](protocol.CmdHeart, TestController)
}
