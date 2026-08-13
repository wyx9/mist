package model

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"simple_game/game/pkg/logger"
	redispkg "simple_game/game/pkg/redis"
)

const (
	lobbyChatKey   = "chat:lobby"
	maxChatHistory = 100
)

var Chat = &ChatModel{}

type ChatModel struct{}

type ChatRecord struct {
	UID  int64  `json:"uid"`
	Name string `json:"name"`
	Msg  string `json:"msg"`
	Time int64  `json:"time"`
}

// SendMessage 写入一条大厅聊天记录到 Redis，并保底只保留最近 100 条。
func (m *ChatModel) SendMessage(uid int64, name, msg string) (*ChatRecord, error) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return nil, errors.New("chat message is empty")
	}
	if redispkg.RedisClient == nil {
		return nil, errors.New("redis client is not initialized")
	}

	record := &ChatRecord{
		UID:  uid,
		Name: name,
		Msg:  msg,
		Time: time.Now().UnixMilli(),
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	pipe := redispkg.RedisClient.TxPipeline()
	pipe.RPush(ctx, lobbyChatKey, raw)
	pipe.LTrim(ctx, lobbyChatKey, -maxChatHistory, -1)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	return record, nil
}

// LoadHistory 读取 Redis 中最近 100 条大厅聊天记录，按旧到新返回。
func (m *ChatModel) LoadHistory() ([]ChatRecord, error) {
	if redispkg.RedisClient == nil {
		return nil, errors.New("redis client is not initialized")
	}
	ctx := context.Background()
	values, err := redispkg.RedisClient.LRange(ctx, lobbyChatKey, 0, -1).Result()
	if err != nil {
		return nil, err
	}

	records := make([]ChatRecord, 0, len(values))
	for _, value := range values {
		var record ChatRecord
		if err := json.Unmarshal([]byte(value), &record); err != nil {
			logger.WARNING("skip invalid chat history record:", err)
			continue
		}
		records = append(records, record)
	}
	return records, nil
}
