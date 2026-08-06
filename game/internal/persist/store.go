package persist

import (
	"context"
	"fmt"

	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/storage"
)

// store 实现 core.PersistStore：Redis 热源 + MySQL 回源，写路径异步入队。
type store struct {
	spec   CategorySpec
	codec  storage.Codec
	redis  *storage.RedisStore
	mysql  storage.MysqlStore
	writer *storage.RedisWriter
}

func (s *store) redisKey(actorID int64) string {
	return fmt.Sprintf("%s%d", s.spec.RedisKeyPrefix, actorID)
}

func (s *store) Load(actorID int64, v any) (bool, error) {
	ctx := context.Background()
	found, err := s.redis.Load(ctx, s.redisKey(actorID), s.codec, v)
	if err != nil {
		return false, err
	}
	if found {
		return true, nil
	}
	if s.mysql == nil {
		return false, nil
	}
	raw, found, err := s.mysql.Load(ctx, s.spec.Table, actorID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	if err := s.codec.Unmarshal(raw, v); err != nil {
		return false, err
	}
	// 回填 Redis 热层；失败不影响本次加载
	if err := s.redis.Put(ctx, s.redisKey(actorID), raw); err != nil {
		logger.ERROR("persist: redis backfill failed:", err, "category:", s.spec.Category, "actor:", actorID)
	}
	return true, nil
}

// Save 在调用方（actor 循环内）序列化快照后入队异步写入 Redis；
// 队列满时阻塞（背压），已停止时返回 ErrWriterStopped。
func (s *store) Save(actorID int64, v any) error {
	raw, err := s.codec.Marshal(v)
	if err != nil {
		return err
	}
	return s.writer.Submit(storage.RedisJob{
		Key:      s.redisKey(actorID),
		Category: s.spec.Category,
		ActorID:  actorID,
		Raw:      raw,
	})
}

func (s *store) Delete(actorID int64) error {
	return s.writer.Submit(storage.RedisJob{
		Key:      s.redisKey(actorID),
		Category: s.spec.Category,
		ActorID:  actorID,
		IsDelete: true,
	})
}
