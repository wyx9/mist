package controller

import (
	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
	"simple_game/game/internal/actor"
	"simple_game/game/internal/rpc"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/protocol"
)

// 内部消息 handler（跨 actor 通信）。业务内部消息在此注册，
// actor 文件不再硬编码 type-switch；新增内部消息只改本文件 + register.go。

// RoomPlayerJoinHandler 玩家加入大厅（异步 Send）。
func RoomPlayerJoinHandler(ctx *rpc.HandlerContext[*actor.Room], req *pt.RoomPlayerJoin) error {
	r := ctx.Actor
	r.Players[req.UID] = req.Name
	logger.INFO("room player joined, uid:", req.UID, "name:", req.Name, "total:", len(r.Players))
	broadcastLobby(r, ctx.Comm, &pt.LobbyBroadcast{
		Kind:   pt.LobbyBroadcastKind_LOBBY_JOIN,
		UID:    req.UID,
		Name:   req.Name,
		Online: int32(len(r.Players)),
	})
	return nil
}

// RoomPlayerLeaveHandler 玩家离开大厅（异步 Send）。
func RoomPlayerLeaveHandler(ctx *rpc.HandlerContext[*actor.Room], req *pt.RoomPlayerLeave) error {
	r := ctx.Actor
	name, ok := r.Players[req.UID]
	if !ok {
		logger.WARNING("room player leave unknown, uid:", req.UID)
		return nil
	}
	delete(r.Players, req.UID)
	logger.INFO("room player left, uid:", req.UID, "total:", len(r.Players))
	broadcastLobby(r, ctx.Comm, &pt.LobbyBroadcast{
		Kind:   pt.LobbyBroadcastKind_LOBBY_LEAVE,
		UID:    req.UID,
		Name:   name,
		Online: int32(len(r.Players)),
	})
	return nil
}

// LobbyChatHandler 大厅聊天广播（异步 Send）。
func LobbyChatHandler(ctx *rpc.HandlerContext[*actor.Room], req *pt.LobbyChatMessage) error {
	broadcastLobby(ctx.Actor, ctx.Comm, &pt.LobbyBroadcast{
		Kind: pt.LobbyBroadcastKind_LOBBY_CHAT,
		UID:  req.UID,
		Name: req.Name,
		Msg:  req.Msg,
		Time: req.Time,
	})
	return nil
}

// RoomGetPlayersHandler 返回大厅在线玩家快照（同步 Call）。
func RoomGetPlayersHandler(ctx *rpc.HandlerContext[*actor.Room], req *pt.RoomGetPlayersReq) (*pt.RoomGetPlayersRes, error) {
	r := ctx.Actor
	out := &pt.RoomGetPlayersRes{}
	for uid, name := range r.Players {
		out.Players = append(out.Players, &pt.RoomPlayerInfo{UID: uid, Name: name})
	}
	return out, nil
}

// LobbyBroadcastHandler 玩家收到大厅广播后写回自己的客户端连接（异步 Send）。
func LobbyBroadcastHandler(ctx *rpc.HandlerContext[*actor.Player], req *pt.LobbyBroadcast) error {
	p := ctx.Actor
	// 断线重连宽限期内无连接时静默丢弃，避免噪音日志
	if !p.Connected() {
		return nil
	}
	var cmd uint16
	var bodyMsg proto.Message
	switch req.Kind {
	case pt.LobbyBroadcastKind_LOBBY_JOIN:
		cmd = protocol.CmdLobbyJoinPush
		bodyMsg = &pt.LobbyJoinPush{Player: &pt.LobbyPlayer{UID: req.UID, Name: req.Name}, Online: req.Online}
	case pt.LobbyBroadcastKind_LOBBY_LEAVE:
		cmd = protocol.CmdLobbyLeavePush
		bodyMsg = &pt.LobbyLeavePush{Player: &pt.LobbyPlayer{UID: req.UID, Name: req.Name}, Online: req.Online}
	case pt.LobbyBroadcastKind_LOBBY_CHAT:
		cmd = protocol.CmdChatPush
		bodyMsg = &pt.ChatNtf{UUid: req.UID, Msg: req.Msg, Name: req.Name, Time: req.Time}
	default:
		logger.WARNING("unknown lobby broadcast kind:", req.Kind, "player:", p.Name())
		return nil
	}
	body, err := proto.Marshal(bodyMsg)
	if err != nil {
		return err
	}
	out, err := protocol.EncodeMsg(&protocol.Msg{
		MsgType: protocol.MsgTypePush,
		Cmd:     cmd,
		UID:     req.UID,
		Body:    body,
	})
	if err != nil {
		return err
	}
	return p.WriteMessage(out)
}

// broadcastLobby 向当前大厅所有玩家逐个投递广播（对齐 skynet 广播模式：
// 持有接收方列表逐个发消息，全部走跨 actor 消息，不直接操作他人状态）。
func broadcastLobby(r *actor.Room, comm rpc.Comm, msg *pt.LobbyBroadcast) {
	for _, name := range r.Players {
		if err := comm.Cast(name, msg); err != nil {
			logger.ERROR("lobby broadcast send failed:", err, "to:", name)
		}
	}
}
