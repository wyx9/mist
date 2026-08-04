package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// fakeStore 记录 MySQL 操作，可注入错误。
type fakeStore struct {
	mu        sync.Mutex
	upserts   []rowOp
	deletes   []rowOp
	upsertErr error
	loadData  []byte
}

type rowOp struct {
	actorID int64
	table   string
	data    []byte
}

func (f *fakeStore) EnsureTable(ctx context.Context, table string) error { return nil }

func (f *fakeStore) Load(ctx context.Context, table string, actorID int64) ([]byte, bool, error) {
	return f.loadData, true, nil
}

func (f *fakeStore) Upsert(ctx context.Context, table string, actorID int64, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upserts = append(f.upserts, rowOp{actorID: actorID, table: table, data: append([]byte(nil), data...)})
	return nil
}

func (f *fakeStore) Delete(ctx context.Context, table string, actorID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, rowOp{actorID: actorID, table: table})
	return nil
}

func (f *fakeStore) Close() error { return nil }

func (f *fakeStore) upsertCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.upserts)
}

func newTestSync(t *testing.T, batch int, openStore func(dbName string) (MysqlStore, error)) (*MysqlSync, *miniredis.Miniredis) {
	t.Helper()
	mr, rdb := newTestRedis(t)
	s := NewMysqlSync(MysqlSyncConfig{
		Redis:     rdb,
		Interval:  30 * time.Second,
		BatchSize: batch,
		Categories: map[string]CategoryTable{
			"player": {Category: "player", Table: "player_data", DBName: "simple_game", RedisKeyPrefix: "actor_data:player:"},
		},
		OpenStore: openStore,
	})
	return s, mr
}

func TestMysqlSyncDrainUpsertAndSrem(t *testing.T) {
	st := &fakeStore{}
	s, mr := newTestSync(t, 2, func(dbName string) (MysqlStore, error) { return st, nil })
	ctx := context.Background()
	mr.Set("actor_data:player:1", `{"Name":"p1"}`)
	mr.SAdd(DirtySetKey("player"), "1")

	if err := s.drainCategory(ctx, s.categories["player"]); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if st.upsertCount() != 1 {
		t.Fatalf("expected 1 upsert, got %d", st.upsertCount())
	}
	if st.upserts[0].actorID != 1 || string(st.upserts[0].data) != `{"Name":"p1"}` {
		t.Fatalf("unexpected upsert: %+v", st.upserts[0])
	}
	ok, _ := mr.SIsMember(DirtySetKey("player"), "1")
	if ok {
		t.Fatal("dirty uid should be removed after sync")
	}
}

func TestMysqlSyncDrainDeleteWhenRedisMissing(t *testing.T) {
	st := &fakeStore{}
	s, mr := newTestSync(t, 10, func(dbName string) (MysqlStore, error) { return st, nil })
	ctx := context.Background()
	// Redis 无 key（数据被删除），脏集合仍登记
	mr.SAdd(DirtySetKey("player"), "5")

	if err := s.drainCategory(ctx, s.categories["player"]); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(st.deletes) != 1 || st.deletes[0].actorID != 5 {
		t.Fatalf("expected delete of actor 5, got %+v", st.deletes)
	}
	ok, _ := mr.SIsMember(DirtySetKey("player"), "5")
	if ok {
		t.Fatal("dirty uid should be removed after delete sync")
	}
}

func TestMysqlSyncDrainKeepsDirtyOnError(t *testing.T) {
	st := &fakeStore{upsertErr: errors.New("db down")}
	s, mr := newTestSync(t, 10, func(dbName string) (MysqlStore, error) { return st, nil })
	ctx := context.Background()
	mr.Set("actor_data:player:1", `{"Name":"p1"}`)
	mr.SAdd(DirtySetKey("player"), "1")

	if err := s.drainCategory(ctx, s.categories["player"]); err == nil {
		t.Fatal("expected drain error")
	}
	ok, _ := mr.SIsMember(DirtySetKey("player"), "1")
	if !ok {
		t.Fatal("dirty uid must be retained on failure")
	}
}

func TestMysqlSyncBatchChunking(t *testing.T) {
	st := &fakeStore{}
	s, mr := newTestSync(t, 2, func(dbName string) (MysqlStore, error) { return st, nil })
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		mr.Set(fmt.Sprintf("actor_data:player:%d", i), fmt.Sprintf(`{"Num":%d}`, i))
		mr.SAdd(DirtySetKey("player"), fmt.Sprintf("%d", i))
	}

	if err := s.drainCategory(ctx, s.categories["player"]); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if st.upsertCount() != 5 {
		t.Fatalf("expected 5 upserts, got %d", st.upsertCount())
	}
	n, _ := mr.SCard(DirtySetKey("player"))
	if n != 0 {
		t.Fatal("dirty set should be empty after drain")
	}
}

func TestMysqlSyncWriterForCaches(t *testing.T) {
	calls := 0
	s, _ := newTestSync(t, 10, func(dbName string) (MysqlStore, error) {
		calls++
		return &fakeStore{}, nil
	})
	if _, err := s.WriterFor("simple_game"); err != nil {
		t.Fatalf("writer: %v", err)
	}
	if _, err := s.WriterFor("simple_game"); err != nil {
		t.Fatalf("writer: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 open call, got %d", calls)
	}
}

func TestMysqlSyncStartStopFinalDrain(t *testing.T) {
	st := &fakeStore{}
	openCalls := 0
	s, mr := newTestSync(t, 10, func(dbName string) (MysqlStore, error) {
		openCalls++
		return st, nil
	})
	mr.Set("actor_data:player:7", `{"Name":"p7"}`)
	mr.SAdd(DirtySetKey("player"), "7")

	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	s.Stop(2 * time.Second)
	if st.upsertCount() != 1 {
		t.Fatalf("expected final drain upsert, got %d", st.upsertCount())
	}
	if openCalls != 1 {
		t.Fatalf("expected 1 open call, got %d", openCalls)
	}
}

var _ MysqlStore = (*fakeStore)(nil)
