package actor

import (
	"fmt"
	"testing"
	"time"

	"simple_game/game/pkg/network"
)

// testActor 仅用于测试的 IActor 实现。
type testActor struct{}

func (t *testActor) Start()           {}
func (t *testActor) Stop()            {}
func (t *testActor) Category() string { return "test" }

// echoActor 收到 RPC 后回 "echo:<msg>"。
type echoActor struct{}

func (e *echoActor) Start() {}
func (e *echoActor) Stop()  {}
func (e *echoActor) HandleMessage(ctx *ActorContext, msg interface{}) {
	ctx.Reply("echo:" + fmt.Sprint(msg))
}
func (e *echoActor) Category() string { return "test" }

// proxyActor 收到消息后转发 RPC 到 target，并把结果回给调用方。
type proxyActor struct {
	target string
	msg    interface{}
}

func (p *proxyActor) Start() {}
func (p *proxyActor) Stop()  {}
func (p *proxyActor) HandleMessage(ctx *ActorContext, msg interface{}) {
	res, err := ctx.Call(p.target, p.msg, time.Second)
	if err != nil {
		ctx.Reply(err)
		return
	}
	ctx.Reply(res)
}
func (p *proxyActor) Category() string { return "test" }

// silentActor 收到消息不回复。
type silentActor struct{}

func (s *silentActor) Start()                                           {}
func (s *silentActor) Stop()                                            {}
func (s *silentActor) HandleMessage(ctx *ActorContext, msg interface{}) {}
func (s *silentActor) Category() string                                 { return "test" }

// panicActor handler 主动 panic。
type panicActor struct{}

func (p *panicActor) Start()                                           {}
func (p *panicActor) Stop()                                            {}
func (p *panicActor) HandleMessage(ctx *ActorContext, msg interface{}) { panic("boom") }
func (p *panicActor) Category() string                                 { return "test" }

// captureActor 记录收到的异步消息。
type captureActor struct {
	ch chan interface{}
}

func (c *captureActor) Start()                                           {}
func (c *captureActor) Stop()                                            {}
func (c *captureActor) HandleMessage(ctx *ActorContext, msg interface{}) { c.ch <- msg }
func (c *captureActor) Category() string                                 { return "test" }

// fakeConn 仅用于测试的 NetConn 实现。
type fakeConn struct{}

var _ network.NetConn = (*fakeConn)(nil)

func (f *fakeConn) ReadMessage() ([]byte, error)       { return nil, nil }
func (f *fakeConn) WriteMessage(data []byte) error     { return nil }
func (f *fakeConn) RemoteAddr() string                 { return "127.0.0.1:1" }
func (f *fakeConn) Close() error                       { return nil }
func (f *fakeConn) SetReadDeadline(t time.Time) error  { return nil }
func (f *fakeConn) SetWriteDeadline(t time.Time) error { return nil }

func TestCloseActor(t *testing.T) {
	am := &AManager{}
	am.RegisterCategory(&testActor{})

	ab := NewActorBase("a1", &testActor{}, nil)
	if err := am.Add(ab); err != nil {
		t.Fatalf("add: %v", err)
	}
	if am.Find("a1") == nil {
		t.Fatal("actor not found after add")
	}

	if err := am.CloseActor("a1", time.Second); err != nil {
		t.Fatalf("close: %v", err)
	}
	if am.Find("a1") != nil {
		t.Fatal("actor still registered after close")
	}

	if err := am.CloseActor("a1", time.Second); err != ErrActorNotFound {
		t.Fatalf("expected ErrActorNotFound, got %v", err)
	}
}

func TestStartPlayer(t *testing.T) {
	am := &AManager{}
	am.RegisterCategory(&Player{})

	p, err := am.StartPlayer("p1", 1001, &fakeConn{}, nil)
	if err != nil {
		t.Fatalf("start player: %v", err)
	}
	if p == nil || p.name != "p1" {
		t.Fatalf("unexpected player: %+v", p)
	}
	if p.uid != 1001 {
		t.Fatalf("unexpected player uid: %d", p.uid)
	}

	ab := am.Find("p1")
	if ab == nil {
		t.Fatal("player actor not registered")
	}
	if ab.iActor.Category() != "player" {
		t.Fatalf("unexpected category: %s", ab.iActor.Category())
	}
	if ab.ID() != 1001 {
		t.Fatalf("unexpected actor id: %d", ab.ID())
	}

	if err := am.CloseActor("p1", time.Second); err != nil {
		t.Fatalf("close: %v", err)
	}
	if am.Find("p1") != nil {
		t.Fatal("player actor still registered after close")
	}
}

// fixedIDActor 提供固定 ActorID，验证 StartActor 沿用而非重新分配。
type fixedIDActor struct {
	id int64
}

func (f *fixedIDActor) Start()           {}
func (f *fixedIDActor) Stop()            {}
func (f *fixedIDActor) Category() string { return "test" }
func (f *fixedIDActor) ActorID() int64   { return f.id }

