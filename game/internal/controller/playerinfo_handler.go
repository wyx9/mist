package controller

import (
	"errors"
	"time"

	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
	"simple_game/game/internal/actor"
	"simple_game/game/internal/route"
	"simple_game/game/internal/rpc"
	"simple_game/game/pkg/logger"
)

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
