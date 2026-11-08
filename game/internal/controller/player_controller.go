package controller

import (
	"errors"
	"simple_game/game/internal/rpc"
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
	ctx.Actor.MarkDirty()

	// 通知常驻 Room actor 记录玩家加入（测试跨 actor 异步通信）
	if err := ctx.Comm.Cast(actor.RoomUID, &pt.RoomPlayerJoin{
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

// LobbyController 返回大厅在线玩家快照（经 Room actor RPC 获取，不直接读其状态）。
func LobbyController(ctx *route.HandlerContext[*actor.Player], req *pt.LobbyInfoReq) (proto.Message, error) {
	res, err := ctx.Comm.Call(actor.RoomUID, &pt.RoomGetPlayersReq{}, 5*time.Second)
	if err != nil {
		logger.ERROR("lobby call room failed:", err)
		return nil, err
	}
	players, ok := res.(*pt.RoomGetPlayersRes)
	if !ok {
		logger.ERROR("lobby room reply type mismatch:", res)
		return nil, errors.New("room reply type mismatch")
	}
	out := &pt.LobbyInfoRes{Online: int32(len(players.Players))}
	for _, p := range players.Players {
		out.Players = append(out.Players, &pt.LobbyPlayer{UID: p.UID, Name: p.Name})
	}
	return out, nil
}

// PlayerInfoController 查询指定玩家的信息（客户端入口）。
// 经 ctx.Comm.Call 透明跨进程：目标玩家在本地则本地调用，在其它节点则经 gateway 路由。
func PlayerInfoController(ctx *route.HandlerContext[*actor.Player], req *pt.PlayerInfoReq) (proto.Message, error) {
	if req.UUid <= 0 {
		return nil, errors.New("target player uid is empty")
	}
	res, err := ctx.Comm.Call(req.UUid, &pt.PlayerInfoReq{}, 5*time.Second)
	if err != nil {
		logger.ERROR("get player info failed:", err, "target:", req.UUid)
		return nil, err
	}
	info, ok := res.(*pt.PlayerInfoRes)
	if !ok {
		logger.ERROR("player info reply type mismatch:", res)
		return nil, errors.New("player info reply type mismatch")
	}
	return info, nil
}

// PlayerInfoInternalHandler 返回当前玩家自己的信息快照（内部 RPC 处理端，
// 注册键为请求消息名 PlayerInfoReq；目标 actor 收到请求后回自己的 uid/name/在线状态）。
func PlayerInfoInternalHandler(ctx *rpc.HandlerContext[*actor.Player], req *pt.PlayerInfoReq) (*pt.PlayerInfoRes, error) {
	p := ctx.Actor
	return &pt.PlayerInfoRes{
		UUid:        p.ActorID(),
		Name:        p.Name(),
		Online:      p.Connected(),
		LastLoginAt: p.PlayerData.LastLoginAt,
	}, nil
}
