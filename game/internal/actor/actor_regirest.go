package actor

import (
	"simple_game/game/internal/actor/core"
	"simple_game/game/pkg/logger"
)

const (
	PlayerActorName = "player"  // player 类别名
	RoomActorName   = "room"    // room 类别名
	RoomUID         = int64(-1) // 常驻 Room 的保留 uid（Redis INCR 从 1 起，不冲突）
)

// RegisterAll 统一注册业务 Actor 类别与工厂（Server Phase 3 调用）。
// player 工厂在 gameserver 注册（需 persist store 闭包）；room 工厂在此注册（常驻 daemon）。
func RegisterAll(am *core.AManager) {
	am.RegisterCategory(&Player{})
	am.RegisterCategory(&Room{})
	_ = am.RegisterFactory(RoomActorName, func(uid int64) (core.IActor, error) {
		return &Room{}, nil
	})
	logger.INFO("actor RegisterAll complete")
}

// StartDaemonActor 启动常驻的 actor（幂等，按需启动）。
// 使用 EnsureDaemon：常驻 actor 使用保留 uid（RoomUID），跳过 EnsureRunning 的 uid 合法性校验。
func StartDaemonActor(am *core.AManager) {
	if _, err := am.EnsureDaemon(RoomUID, RoomActorName); err != nil {
		logger.ERROR("start room actor failed:", err)
		return
	}
	logger.INFO("room actor started")
}
