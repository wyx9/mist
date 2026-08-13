package iface

import "simple_game/game/internal/model"

type ChatModelIFace interface {
	SendMessage(uid int64, name, msg string) (*model.ChatRecord, error)
}
