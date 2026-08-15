package controller

import (
	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
	"simple_game/game/internal/actor"
	"simple_game/game/internal/model"
	"simple_game/game/internal/route"
	"simple_game/game/pkg/logger"
)

func ChatController(ctx *route.HandlerContext[*actor.Player], req *pt.SendChatReq) (proto.Message, error) {
	record, err := model.Chat.SendMessage(ctx.Actor.ActorID(), ctx.Actor.Name(), req.Msg)
	if err != nil {
		return nil, err
	}

	if err := ctx.Comm.Send(actor.RoomActorName, &pt.LobbyChatMessage{
		UID:  record.UID,
		Name: record.Name,
		Msg:  record.Msg,
		Time: record.Time,
	}); err != nil {
		logger.ERROR("send lobby chat to room failed:", err)
	}

	return &pt.SendChatRsp{UUid: record.UID}, nil
}

func ChatHistoryController(ctx *route.HandlerContext[*actor.Player], req *pt.ChatHistoryReq) (proto.Message, error) {
	records, err := model.Chat.LoadHistory()
	if err != nil {
		return nil, err
	}

	out := &pt.ChatHistoryRes{}
	for _, record := range records {
		out.Messages = append(out.Messages, &pt.ChatNtf{
			UUid: record.UID,
			Msg:  record.Msg,
			Name: record.Name,
			Time: record.Time,
		})
	}
	return out, nil
}
