package controller

import (
	"time"

	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
	"simple_game/game/internal/actor"
	"simple_game/game/internal/route"
	"simple_game/game/pkg/logger"
)

// LoginController 处理登录请求：ctx.Actor 为当前玩家 actor，
// ctx.Msg 携带 uid/msgID/token，失败时返回 error 由路由统一下发错误响应。
func LoginController(ctx *route.HandlerContext[*actor.Player], req *pt.LoginReq) (proto.Message, error) {
	playerData := ctx.Actor.PlayerData
	playerData.IsLogin = true
	playerData.LastLoginAt = time.Now().Unix()

	// 通知常驻 Room actor 记录玩家加入（测试跨 actor 异步通信）
	if err := ctx.Comm.Cast(actor.RoomActorName, &pt.RoomPlayerJoin{
		UID:  ctx.Actor.ActorID(),
		Name: ctx.Actor.Name(),
	}); err != nil {
		logger.ERROR("send room join failed:", err)
	}

	return &pt.LoginRes{
		UUid: req.UUid,
		Code: 0,
	}, nil
}

// TestController 处理心跳请求。
func TestController(ctx *route.HandlerContext[*actor.Player], req *pt.HeartReq) (proto.Message, error) {
	return &pt.HeartRes{Time: time.Now().Unix()}, nil
}
