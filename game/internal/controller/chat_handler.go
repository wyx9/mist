package controller

import (
	"simple_game/game/api/protos/pt"
	"simple_game/game/internal/actor"
	"simple_game/game/internal/model"
	"simple_game/game/internal/route"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/protocol"

	"google.golang.org/protobuf/proto"
)

func ChatController(ctx *route.HandlerContext[*actor.Player], req *pt.SendChatReq) (proto.Message, error) {
	if err := model.Chat.SendMessage(ctx.Actor.ActorID(), ctx.Actor.Name(), req.Msg); err != nil {
		return nil, err
	}
	logger.DEBUG("[ChatController] userId:", ctx.Actor.ActorID(), "	msg:", req.Msg)
	// 服务端主动推送：把聊天消息直接推给当前客户端（MsgTypePush）。
	if err := ctx.Push(protocol.CmdChatPush, &pt.ChatNtf{UUid: req.UUid, Msg: req.Msg}); err != nil {
		logger.ERROR("chat push failed:", err)
	}
	return &pt.SendChatRsp{
		UUid: req.UUid,
	}, nil
}
