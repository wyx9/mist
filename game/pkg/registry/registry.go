package registry

import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"
)

// game 服注册表：game 启动后注册自身地址并定期心跳续期，gateway/webapi 通过
// ListFresh 发现可用 game。索引 set 成员为 "ip:port"，每个地址配一个带 TTL 的心跳 key。
const (
	ServerSetKey      = "game:servers"
	alivePrefix       = "game:alive:"
	actorLocPrefix    = "actor:loc:"
	AliveTTL          = 10 * time.Second
	HeartbeatInterval = 5 * time.Second
)

func aliveKey(addr string) string {
	return alivePrefix + addr
}

// Register 注册（或续期）一个 game 地址。
func Register(ctx context.Context, rdb *redis.Client, addr string) error {
	pipe := rdb.TxPipeline()
	pipe.SAdd(ctx, ServerSetKey, addr)
	pipe.Set(ctx, aliveKey(addr), "1", AliveTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// Unregister 注销一个 game 地址（优雅关闭时调用）。
func Unregister(ctx context.Context, rdb *redis.Client, addr string) error {
	pipe := rdb.TxPipeline()
	pipe.SRem(ctx, ServerSetKey, addr)
	pipe.Del(ctx, aliveKey(addr))
	_, err := pipe.Exec(ctx)
	return err
}

// ListFresh 返回当前心跳仍新鲜的 game 地址列表。
func ListFresh(ctx context.Context, rdb *redis.Client) ([]string, error) {
	servers, err := rdb.SMembers(ctx, ServerSetKey).Result()
	if err != nil {
		return nil, err
	}
	fresh := make([]string, 0, len(servers))
	for _, addr := range servers {
		n, err := rdb.Exists(ctx, aliveKey(addr)).Result()
		if err != nil {
			return nil, err
		}
		if n > 0 {
			fresh = append(fresh, addr)
		}
	}
	return fresh, nil
}

// ActorLocKey 返回 actor 位置表 key：actor:loc:{actorName} -> 节点 addr。
func ActorLocKey(name string) string {
	return actorLocPrefix + name
}

// RegisterActorLoc 写入（或覆盖）某个 actor 的位置：actor:loc:{name} = addr。
// 由 game 在创建可路由 actor 时调用。
func RegisterActorLoc(ctx context.Context, rdb *redis.Client, name, addr string) error {
	return rdb.Set(ctx, ActorLocKey(name), addr, 0).Err()
}

// UnregisterActorLoc 删除某个 actor 的位置记录。由 game 在关闭可路由 actor 时调用。
func UnregisterActorLoc(ctx context.Context, rdb *redis.Client, name string) error {
	return rdb.Del(ctx, ActorLocKey(name)).Err()
}

// ResolveActorLoc 解析 actor 所在节点；未注册返回 found=false。
func ResolveActorLoc(ctx context.Context, rdb *redis.Client, name string) (addr string, found bool, err error) {
	val, err := rdb.Get(ctx, ActorLocKey(name)).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}

// SweepActorLoc 清理指向指定节点的所有 actor 位置记录（节点失联时由 gateway 调用，best-effort）。
func SweepActorLoc(ctx context.Context, rdb *redis.Client, addr string) {
	var cursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, cursor, actorLocPrefix+"*", 100).Result()
		if err != nil {
			return
		}
		for _, key := range keys {
			v, err := rdb.Get(ctx, key).Result()
			if err == nil && v == addr {
				_ = rdb.Del(ctx, key).Err()
			}
		}
		cursor = next
		if cursor == 0 {
			return
		}
	}
}
