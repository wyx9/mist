package route

import (
	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
	"simple_game/game/internal/controller"
)

func RegisteredRoute() {
	allController := controller.AllController{}

	AddRoute("LoginReq", func(ctx interface{}, req any) proto.Message {
		player := allController.Ctx(ctx)
		return controller.LoginController(player, req.(*pt.LoginReq))
	})

	// 测试
	AddRoute("HeartReq", func(ctx interface{}, req any) proto.Message {
		player := allController.Ctx(ctx)
		return controller.TestController(player, req.(*pt.HeartReq))
	})

}
