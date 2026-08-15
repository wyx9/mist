package gameserver

import (
	"context"

	"simple_game/game/pkg/redis"
	"simple_game/game/pkg/registry"
)

// actorLocRegistry 实现 core.LocationRegistry：把可路由 actor 的位置写入/删除 Redis。
type actorLocRegistry struct {
	srv *Server
}

func (r *actorLocRegistry) Register(name string) error {
	return registry.RegisterActorLoc(context.Background(), redis.RedisClient, name, r.srv.regAddr)
}

func (r *actorLocRegistry) Unregister(name string) error {
	return registry.UnregisterActorLoc(context.Background(), redis.RedisClient, name)
}
