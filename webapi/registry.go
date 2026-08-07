// webapi/registry.go
package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-redis/redis/v8"
)

const redisKeyPrefix = "player:"

// playerRegInfo Redis 中存储的玩家注册信息。
type playerRegInfo struct {
	GatewayAddr string `json:"gateway_addr"`
	GameAddr  string `json:"game_addr"`
}

// registerPlayer 将玩家信息写入 Redis。
func registerPlayer(rdb *redis.Client, playerName, gatewayAddr, gameAddr string, ttl time.Duration) error {
	info := playerRegInfo{
		GatewayAddr: gatewayAddr,
		GameAddr:  gameAddr,
	}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return rdb.Set(context.Background(), redisKeyPrefix+playerName, string(data), ttl).Err()
}

// getPlayerReg 从 Redis 读取玩家注册信息。
func getPlayerReg(rdb *redis.Client, playerName string) (*playerRegInfo, error) {
	val, err := rdb.Get(context.Background(), redisKeyPrefix+playerName).Result()
	if err != nil {
		return nil, err
	}
	info := &playerRegInfo{}
	if err := json.Unmarshal([]byte(val), info); err != nil {
		return nil, err
	}
	return info, nil
}
