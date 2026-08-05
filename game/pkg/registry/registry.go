package registry

import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"
)

// game 服注册表：game 启动后注册自身地址并定期心跳续期，agent/world 通过
// ListFresh 发现可用 game。索引 set 成员为 "ip:port"，每个地址配一个带 TTL 的心跳 key。
const (
	ServerSetKey      = "game:servers"
	alivePrefix       = "game:alive:"
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
