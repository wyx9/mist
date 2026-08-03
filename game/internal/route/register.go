package route

import (
	"simple_game/game/internal/controller"
	"simple_game/game/pkg/protocol"
)

// RegisteredRoute 注册所有业务路由，新增协议在此加一行即可。
func RegisteredRoute() {
	Register(protocol.CmdLogin, controller.LoginController)
	Register(protocol.CmdHeart, controller.TestController)
}
