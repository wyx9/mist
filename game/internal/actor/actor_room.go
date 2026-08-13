package actor

import (
	"time"

	"simple_game/game/internal/actor/core"
	"simple_game/game/internal/actor/entity"
	"simple_game/game/pkg/logger"
)

// Room 常驻内存 Actor：仅保存登录玩家 uid -> 玩家名，不参与任何持久化。
// 实现 IdleTimeout() <= 0，sweeper 永不因空闲自动关闭；数据实体见 entity.Room。
type Room struct {
	entity.Room
	timer core.TimerHandle // actor 内部定时器句柄（消息式，框架注入）
}

// Start 初始化内存表。
func (r *Room) Start() {
	r.Players = make(map[int64]string)
	logger.INFO("room actor Start")
}

// Stop 停止时打印人数（纯内存，无需持久化清理）。
func (r *Room) Stop() {
	logger.INFO("room actor stop, players:", len(r.Players))
}

// Category 返回类别标识。
func (r *Room) Category() string {
	return "room"
}

// IdleTimeout 常驻 Actor：返回 0 表示永不因空闲自动关闭。
func (r *Room) IdleTimeout() time.Duration {
	return 0
}

// SetTimerHandle 实现 core.SetTimerHandle：创建阶段注入 actor 内部定时器句柄。
func (r *Room) SetTimerHandle(h core.TimerHandle) { r.timer = h }

// HandleMessage 处理内部消息：登录加入 / 断开移除。
func (r *Room) HandleMessage(ctx *core.ActorContext, msg interface{}) {
	switch m := msg.(type) {
	case entity.RoomPlayerJoin:
		r.Players[m.UID] = m.Name
		logger.INFO("room player joined, uid:", m.UID, "name:", m.Name, "total:", len(r.Players))
		r.broadcast(ctx, entity.LobbyBroadcast{
			Kind: entity.LobbyJoin, UID: m.UID, Name: m.Name, Online: len(r.Players),
		})
	case entity.RoomPlayerLeave:
		name, ok := r.Players[m.UID]
		if !ok {
			logger.WARNING("room player leave unknown, uid:", m.UID)
			return
		}
		delete(r.Players, m.UID)
		logger.INFO("room player left, uid:", m.UID, "total:", len(r.Players))
		r.broadcast(ctx, entity.LobbyBroadcast{
			Kind: entity.LobbyLeave, UID: m.UID, Name: name, Online: len(r.Players),
		})
	case entity.LobbyChatMessage:
		r.broadcast(ctx, entity.LobbyBroadcast{
			Kind: entity.LobbyChat,
			UID:  m.UID,
			Name: m.Name,
			Msg:  m.Msg,
			Time: m.Time,
		})
	case entity.RoomGetPlayers:
		players := make([]entity.RoomPlayerInfo, 0, len(r.Players))
		for uid, name := range r.Players {
			players = append(players, entity.RoomPlayerInfo{UID: uid, Name: name})
		}
		ctx.Reply(players)
	}
}

// broadcast 向当前所有玩家 actor 逐个投递大厅广播（对齐 skynet 广播模式：
// 服务持有接收方列表并逐个发消息，全部走跨 actor 消息，不直接操作他人状态）。
func (r *Room) broadcast(ctx *core.ActorContext, msg entity.LobbyBroadcast) {
	for _, name := range r.Players {
		if err := ctx.Send(name, msg); err != nil {
			logger.ERROR("lobby broadcast send failed:", err, "to:", name)
		}
	}
}
