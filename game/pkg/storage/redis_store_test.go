package storage

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
)

type codecSample struct {
	Name string
	Num  int64
}

func newTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func TestRedisStoreSaveLoadDelete(t *testing.T) {
	mr, rdb := newTestRedis(t)
	s := NewRedisStore(rdb)
	ctx := context.Background()

	if err := s.Save(ctx, "actor_data:player:1", JSONCodec{}, &codecSample{Name: "p1"}, "player", 1); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !mr.Exists("actor_data:player:1") {
		t.Fatal("key not set after save")
	}
	ok, _ := mr.SIsMember(DirtySetKey("player"), "1")
	if !ok {
		t.Fatal("dirty set missing uid after save")
	}

	var out codecSample
	found, err := s.Load(ctx, "actor_data:player:1", JSONCodec{}, &out)
	if err != nil || !found {
		t.Fatalf("load: found=%v err=%v", found, err)
	}
	if out.Name != "p1" {
		t.Fatalf("unexpected loaded: %+v", out)
	}

	// 未命中
	found, err = s.Load(ctx, "actor_data:player:999", JSONCodec{}, &out)
	if err != nil || found {
		t.Fatalf("load miss: found=%v err=%v", found, err)
	}

	// Delete：Redis key 删除 + 脏集合保留待 DB 删除
	if err := s.Delete(ctx, "actor_data:player:1", "player", 1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if mr.Exists("actor_data:player:1") {
		t.Fatal("key still exists after delete")
	}
	ok, _ = mr.SIsMember(DirtySetKey("player"), "1")
	if !ok {
		t.Fatal("dirty set missing uid after delete")
	}
}

func TestRedisStorePutBackfill(t *testing.T) {
	_, rdb := newTestRedis(t)
	s := NewRedisStore(rdb)
	ctx := context.Background()
	if err := s.Put(ctx, "actor_data:player:2", []byte(`{"Name":"p2"}`)); err != nil {
		t.Fatalf("put: %v", err)
	}
	var out codecSample
	found, err := s.Load(ctx, "actor_data:player:2", JSONCodec{}, &out)
	if err != nil || !found || out.Name != "p2" {
		t.Fatalf("load after put: found=%v err=%v out=%+v", found, err, out)
	}
}
