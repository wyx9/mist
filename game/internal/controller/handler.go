package controller

import (
	"google.golang.org/protobuf/proto"
	"simple_game/game/api/protos/pt"
	engine "simple_game/game/engine"
	"time"
)

type AllController struct {
	BaseController
}

func LoginController(actor *engine.Player, req *pt.LoginReq) *pt.LoginRes {
	return &pt.LoginRes{
		UUid: req.UUid,
		Code: 0,
	}
}

func TestController(actor *engine.Player, req *pt.HeartReq) proto.Message {
	return &pt.HeartRes{Time: time.Now().Unix()}
}
