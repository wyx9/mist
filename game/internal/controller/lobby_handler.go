package controller

import (
	"errors"
	"time"

	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
	"simple_game/game/internal/actor"
	"simple_game/game/internal/actor/entity"
	"simple_game/game/internal/route"
	"simple_game/game/pkg/logger"
)

// LobbyController 返回大厅在线玩家快照（经 Room actor RPC 获取，不直接读其状态）。
func LobbyController(ctx *route.HandlerContext[*actor.Player], req *pt.LobbyInfoReq) (proto.Message, error) {
	res, err := ctx.Comm.Call(actor.RoomActorName, entity.RoomGetPlayers{}, 5*time.Second)
	if err != nil {
		logger.ERROR("lobby call room failed:", err)
		return nil, err
	}
	players, ok := res.([]entity.RoomPlayerInfo)
	if !ok {
		logger.ERROR("lobby room reply type mismatch:", res)
		return nil, errors.New("room reply type mismatch")
	}
	out := &pt.LobbyInfoRes{Online: int32(len(players))}
	for _, p := range players {
		out.Players = append(out.Players, &pt.LobbyPlayer{UID: p.UID, Name: p.Name})
	}
	return out, nil
}
