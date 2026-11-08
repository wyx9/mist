package storage

import (
	"context"
	"errors"
	"sync"
	"time"

	"simple_game/game/pkg/logger"
)

// ErrWriterStopped 写协程已停止，拒绝新任务。
var ErrWriterStopped = errors.New("redis writer stopped")

// RedisJob Redis 写入任务：保存快照字节或删除标记。
// 同一 channel FIFO 保证同一 actor 的快照顺序，后写入覆盖先写入。
type RedisJob struct {
	Key      string
	Category string
	ActorID  int64
	Raw      []byte // IsDelete=true 时忽略
	IsDelete bool
	NoSync   bool // true 表示只写/删 Redis，不登记 MySQL 脏集合
}

// redisWriterStore 字节级写入接口（RedisStore 实现，测试可注入 fake）。
type redisWriterStore interface {
	SaveRaw(ctx context.Context, key string, raw []byte, category string, actorID int64) error
	Delete(ctx context.Context, key string, category string, actorID int64) error
	Put(ctx context.Context, key string, raw []byte) error
	DeleteOnly(ctx context.Context, key string) error
}

// RedisWriter 异步 Redis 写协程：消费 FIFO 队列完成网络 I/O。
// 队列满时 Submit 阻塞（背压）；写失败按退避重试同一 job，不丢弃。
type RedisWriter struct {
	store     redisWriterStore
	jobs      chan RedisJob
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	started   bool
	retryBase time.Duration
	mu        sync.Mutex
}

func NewRedisWriter(store redisWriterStore, queueSize int) *RedisWriter {
	if queueSize <= 0 {
		queueSize = 4096
	}
	return &RedisWriter{
		store:     store,
		jobs:      make(chan RedisJob, queueSize),
		retryBase: 100 * time.Millisecond,
	}
}

// Start 启动写协程。
func (w *RedisWriter) Start() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started {
		return
	}
	w.ctx, w.cancel = context.WithCancel(context.Background())
	w.started = true
	w.wg.Add(1)
	go w.loop()
}

// Submit 入队写入任务；队列满时阻塞（背压），未启动或已停止时返回 ErrWriterStopped。
func (w *RedisWriter) Submit(job RedisJob) error {
	w.mu.Lock()
	started := w.started
	w.mu.Unlock()
	if !started {
		return ErrWriterStopped
	}
	for {
		select {
		case <-w.ctx.Done():
			return ErrWriterStopped
		case w.jobs <- job:
			return nil
		}
	}
}

// Stop 停止写协程：先取消接收新任务，再 drain 队列中剩余任务。
func (w *RedisWriter) Stop(timeout time.Duration) {
	w.mu.Lock()
	if !w.started {
		w.mu.Unlock()
		return
	}
	w.cancel()
	w.started = false
	w.mu.Unlock()

	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		logger.ERROR("redis writer stop timeout")
	}
}

func (w *RedisWriter) loop() {
	defer w.wg.Done()
	for {
		select {
		case <-w.ctx.Done():
			w.drain()
			return
		case job := <-w.jobs:
			w.process(job)
		}
	}
}

// drain 停止时把队列里剩余任务处理完。
func (w *RedisWriter) drain() {
	for {
		select {
		case job := <-w.jobs:
			w.process(job)
		default:
			return
		}
	}
}

func (w *RedisWriter) process(job RedisJob) {
	backoff := w.retryBase
	for {
		var err error
		switch {
		case job.IsDelete && job.NoSync:
			err = w.store.DeleteOnly(context.Background(), job.Key)
		case job.IsDelete:
			err = w.store.Delete(context.Background(), job.Key, job.Category, job.ActorID)
		case job.NoSync:
			err = w.store.Put(context.Background(), job.Key, job.Raw)
		default:
			err = w.store.SaveRaw(context.Background(), job.Key, job.Raw, job.Category, job.ActorID)
		}
		if err == nil {
			return
		}
		logger.ERROR("redis writer job failed:", err, "key:", job.Key)
		select {
		case <-w.ctx.Done():
			logger.ERROR("redis writer job dropped on shutdown:", job.Key)
			return
		case <-time.After(backoff):
		}
		if backoff < 32*w.retryBase {
			backoff *= 2
		}
	}
}
