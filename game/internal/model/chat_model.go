package model

import (
	"simple_game/game/pkg/logger"
)

var Chat = &ChatModel{}

type ChatModel struct{}

// SendMessage 发送聊天消息。
func (m *ChatModel) SendMessage(uid int64, name, msg string) error {
	logger.DEBUG("[ChatModel] SendMessage:", "uid:", uid, "name:", name, "msg:", msg)

	return nil
}
