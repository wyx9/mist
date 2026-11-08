package storage

import (
	"context"
	"errors"

	"github.com/go-redis/redis/v8"

	"simple_game/game/pkg/tool"
)

const dirtySetPrefix = "dirty:"

// DirtySetKey 返回某类别的脏集合 key，存放待同步到 MySQL 的 actor uid。
func DirtySetKey(category string) string {
	return dirtySetPrefix + category
}

// RedisStore Redis 热数据源：存序列化字节，并登记脏集合等待 DB 写线程落 MySQL。
type RedisStore struct {
	rdb *redis.Client
}

func NewRedisStore(rdb *redis.Client) *RedisStore {
	return &RedisStore{rdb: rdb}
}

// Load 从 Redis 读取并反序列化到 v；未命中返回 found=false。
func (s *RedisStore) Load(ctx context.Context, key string, codec Codec, v any) (bool, error) {
	raw, err := s.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := codec.Unmarshal(raw, v); err != nil {
		return false, err
	}
	return true, nil
}

// Save 序列化 v 后写入 Redis（同步路径，测试/直接调用用）。
func (s *RedisStore) Save(ctx context.Context, key string, codec Codec, v any, category string, actorID int64) error {
	raw, err := codec.Marshal(v)
	if err != nil {
		return err
	}
	return s.SaveRaw(ctx, key, raw, category, actorID)
}

// SaveRaw 写入序列化字节到 Redis，并把 actorID 登记进脏集合（等待 DB 同步线程落 MySQL）。
// 供异步写协程使用，不包含序列化。
func (s *RedisStore) SaveRaw(ctx context.Context, key string, raw []byte, category string, actorID int64) error {
	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, key, raw, 0)
	pipe.SAdd(ctx, DirtySetKey(category), tool.ToString(actorID))
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	return nil
}

// Delete 删除 Redis 数据并登记脏集合（DB 线程最终删除 MySQL 行）。
func (s *RedisStore) Delete(ctx context.Context, key string, category string, actorID int64) error {
	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, key)
	pipe.SAdd(ctx, DirtySetKey(category), tool.ToString(actorID))
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	return nil
}

// Put 直接回填原始字节（加载回源时使用），不登记脏集合。
func (s *RedisStore) Put(ctx context.Context, key string, raw []byte) error {
	return s.rdb.Set(ctx, key, raw, 0).Err()
}

// DeleteOnly 只删除 Redis key，不登记 MySQL 脏集合（Redis-only 策略）。
func (s *RedisStore) DeleteOnly(ctx context.Context, key string) error {
	return s.rdb.Del(ctx, key).Err()
}
