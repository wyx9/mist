package storage

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"
)

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func TestRedisWriterWritesInOrder(t *testing.T) {
	_, rdb := newTestRedis(t)
	w := NewRedisWriter(NewRedisStore(rdb), 4)
	w.Start()
	defer w.Stop(2 * time.Second)

	for i := 1; i <= 3; i++ {
		job := RedisJob{
			Key:      "actor_data:player:1",
			Category: "player",
			ActorID:  1,
			Raw:      []byte(fmt.Sprintf(`{"v":%d}`, i)),
		}
		if err := w.Submit(job); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}

	waitUntil(t, 2*time.Second, func() bool {
		raw, _ := rdb.Get(context.Background(), "actor_data:player:1").Result()
		return raw == `{"v":3}`
	})
	ok, _ := rdb.SIsMember(context.Background(), DirtySetKey("player"), "1").Result()
	if !ok {
		t.Fatal("dirty set missing uid after async save")
	}
}

func TestRedisWriterDeleteOrder(t *testing.T) {
	_, rdb := newTestRedis(t)
	w := NewRedisWriter(NewRedisStore(rdb), 4)
	w.Start()
	defer w.Stop(2 * time.Second)

	if err := w.Submit(RedisJob{Key: "actor_data:player:2", Category: "player", ActorID: 2, Raw: []byte(`{"v":1}`)}); err != nil {
		t.Fatalf("submit save: %v", err)
	}
	if err := w.Submit(RedisJob{Key: "actor_data:player:2", Category: "player", ActorID: 2, IsDelete: true}); err != nil {
		t.Fatalf("submit delete: %v", err)
	}

	// save 与 delete 可能在同一轮轮询间隙内完成，以「key 消失 且 脏集合含 uid」作为 delete 落地的完成信号
	waitUntil(t, 2*time.Second, func() bool {
		n, _ := rdb.Exists(context.Background(), "actor_data:player:2").Result()
		if n != 0 {
			return false
		}
		ok, _ := rdb.SIsMember(context.Background(), DirtySetKey("player"), "2").Result()
		return ok
	})
}

func TestRedisWriterStopDrains(t *testing.T) {
	mr, rdb := newTestRedis(t)
	w := NewRedisWriter(NewRedisStore(rdb), 4)
	w.Start()

	for i := 1; i <= 3; i++ {
		job := RedisJob{
			Key:      "actor_data:player:3",
			Category: "player",
			ActorID:  3,
			Raw:      []byte(fmt.Sprintf(`{"v":%d}`, i)),
		}
		if err := w.Submit(job); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}
	w.Stop(2 * time.Second)

	if !mr.Exists("actor_data:player:3") {
		t.Fatal("data not drained after stop")
	}
	if err := w.Submit(RedisJob{Key: "x", Category: "player", ActorID: 9}); !errors.Is(err, ErrWriterStopped) {
		t.Fatalf("expected ErrWriterStopped, got %v", err)
	}
}

// flakyStore 前 N 次写失败，之后成功。
type flakyStore struct {
	mu    sync.Mutex
	fails int
	store *RedisStore
}

func (f *flakyStore) SaveRaw(ctx context.Context, key string, raw []byte, category string, actorID int64) error {
	f.mu.Lock()
	if f.fails > 0 {
		f.fails--
		f.mu.Unlock()
		return errors.New("redis down")
	}
	f.mu.Unlock()
	return f.store.SaveRaw(ctx, key, raw, category, actorID)
}

func (f *flakyStore) Delete(ctx context.Context, key string, category string, actorID int64) error {
	return f.store.Delete(ctx, key, category, actorID)
}

func TestRedisWriterRetriesOnFailure(t *testing.T) {
	_, rdb := newTestRedis(t)
	store := &flakyStore{fails: 2, store: NewRedisStore(rdb)}
	w := NewRedisWriter(store, 4)
	w.retryBase = time.Millisecond
	w.Start()
	defer w.Stop(2 * time.Second)

	if err := w.Submit(RedisJob{Key: "actor_data:player:4", Category: "player", ActorID: 4, Raw: []byte(`{"v":1}`)}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		n, _ := rdb.Exists(context.Background(), "actor_data:player:4").Result()
		return n == 1
	})
}

// gatedStore 每次写阻塞到 release 放行，用于验证背压。
type gatedStore struct {
	calls   chan struct{}
	release chan struct{}
}

func (g *gatedStore) SaveRaw(ctx context.Context, key string, raw []byte, category string, actorID int64) error {
	g.calls <- struct{}{}
	<-g.release
	return nil
}

func (g *gatedStore) Delete(ctx context.Context, key string, category string, actorID int64) error {
	g.calls <- struct{}{}
	<-g.release
	return nil
}

func TestRedisWriterBackpressure(t *testing.T) {
	g := &gatedStore{calls: make(chan struct{}, 10), release: make(chan struct{})}
	w := NewRedisWriter(g, 1)
	w.Start()
	defer w.Stop(2 * time.Second)

	mk := func(n int) RedisJob {
		return RedisJob{Key: "k:" + strconv.Itoa(n), Category: "player", ActorID: int64(n), Raw: []byte("x")}
	}
	if err := w.Submit(mk(1)); err != nil {
		t.Fatalf("submit 1: %v", err)
	}
	<-g.calls // 写协程取走 job1，阻塞在 store
	if err := w.Submit(mk(2)); err != nil {
		t.Fatalf("submit 2: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- w.Submit(mk(3)) }()
	select {
	case err := <-done:
		t.Fatalf("submit 3 should block on full queue, got %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(g.release) // 放行 job1、job2
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("submit 3 failed after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("submit 3 still blocked after release")
	}
	// 第一个 call 已在提交后消费，这里还剩余 2 个（job2、job3）
	for i := 0; i < 2; i++ {
		select {
		case <-g.calls:
		case <-time.After(2 * time.Second):
			t.Fatalf("expected 2 more store calls, got %d", i)
		}
	}
}
