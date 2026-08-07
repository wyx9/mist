package controller

import (
	"simple_game/game/api/protos/pt"
	"simple_game/game/internal/actor"
	"simple_game/game/internal/route"
	"simple_game/game/pkg/logger"

	"google.golang.org/protobuf/proto"
)

func ChatController(ctx *route.HandlerContext[*actor.Player], req *pt.SendChatReq) (proto.Message, error) {
	logger.DEBUG("[ChatController] userId:", ctx.Actor.ActorID(), "	msg:", req.Msg)
	return &pt.SendChatRsp{
		UUid: req.UUid,
	}, nil
}
