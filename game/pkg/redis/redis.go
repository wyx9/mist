package pkg

import (
	"context"
	"github.com/go-redis/redis/v8"
)

//https://redis.uptrace.dev/guide/go-redis.html#installation

var RedisClient *redis.Client

var RCtx context.Context

func RedisStart(addr, password string, db int) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password, // no password set
		DB:       db,       // use default DB
	})

	RedisClient = rdb
	RCtx = context.Background()
	result, _ := RedisClient.Ping(RCtx).Result()
	if result == "PONG" {
		INFO("redis server start suc")
	} else {
		ERROR("redis server start fail")
	}
}
