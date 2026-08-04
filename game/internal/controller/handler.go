package controller

import (
	"time"

	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
	"simple_game/game/internal/actor"
	"simple_game/game/internal/route"
)

// LoginController 处理登录请求：ctx.Actor 为当前玩家 actor，
// ctx.Msg 携带 uid/msgID/token，失败时返回 error 由路由统一下发错误响应。
func LoginController(ctx *route.HandlerContext[*actor.Player], req *pt.LoginReq) (proto.Message, error) {
	return &pt.LoginRes{
		UUid: req.UUid,
		Code: 0,
	}, nil
}

// TestController 处理心跳请求。
func TestController(ctx *route.HandlerContext[*actor.Player], req *pt.HeartReq) (proto.Message, error) {
	return &pt.HeartRes{Time: time.Now().Unix()}, nil
}
