package core

import (
	"context"
	"fmt"
	"simple_game/game/pkg/redis"
	"simple_game/game/pkg/registry"
)

// RegisterAddr 原子认领位置：已被其他节点占有时返回 core.ErrActorNotOwner（不覆盖）。
func (am *AManager) RegisterAddr(uid int64) error {
	if redis.RedisClient == nil {
		return fmt.Errorf("redis client not initialized")
	}
	claimed, err := registry.ClaimActorLoc(context.Background(), redis.RedisClient, uid, am.addrSnapshot())
	if err != nil {
		return err
	}
	if !claimed {
		return ErrActorNotOwner
	}
	return nil
}

func (am *AManager) UnregisterAddr(uid int64) error {
	if redis.RedisClient == nil {
		return nil
	}
	return registry.UnregisterActorLoc(context.Background(), redis.RedisClient, uid)
}

// ClaimActorLoc 强制接管 uid 的位置（覆盖写），供握手路径在新建/顶替时使用。
func (am *AManager) ClaimActorLoc(uid int64) error {
	if redis.RedisClient == nil {
		return fmt.Errorf("redis client not initialized")
	}
	return registry.SetActorLoc(context.Background(), redis.RedisClient, uid, am.addrSnapshot())
}

// IsOwner 判断 uid 位置是否归属本节点；无记录视为可认领（返回 true）。
func (am *AManager) IsOwner(uid int64) (bool, error) {
	if redis.RedisClient == nil {
		return false, fmt.Errorf("redis client not initialized")
	}
	addr, found, err := registry.ResolveActorLoc(context.Background(), redis.RedisClient, uid)
	if err != nil {
		return false, err
	}
	if !found {
		return true, nil
	}
	return addr == am.addrSnapshot(), nil
}

func (am *AManager) RegisterInit(uid int64, category string) error {
	if redis.RedisClient == nil {
		return nil
	}
	return registry.RegisterActorInit(context.Background(), redis.RedisClient, uid, category)
}

func (am *AManager) UnregisterInit(uid int64) error {
	if redis.RedisClient == nil {
		return nil
	}
	return registry.UnregisterActorInit(context.Background(), redis.RedisClient, uid)
}

// GetActorCategory 查询某 uid 的类别；未初始化返回 found=false。
func (am *AManager) GetActorCategory(uid int64) (category string, found bool, err error) {
	if redis.RedisClient == nil {
		return "", false, nil
	}
	return registry.ActorCategory(context.Background(), redis.RedisClient, uid)
}
