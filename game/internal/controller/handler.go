package controller

import (
	"time"

	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
)

type AllController struct {
	BaseController
}

func LoginController(player interface{}, req *pt.LoginReq) *pt.LoginRes {
	return &pt.LoginRes{
		UUid: req.UUid,
		Code: 0,
	}
}

func TestController(player interface{}, req *pt.HeartReq) proto.Message {
	return &pt.HeartRes{Time: time.Now().Unix()}
}
