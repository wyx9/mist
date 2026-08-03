package actor

import (
	"context"
	"fmt"
	"testing"
	"time"

	goredis "github.com/go-redis/redis/v8"

	"simple_game/game/pkg/network"
	"simple_game/game/pkg/redis"
)

// testActor 仅用于测试的 IActor 实现。
type testActor struct{}

func (t *testActor) Start()                                     {}
func (t *testActor) Stop()                                      {}
func (t *testActor) Handler(ctx *ActorContext, msg interface{}) {}
func (t *testActor) Category() string                           { return "test" }

// echoActor 收到 RPC 后回 "echo:<msg>"。
type echoActor struct{}

func (e *echoActor) Start() {}
func (e *echoActor) Stop()  {}
func (e *echoActor) Handler(ctx *ActorContext, msg interface{}) {
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
func (p *proxyActor) Handler(ctx *ActorContext, msg interface{}) {
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

func (s *silentActor) Start()                                     {}
func (s *silentActor) Stop()                                      {}
func (s *silentActor) Handler(ctx *ActorContext, msg interface{}) {}
func (s *silentActor) Category() string                           { return "test" }

// panicActor handler 主动 panic。
type panicActor struct{}

func (p *panicActor) Start()                                     {}
func (p *panicActor) Stop()                                      {}
func (p *panicActor) Handler(ctx *ActorContext, msg interface{}) { panic("boom") }
func (p *panicActor) Category() string                           { return "test" }

// captureActor 记录收到的异步消息。
type captureActor struct {
	ch chan interface{}
}

func (c *captureActor) Start()                                     {}
func (c *captureActor) Stop()                                      {}
func (c *captureActor) Handler(ctx *ActorContext, msg interface{}) { c.ch <- msg }
func (c *captureActor) Category() string                           { return "test" }

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
	// Player.Start 会读 Redis；测试环境给一个不可达的客户端，Get 失败返回空值即可。
	redis.RedisClient = goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	redis.RCtx = context.Background()

	am := &AManager{}
	am.RegisterCategory(&Player{})

	p, err := am.StartPlayer("p1", &fakeConn{})
	if err != nil {
		t.Fatalf("start player: %v", err)
	}
	if p == nil || p.name != "p1" {
		t.Fatalf("unexpected player: %+v", p)
	}

	ab := am.Find("p1")
	if ab == nil {
		t.Fatal("player actor not registered")
	}
	if ab.iActor.Category() != "player" {
		t.Fatalf("unexpected category: %s", ab.iActor.Category())
	}

	if err := am.CloseActor("p1", time.Second); err != nil {
		t.Fatalf("close: %v", err)
	}
	if am.Find("p1") != nil {
		t.Fatal("player actor still registered after close")
	}
}

func TestCallRoundtrip(t *testing.T) {
	am := &AManager{}
	am.RegisterCategory(&echoActor{})
	if _, err := am.StartActor("b", &echoActor{}, nil); err != nil {
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

	if _, err := am.StartActor("b", &echoActor{}, nil); err != nil {
		t.Fatalf("start b: %v", err)
	}
	if _, err := am.StartActor("a", &proxyActor{target: "b", msg: "ping"}, nil); err != nil {
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
	if _, err := am.StartActor("a", &echoActor{}, nil); err != nil {
		t.Fatalf("start a: %v", err)
	}

	if _, err := am.Call("a", "a", "x", time.Second); err != ErrSelfCall {
		t.Fatalf("expected ErrSelfCall, got %v", err)
	}
}

func TestCallNoReplyAutoNil(t *testing.T) {
	am := &AManager{}
	am.RegisterCategory(&silentActor{})
	if _, err := am.StartActor("s", &silentActor{}, nil); err != nil {
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
	if _, err := am.StartActor("p", &panicActor{}, nil); err != nil {
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
	if _, err := am.StartActor("c", captured, nil); err != nil {
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
