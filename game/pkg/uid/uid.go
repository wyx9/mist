package uid

import (
	"context"
	"sync"

	"github.com/go-redis/redis/v8"

	"simple_game/game/pkg/logger"
)

// 全局自增 uid 的 Redis key。
const seqKey = "uid:global"

var (
	mu  sync.RWMutex
	rdb *redis.Client
)

// Init 注入 Redis 客户端（进程启动时调用一次，须早于任何 NewUid 调用）。
// game 在 Redis 初始化后、webapi 在创建自己的 rdb 后各调用一次。
func Init(client *redis.Client) {
	mu.Lock()
	rdb = client
	mu.Unlock()
}

// NewUid 返回一个全局自增 uid（跨进程唯一、连续，从 1 开始）。
// 内部实现：Redis INCR。未初始化或 Redis 不可用时返回 0 并记录错误。
func NewUid() int64 {
	mu.RLock()
	c := rdb
	mu.RUnlock()
	if c == nil {
		logger.ERROR("uid: redis client not initialized")
		return 0
	}
	seq, err := c.Incr(context.Background(), seqKey).Result()
	if err != nil {
		logger.ERROR("uid: redis incr failed:", err)
		return 0
	}
	return seq
}

// SetMachineId 兼容保留：uid 由 Redis 全局计数生成，机器 id 不再参与计算，此函数为空操作。
func SetMachineId(mid int64) {}
