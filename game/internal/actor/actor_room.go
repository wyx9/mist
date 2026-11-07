package actor

import (
	"time"

	"simple_game/game/internal/actor/core"
	"simple_game/game/internal/actor/entity"
	"simple_game/game/pkg/actortype"
	"simple_game/game/pkg/logger"
)

// Room 常驻内存 Actor：仅保存登录玩家 uid -> 玩家名，不持久化；sweeper 永不自动关闭。
type Room struct {
	entity.Room
	timer core.TimerHandle
}

func (r *Room) Start() {
	r.Players = make(map[int64]string)
	logger.INFO("room actor Start")
}

func (r *Room) Stop() {
	logger.INFO("room actor stop, players:", len(r.Players))
}

func (r *Room) Type() actortype.ActorType { return ActorTypeRoom }

// IdleTimeout 返回 0：常驻 Actor，永不因空闲自动关闭。
func (r *Room) IdleTimeout() time.Duration {
	return 0
}

func (r *Room) SetTimerHandle(h core.TimerHandle) { r.timer = h }
