package gameserver

import (
	"context"

	core "simple_game/game/internal/actor/core"
	"simple_game/game/pkg/redis"
	"simple_game/game/pkg/registry"
)

// actorLocRegistry 实现 core.LocationRegistry：把可路由 actor 的位置写入/删除 Redis（按 uid）。
// 位置表是「已实例化 actor 及其节点」的权威登记，owner 边界由此强制执行：
// Register 原子认领，IsOwner 查询归属。（强制接管不在此接口内，握手路径直接用 SetActorLoc。）
type actorLocRegistry struct {
	srv *Server
}

// Register 原子认领位置：已被其他节点占有时返回 core.ErrActorNotOwner（不覆盖）。
func (r *actorLocRegistry) Register(uid int64) error {
	claimed, err := registry.ClaimActorLoc(context.Background(), redis.RedisClient, uid, r.srv.regAddr)
	if err != nil {
		return err
	}
	if !claimed {
		return core.ErrActorNotOwner
	}
	return nil
}

func (r *actorLocRegistry) Unregister(uid int64) error {
	return registry.UnregisterActorLoc(context.Background(), redis.RedisClient, uid)
}

// IsOwner 位置是否归属本节点；无记录视为可认领（返回 true）。
func (r *actorLocRegistry) IsOwner(uid int64) (bool, error) {
	node, found, err := registry.ResolveActorLoc(context.Background(), redis.RedisClient, uid)
	if err != nil {
		return false, err
	}
	if !found {
		return true, nil
	}
	return node == r.srv.regAddr, nil
}

// claimActorLoc 强制接管 uid 的位置（覆盖写），供握手路径在新建/顶替时使用。
func (s *Server) claimActorLoc(uid int64) error {
	return registry.SetActorLoc(context.Background(), redis.RedisClient, uid, s.regAddr)
}

// isLocalOwner 判断 uid 的位置是否归属本节点；无记录视为可认领（返回 true）。
// 复用 actorLocRegistry.IsOwner 同一实现；查询失败时保守视为本节点（不做破坏性操作），
// 由后续创建路径的原子认领兜底。
func (s *Server) isLocalOwner(uid int64) bool {
	owner, err := (&actorLocRegistry{srv: s}).IsOwner(uid)
	if err != nil {
		return true
	}
	return owner
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