func TestStartActorUIDAssignment(t *testing.T) {
	am := &AManager{}
	am.RegisterCategory(&testActor{})

	// 未提供 ActorID 的 actor：自动分配雪花 uid
	ab, err := StartActor(am, "auto", &testActor{}, nil)
	if err != nil {
		t.Fatalf("start auto: %v", err)
	}
	if ab.ID() == 0 {
		t.Fatal("expected auto-assigned uid")
	}
	if err := am.CloseActor("auto", time.Second); err != nil {
		t.Fatalf("close auto: %v", err)
	}

	// 提供非零 ActorID 的 actor：沿用该 id
	ab2, err := StartActor(am, "fixed", &fixedIDActor{id: 42}, nil)
	if err != nil {
		t.Fatalf("start fixed: %v", err)
	}
	if ab2.ID() != 42 {
		t.Fatalf("expected actor id 42, got %d", ab2.ID())
	}
	if err := am.CloseActor("fixed", time.Second); err != nil {
		t.Fatalf("close fixed: %v", err)
	}
}

func TestRangeByCategory(t *testing.T) {
	am := &AManager{}
	am.RegisterCategory(&testActor{})
	for _, name := range []string{"a", "b", "c"} {
		if _, err := StartActor(am, name, &testActor{}, nil); err != nil {
			t.Fatalf("start %s: %v", name, err)
		}
	}

	seen := map[string]bool{}
	am.RangeByCategory("test", func(name string, ab *ActorBase) bool {
		seen[name] = true
		return true
	})
	if len(seen) != 3 || !seen["a"] || !seen["b"] || !seen["c"] {
		t.Fatalf("unexpected ranged names: %v", seen)
	}

	// 未注册类别：空遍历
	am.RangeByCategory("nope", func(name string, ab *ActorBase) bool {
		t.Fatal("should not iterate unknown category")
		return true
	})
}

func TestCallRoundtrip(t *testing.T) {
	am := &AManager{}
	am.RegisterCategory(&echoActor{})
	if _, err := StartActor(am, "b", &echoActor{}, nil); err != nil {
		t.Fatalf("start b: %v", err)
	}

	res, err := am.Call("b", "a", "hello", time.Second)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res != "echo:hello" {
		t.Fatalf("unexpected result: %v", res)
	}
}

func TestActorCallActor(t *testing.T) {
	am := &AManager{}
	am.RegisterCategory(&echoActor{})
	am.RegisterCategory(&proxyActor{})

	if _, err := StartActor(am, "b", &echoActor{}, nil); err != nil {
		t.Fatalf("start b: %v", err)
	}
	if _, err := StartActor(am, "a", &proxyActor{target: "b", msg: "ping"}, nil); err != nil {
		t.Fatalf("start a: %v", err)
	}

	res, err := am.Call("a", "test", "ignored", time.Second)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res != "echo:ping" {
		t.Fatalf("unexpected result: %v", res)
	}
}

func TestSelfCallRejected(t *testing.T) {
	am := &AManager{}
	am.RegisterCategory(&echoActor{})
	if _, err := StartActor(am, "a", &echoActor{}, nil); err != nil {
		t.Fatalf("start a: %v", err)
	}

	if _, err := am.Call("a", "a", "x", time.Second); err != ErrSelfCall {
		t.Fatalf("expected ErrSelfCall, got %v", err)
	}
}

func TestCallNoReplyAutoNil(t *testing.T) {
	am := &AManager{}
	am.RegisterCategory(&silentActor{})
	if _, err := StartActor(am, "s", &silentActor{}, nil); err != nil {
		t.Fatalf("start s: %v", err)
	}

	res, err := am.Call("s", "test", "x", time.Second)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result, got %v", res)
	}
}

func TestCallPanicReturnsError(t *testing.T) {
	am := &AManager{}
	am.RegisterCategory(&panicActor{})
	if _, err := StartActor(am, "p", &panicActor{}, nil); err != nil {
		t.Fatalf("start p: %v", err)
	}

	_, err := am.Call("p", "test", "x", time.Second)
	if err == nil {
		t.Fatal("expected error from panicking handler")
	}
	if _, ok := err.(*CallError); !ok {
		t.Fatalf("expected *CallError, got %T", err)
	}
}

func TestSend(t *testing.T) {
	am := &AManager{}
	captured := &captureActor{ch: make(chan interface{}, 1)}
	am.RegisterCategory(captured)
	if _, err := StartActor(am, "c", captured, nil); err != nil {
		t.Fatalf("start c: %v", err)
	}

	if err := am.Send("c", "a", "hi"); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case m := <-captured.ch:
		if m != "hi" {
			t.Fatalf("unexpected msg: %v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("message not received")
	}
}

func TestCallUnknownActor(t *testing.T) {
	am := &AManager{}
	if _, err := am.Call("nope", "test", "x", time.Second); err != ErrActorNotFound {
		t.Fatalf("expected ErrActorNotFound, got %v", err)
	}
}
