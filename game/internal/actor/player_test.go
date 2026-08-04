package actor

import (
	"encoding/json"
	"testing"
)

// fakeHandle 记录 Load/Save 调用，供 Player 持久化逻辑测试。
type fakeHandle struct {
	saves   int
	loads   int
	found   bool
	loadErr error
	saveErr error
	data    []byte
}

func (f *fakeHandle) Load(actorID int64, v any) (bool, error) {
	f.loads++
	if f.loadErr != nil {
		return false, f.loadErr
	}
	if !f.found {
		return false, nil
	}
	return true, json.Unmarshal(f.data, v)
}

func (f *fakeHandle) Save(actorID int64, v any) error {
	f.saves++
	if f.saveErr != nil {
		return f.saveErr
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f.data = raw
	return nil
}

func (f *fakeHandle) Delete(actorID int64) error { return nil }

func TestPlayerLoadFromHandle(t *testing.T) {
	h := &fakeHandle{found: true, data: []byte(`{"UID":1001,"Name":"p1","LastLoginAt":123}`)}
	p := &Player{name: "p1", uid: 1001, handle: h, PlayerData: &PlayerData{}}
	p.loadPlayer()
	if p.PlayerData.UID != 1001 || p.PlayerData.Name != "p1" || p.PlayerData.LastLoginAt != 123 {
		t.Fatalf("unexpected loaded data: %+v", p.PlayerData)
	}
	if p.dirty {
		t.Fatal("dirty should be false after successful load")
	}
}

func TestPlayerLoadMissMarksDirty(t *testing.T) {
	h := &fakeHandle{found: false}
	p := &Player{name: "p1", uid: 1001, handle: h, PlayerData: &PlayerData{UID: 1001}}
	p.loadPlayer()
	if !p.dirty {
		t.Fatal("expected dirty after load miss (new player)")
	}
}

func TestPlayerTickFlushWhenDirty(t *testing.T) {
	h := &fakeHandle{}
	p := &Player{name: "p1", uid: 1001, handle: h, dirty: true, PlayerData: &PlayerData{UID: 1001, Name: "p1"}}
	p.HandleMessage(nil, PersistTick{})
	if h.saves != 1 {
		t.Fatalf("expected 1 save, got %d", h.saves)
	}
	if p.dirty {
		t.Fatal("dirty should be cleared after flush")
	}
}

func TestPlayerTickSkipsWhenClean(t *testing.T) {
	h := &fakeHandle{}
	p := &Player{name: "p1", uid: 1001, handle: h, PlayerData: &PlayerData{UID: 1001}}
	p.HandleMessage(nil, PersistTick{})
	if h.saves != 0 {
		t.Fatalf("unexpected save: %d", h.saves)
	}
}

func TestPlayerStopFlushes(t *testing.T) {
	h := &fakeHandle{}
	p := &Player{name: "p1", uid: 1001, handle: h, PlayerData: &PlayerData{UID: 1001, Name: "p1"}}
	p.Stop()
	if h.saves != 1 {
		t.Fatalf("expected save on stop, got %d", h.saves)
	}
}

func TestPlayerMarkDirtyAndFlush(t *testing.T) {
	h := &fakeHandle{}
	p := &Player{name: "p1", uid: 1001, handle: h, PlayerData: &PlayerData{UID: 1001}}
	p.MarkDirty()
	p.flushIfDirty()
	if h.saves != 1 {
		t.Fatalf("expected 1 save, got %d", h.saves)
	}
}
