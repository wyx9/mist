package gameserver

import (
	"context"

	"simple_game/game/pkg/redis"
	"simple_game/game/pkg/registry"
)

// actorLocRegistry 实现 core.LocationRegistry：把可路由 actor 的位置写入/删除 Redis（按 uid）。
type actorLocRegistry struct {
	srv *Server
}

func (r *actorLocRegistry) Register(uid int64) error {
	return registry.RegisterActorLoc(context.Background(), redis.RedisClient, uid, r.srv.regAddr)
}

func (r *actorLocRegistry) Unregister(uid int64) error {
	return registry.UnregisterActorLoc(context.Background(), redis.RedisClient, uid)
}

// actorInitRegistry 实现 core.InitRegistry：actor 初始化记录（uid→category）持久化到 Redis。
// 进程重启/跨节点后仍可据此按需复活 actor。
type actorInitRegistry struct {
	srv *Server
}

func (r *actorInitRegistry) Register(uid int64, category string) error {
	return registry.RegisterActorInit(context.Background(), redis.RedisClient, uid, category)
}

func (r *actorInitRegistry) Unregister(uid int64) error {
	return registry.UnregisterActorInit(context.Background(), redis.RedisClient, uid)
}

func (r *actorInitRegistry) Category(uid int64) (string, bool, error) {
	return registry.ActorCategory(context.Background(), redis.RedisClient, uid)
}
