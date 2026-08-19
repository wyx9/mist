package registry

import (
	"context"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
)

// game 服注册表：game 启动后注册自身地址并定期心跳续期，gateway/webapi 通过
// ListFresh 发现可用 game。索引 set 成员为 "ip:port"，每个地址配一个带 TTL 的心跳 key。
const (
	ServerSetKey      = "game:servers"
	alivePrefix       = "game:alive:"
	actorLocPrefix    = "actor:loc:"
	actorInitPrefix   = "actor:init:"
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

// ActorLocKey 返回 actor 位置表 key：actor:loc:{uid} -> 节点 addr。
func ActorLocKey(uid int64) string {
	return actorLocPrefix + strconv.FormatInt(uid, 10)
}

// SetActorLoc 覆盖写入某个 actor 的位置：actor:loc:{uid} = addr。
// 强制接管语义（握手/跨节点迁移时用）；普通创建请用 ClaimActorLoc 原子认领。
func SetActorLoc(ctx context.Context, rdb *redis.Client, uid int64, addr string) error {
	return rdb.Set(ctx, ActorLocKey(uid), addr, 0).Err()
}

// claimActorLocScript 原子认领：位置不存在或已属于 addr 时写入并返回 1，否则返回 0。
var claimActorLocScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] or redis.call('EXISTS', KEYS[1]) == 0 then
	redis.call('SET', KEYS[1], ARGV[1])
	return 1
else
	return 0
end
`)

// ClaimActorLoc 原子认领某个 actor 的位置（避免双节点同时创建同一 uid 的双写）。
// 返回 claimed=true 表示认领成功（原本无记录或本就属于 addr）；false 表示已被其他节点占有。
func ClaimActorLoc(ctx context.Context, rdb *redis.Client, uid int64, addr string) (bool, error) {
	n, err := claimActorLocScript.Run(ctx, rdb, []string{ActorLocKey(uid)}, addr).Int64()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// UnregisterActorLoc 删除某个 actor 的位置记录。由 game 在彻底删除可路由 actor 时调用。
func UnregisterActorLoc(ctx context.Context, rdb *redis.Client, uid int64) error {
	return rdb.Del(ctx, ActorLocKey(uid)).Err()
}

// ResolveActorLoc 解析 actor 所在节点；未注册返回 found=false。
func ResolveActorLoc(ctx context.Context, rdb *redis.Client, uid int64) (addr string, found bool, err error) {
	val, err := rdb.Get(ctx, ActorLocKey(uid)).Result()
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

// ActorInitKey 返回 actor 初始化记录 key：actor:init:{uid} -> category。
func ActorInitKey(uid int64) string {
	return actorInitPrefix + strconv.FormatInt(uid, 10)
}

// RegisterActorInit 记录某 uid 已初始化及其类别（actor 首次创建时写入，长期保留，供按需复活）。
func RegisterActorInit(ctx context.Context, rdb *redis.Client, uid int64, category string) error {
	return rdb.Set(ctx, ActorInitKey(uid), category, 0).Err()
}

// UnregisterActorInit 删除初始化记录（彻底删除 actor 时用）。
func UnregisterActorInit(ctx context.Context, rdb *redis.Client, uid int64) error {
	return rdb.Del(ctx, ActorInitKey(uid)).Err()
}

// ActorCategory 查询某 uid 的类别；未初始化返回 found=false。
func ActorCategory(ctx context.Context, rdb *redis.Client, uid int64) (category string, found bool, err error) {
	val, err := rdb.Get(ctx, ActorInitKey(uid)).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}
