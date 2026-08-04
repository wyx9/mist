package persist

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"

	"simple_game/game/internal/actor"
	"simple_game/game/pkg/storage"
)

func newTestManager(t *testing.T, am *actor.AManager, withMySQL bool) (*Manager, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	opts := Options{
		AM:  am,
		RDB: rdb,
		Specs: map[string]CategorySpec{
			"player": {
				Category:       "player",
				RedisKeyPrefix: "actor_data:player:",
				Table:          "player_data",
				DBName:         "simple_game",
				Codec:          "json",
				FlushInterval:  20 * time.Millisecond,
			},
		},
		DefaultDBName: "simple_game",
	}
	if withMySQL {
		opts.MySQLCfg = &storage.MysqlSyncConfig{
			Redis:     rdb,
			Interval:  30 * time.Second,
			BatchSize: 10,
			OpenStore: func(dbName string) (storage.MysqlStore, error) {
				return &fakeMysqlStore{data: []byte(`{"Name":"from-mysql"}`)}, nil
			},
		}
	}
	m, err := NewManager(opts)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	return m, mr
}

// fakeMysqlStore 返回固定数据，验证 Redis miss 时从 MySQL 回源。
type fakeMysqlStore struct {
	data []byte
}

func (f *fakeMysqlStore) EnsureTable(ctx context.Context, table string) error { return nil }
func (f *fakeMysqlStore) Load(ctx context.Context, table string, actorID int64) ([]byte, bool, error) {
	if f.data == nil {
		return nil, false, nil
	}
	return f.data, true, nil
}
func (f *fakeMysqlStore) Upsert(ctx context.Context, table string, actorID int64, data []byte) error {
	return nil
}
func (f *fakeMysqlStore) Delete(ctx context.Context, table string, actorID int64) error { return nil }
func (f *fakeMysqlStore) Close() error                                                  { return nil }

var _ storage.MysqlStore = (*fakeMysqlStore)(nil)

// tickActor 记录收到的 PersistTick。
type tickActor struct {
	ticks chan struct{}
}

func (a *tickActor) Start()           {}
func (a *tickActor) Stop()            {}
func (a *tickActor) Category() string { return "player" }
func (a *tickActor) ActorID() int64   { return 1001 }
func (a *tickActor) PersistData() any { return nil }
func (a *tickActor) HandleMessage(ctx *actor.ActorContext, msg interface{}) {
	if _, ok := msg.(actor.PersistTick); ok {
		a.ticks <- struct{}{}
	}
}

func TestManagerTickerDeliversTick(t *testing.T) {
	am := &actor.AManager{}
	am.RegisterCategory(&tickActor{})
	a := &tickActor{ticks: make(chan struct{}, 16)}
	if _, err := actor.StartActor(am, "p1", a, nil); err != nil {
		t.Fatalf("start actor: %v", err)
	}

	m, _ := newTestManager(t, am, false)
	if err := m.Start(); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer m.Stop(2 * time.Second)

	select {
	case <-a.ticks:
	case <-time.After(2 * time.Second):
		t.Fatal("no persist tick received")
	}
}

func TestManagerHandleForSaveLoad(t *testing.T) {
	am := &actor.AManager{}
	am.RegisterCategory(&tickActor{})
	m, mr := newTestManager(t, am, false)
	if err := m.Start(); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer m.Stop(2 * time.Second)

	h := m.HandleFor("player")
	if h == nil {
		t.Fatal("expected handle for player")
	}

	type data struct {
		Name string
	}
	if err := h.Save(1001, &data{Name: "p1"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	waitPersist(t, func() bool { return mr.Exists("actor_data:player:1001") })
	ok, _ := mr.SIsMember("dirty:player", "1001")
	if !ok {
		t.Fatal("dirty set not marked")
	}

	var out data
	found, err := h.Load(1001, &out)
	if err != nil || !found {
		t.Fatalf("load: found=%v err=%v", found, err)
	}
	if out.Name != "p1" {
		t.Fatalf("unexpected loaded: %+v", out)
	}

	found, err = h.Load(999, &out)
	if err != nil || found {
		t.Fatalf("load miss: found=%v err=%v", found, err)
	}

	if err := h.Delete(1001); err != nil {
		t.Fatalf("delete: %v", err)
	}
	waitPersist(t, func() bool { return !mr.Exists("actor_data:player:1001") })
	ok, _ = mr.SIsMember("dirty:player", "1001")
	if !ok {
		t.Fatal("dirty set missing delete marker")
	}
}

func TestManagerStopDrainsAsyncWrites(t *testing.T) {
	am := &actor.AManager{}
	m, mr := newTestManager(t, am, false)
	if err := m.Start(); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	h := m.HandleFor("player")
	if h == nil {
		t.Fatal("expected handle")
	}
	type data struct {
		Name string
	}
	if err := h.Save(1001, &data{Name: "p1"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	m.Stop(2 * time.Second)
	if !mr.Exists("actor_data:player:1001") {
		t.Fatal("async write not drained before manager stop")
	}
}

func waitPersist(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func TestHandleLoadFallsBackToMySQL(t *testing.T) {
	am := &actor.AManager{}
	m, mr := newTestManager(t, am, true)
	h := m.HandleFor("player")
	if h == nil {
		t.Fatal("expected handle")
	}

	type data struct {
		Name string
	}
	var out data
	found, err := h.Load(555, &out)
	if err != nil || !found {
		t.Fatalf("load: found=%v err=%v", found, err)
	}
	if out.Name != "from-mysql" {
		t.Fatalf("unexpected data: %+v", out)
	}
	// 回填 Redis：下次直接命中热层
	if !mr.Exists("actor_data:player:555") {
		t.Fatal("redis backfill missing after mysql fallback")
	}
}

func TestNewManagerValidation(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	am := &actor.AManager{}

	if _, err := NewManager(Options{AM: nil, RDB: rdb}); err == nil {
		t.Fatal("expected error for nil AManager")
	}
	if _, err := NewManager(Options{AM: am, RDB: nil}); err == nil {
		t.Fatal("expected error for nil redis")
	}
	if _, err := NewManager(Options{
		AM:  am,
		RDB: rdb,
		Specs: map[string]CategorySpec{
			"bad": {Category: "bad", Table: "t", Codec: "json", FlushInterval: time.Second},
		},
	}); err == nil {
		t.Fatal("expected error for missing RedisKeyPrefix")
	}
	if _, err := NewManager(Options{
		AM:  am,
		RDB: rdb,
		Specs: map[string]CategorySpec{
			"bad": {
				Category:       "bad",
				RedisKeyPrefix: "k:",
				Table:          "t",
				Codec:          "yaml",
				FlushInterval:  time.Second,
			},
		},
	}); err == nil {
		t.Fatal("expected error for unknown codec")
	}
}
