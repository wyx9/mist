package actor

import (
	"simple_game/game/internal/actor/core"
	"simple_game/game/pkg/logger"
)

const (
	PlayerActorName = "player"
	RoomActorName   = "room"
)

// RegisterAll 统一注册业务 Actor 类别（Player），Server Phase 3 调用。
func RegisterAll(am *core.AManager) {
	am.RegisterCategory(&Player{})
	am.RegisterCategory(&Room{})
	logger.INFO("actor RegisterAll complete")
}

// StartDaemonActor 启动常驻的actor
func StartDaemonActor(am *core.AManager) {
	_, _ = core.StartActor(am, RoomActorName, &Room{})
	logger.INFO("room actor started")
}
