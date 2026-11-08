package actor

import (
	"simple_game/game/internal/actor/core"
	"simple_game/game/pkg/actortype"
	"simple_game/game/pkg/logger"
)

const (
	ActorTypePlayer actortype.ActorType = "player"  // player 类型标识
	ActorTypeRoom   actortype.ActorType = "room"    // room 类型标识
	RoomUID                             = int64(-1) // 常驻 Room 的保留 uid（Redis INCR 从 1 起，不冲突）
)

// RegisterAll 统一注册业务 Actor 工厂（Server Phase 3 调用）。
func RegisterAll(am *core.AManager) {
	_ = am.RegisterFactory(ActorTypePlayer, func(uid int64) (core.IActor, error) {
		return NewPlayer(am, uid), nil
	})

	_ = am.RegisterFactory(ActorTypeRoom, func(uid int64) (core.IActor, error) {
		return &Room{}, nil
	})
	logger.INFO("actor RegisterAll complete")

	StartDaemonActor(am)
}

// StartDaemonActor 启动常驻的 actor（幂等，按需启动）。
// 使用 EnsureDaemon：常驻 actor 使用保留 uid（RoomUID），跳过 EnsureRunning 的 uid 合法性校验。
func StartDaemonActor(am *core.AManager) {
	if _, err := am.EnsureDaemon(RoomUID, ActorTypeRoom); err != nil {
		logger.ERROR("start room actor failed:", err)
		return
	}
	logger.INFO("room actor started")
}
