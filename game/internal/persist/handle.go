package persist

import (
	"context"
	"fmt"

	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/storage"
)

// handle 实现 actor.PersistHandle：Redis 热源 + MySQL 回源。
type handle struct {
	spec   CategorySpec
	codec  storage.Codec
	redis  *storage.RedisStore // 同步读（Load/Put）
	mysql  storage.MysqlStore
	writer *storage.RedisWriter // 异步写（Save/Delete）
}

func (h *handle) redisKey(actorID int64) string {
	return fmt.Sprintf("%s%d", h.spec.RedisKeyPrefix, actorID)
}

func (h *handle) Load(actorID int64, v any) (bool, error) {
	ctx := context.Background()
	found, err := h.redis.Load(ctx, h.redisKey(actorID), h.codec, v)
	if err != nil {
		return false, err
	}
	if found {
		return true, nil
	}
	if h.mysql == nil {
		return false, nil
	}
	raw, found, err := h.mysql.Load(ctx, h.spec.Table, actorID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	if err := h.codec.Unmarshal(raw, v); err != nil {
		return false, err
	}
	// 回填 Redis 热层；失败不影响本次加载
	if err := h.redis.Put(ctx, h.redisKey(actorID), raw); err != nil {
		logger.ERROR("persist: redis backfill failed:", err, "category:", h.spec.Category, "actor:", actorID)
	}
	return true, nil
}

// Save 在调用方（actor 循环内）序列化快照后入队异步写入 Redis；
// 队列满时阻塞（背压），已停止时返回 ErrWriterStopped。
func (h *handle) Save(actorID int64, v any) error {
	raw, err := h.codec.Marshal(v)
	if err != nil {
		return err
	}
	return h.writer.Submit(storage.RedisJob{
		Key:      h.redisKey(actorID),
		Category: h.spec.Category,
		ActorID:  actorID,
		Raw:      raw,
	})
}

func (h *handle) Delete(actorID int64) error {
	return h.writer.Submit(storage.RedisJob{
		Key:      h.redisKey(actorID),
		Category: h.spec.Category,
		ActorID:  actorID,
		IsDelete: true,
	})
}
