package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-redis/redis/v8"
	"gopkg.in/yaml.v3"
)

// Redis key 与 pub/sub 通道约定，须与 game/configs 包内 KeyPrefix/ChangedCh 保持一致。
const (
	redisKeyPrefix = "config:"
	redisChangedCh = "config:changed"
)

// pushCfg 只解析推送所需的 Redis 配置段。
type pushCfg struct {
	Redis struct {
		Addr     string `yaml:"Addr"`
		PassWord string `yaml:"PassWord"`
		DB       int    `yaml:"DB"`
	} `yaml:"Redis"`
}

// pushToRedis 把生成的 JSON 数据写入 Redis，并逐表发布热更通知。
func pushToRedis(root string, tables []*Table) error {
	data, err := os.ReadFile(filepath.Join(root, "config", "config.yaml"))
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var cfg pushCfg
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.PassWord,
		DB:       cfg.Redis.DB,
	})
	defer rdb.Close()

	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}
	for _, t := range tables {
		raw, err := os.ReadFile(filepath.Join(root, "configs", "data", t.Name+".json"))
		if err != nil {
			return fmt.Errorf("read data %s: %w", t.Name, err)
		}
		key := redisKeyPrefix + t.Name
		if err := rdb.Set(ctx, key, raw, 0).Err(); err != nil {
			return fmt.Errorf("set %s: %w", key, err)
		}
		if err := rdb.Publish(ctx, redisChangedCh, t.Name).Err(); err != nil {
			return fmt.Errorf("publish %s: %w", t.Name, err)
		}
		fmt.Printf("push config: %s (%d bytes)\n", t.Name, len(raw))
	}
	return nil
}
