package controller

import (
	"time"

	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
)

type AllController struct {
	BaseController
}

func LoginController(ctx any, req *pt.LoginReq) proto.Message {
	return &pt.LoginRes{
		UUid: req.UUid,
		Code: 0,
	}
}

func TestController(ctx any, req *pt.HeartReq) proto.Message {
	return &pt.HeartRes{Time: time.Now().Unix()}
}
