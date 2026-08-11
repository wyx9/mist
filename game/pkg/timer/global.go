package timer

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"

	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/uid"
)

// Redis key 约定。
const (
	GlobalZSetKey      = "timer:global" // ZSET：score=DueAt(unix 秒)，member=任务 ID
	globalTaskPrefix   = "timer:task:"  // STRING：任务详情 JSON
	GlobalPollInterval = time.Second    // 全局定时器轮询周期
)

// GlobalTask 全局定时任务（Redis 持久化，进程重启后仍生效）。
type GlobalTask struct {
	ID      string // 任务唯一 id（雪花 ID，为空时自动分配）
	Target  string // 目标 actor 名
	DueAt   int64  // 到期 unix 秒
	Tag     string // 业务标签，供目标 actor 区分任务
	Payload []byte // 序列化数据（JSON），由业务按 Tag 解码
}

// GlobalFire 到期后投递给目标 actor 的包装消息。
type GlobalFire struct {
	ID      string // 任务 id
	Tag     string // 业务标签
	Payload []byte // 序列化数据
}

// Deliverer 到期投递回调：由接入方（gameserver）注入，负责把任务投递给目标 actor。
type Deliverer func(task *GlobalTask) error

// globalTaskRec Redis 中存储的任务详情。
type globalTaskRec struct {
	Target  string `json:"target"`
	DueAt   int64  `json:"due_at"`
	Tag     string `json:"tag"`
	Payload []byte `json:"payload"`
}

// ScheduleGlobal 写入一个全局定时任务；ID 为空时自动分配雪花 ID。
func ScheduleGlobal(ctx context.Context, rdb *redis.Client, task *GlobalTask) error {
	if task.ID == "" {
		task.ID = strconv.FormatInt(uid.NewUid(), 10)
	}
	rec := globalTaskRec{Target: task.Target, DueAt: task.DueAt, Tag: task.Tag, Payload: task.Payload}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	pipe := rdb.TxPipeline()
	pipe.Set(ctx, globalTaskPrefix+task.ID, raw, 0)
	pipe.ZAdd(ctx, GlobalZSetKey, &redis.Z{Score: float64(task.DueAt), Member: task.ID})
	_, err = pipe.Exec(ctx)
	return err
}

// CancelGlobal 取消一个全局定时任务。
func CancelGlobal(ctx context.Context, rdb *redis.Client, id string) error {
	pipe := rdb.TxPipeline()
	pipe.ZRem(ctx, GlobalZSetKey, id)
	pipe.Del(ctx, globalTaskPrefix+id)
	_, err := pipe.Exec(ctx)
	return err
}

// RunGlobal 全局定时器轮询协程：每秒从 Redis 取到期任务并投递。
// 常驻运行，ctx 取消时退出；Redis 故障记 ERROR 下轮重试；投递失败记 ERROR 并丢弃任务。
func RunGlobal(ctx context.Context, rdb *redis.Client, interval time.Duration, deliver Deliverer) {
	if interval <= 0 {
		interval = GlobalPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pollOnce(ctx, rdb, deliver)
		}
	}
}

func pollOnce(ctx context.Context, rdb *redis.Client, deliver Deliverer) {
	now := time.Now().Unix()
	ids, err := rdb.ZRangeByScore(ctx, GlobalZSetKey, &redis.ZRangeBy{
		Min: "-inf",
		Max: strconv.FormatInt(now, 10),
	}).Result()
	if err != nil {
		logger.ERROR("global timer poll failed:", err)
		return
	}
	for _, id := range ids {
		fireOne(ctx, rdb, id, deliver)
	}
}

// fireOne 认领并投递单个到期任务：ZRem 返回 0 说明已被认领，跳过。
func fireOne(ctx context.Context, rdb *redis.Client, id string, deliver Deliverer) {
	n, err := rdb.ZRem(ctx, GlobalZSetKey, id).Result()
	if err != nil {
		logger.ERROR("global timer claim failed:", err, "id:", id)
		return
	}
	if n == 0 {
		return
	}
	raw, err := rdb.Get(ctx, globalTaskPrefix+id).Bytes()
	if err != nil {
		logger.ERROR("global timer task missing:", err, "id:", id)
		return
	}
	var rec globalTaskRec
	if err := json.Unmarshal(raw, &rec); err != nil {
		logger.ERROR("global timer task decode failed:", err, "id:", id)
		return
	}
	task := &GlobalTask{ID: id, Target: rec.Target, DueAt: rec.DueAt, Tag: rec.Tag, Payload: rec.Payload}
	if err := deliver(task); err != nil {
		logger.ERROR("global timer deliver failed:", err, "id:", id)
	}
	if err := rdb.Del(ctx, globalTaskPrefix+id).Err(); err != nil {
		logger.ERROR("global timer task cleanup failed:", err, "id:", id)
	}
}
