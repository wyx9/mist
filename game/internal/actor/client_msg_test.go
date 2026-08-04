package actor

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
	"simple_game/game/internal/route"
	"simple_game/game/pkg/protocol"
)

// captureWriter 记录回包字节（通过 channel 同步）。
type captureWriter struct {
	ch chan []byte
}

func (c *captureWriter) WriteMessage(data []byte) error {
	c.ch <- data
	return nil
}

// clientTestActor 仅用于测试的客户端消息 actor，刻意不实现任何 Handler 方法，
// 验证客户端消息由框架默认分发。
type clientTestActor struct {
	captureWriter
}

func (a *clientTestActor) Start()           {}
func (a *clientTestActor) Stop()            {}
func (a *clientTestActor) Category() string { return "client_test" }

func TestHandleClientMsgWritesResponse(t *testing.T) {
	route.Register[*clientTestActor, *pt.HeartReq](9992, func(ctx *route.HandlerContext[*clientTestActor], req *pt.HeartReq) (proto.Message, error) {
		return &pt.HeartRes{Time: 123}, nil
	})

	a := &clientTestActor{captureWriter: captureWriter{ch: make(chan []byte, 1)}}
	m := &protocol.Msg{MsgType: protocol.MsgTypeReq, MsgID: 9, UID: 77, Cmd: 9992}
	if err := m.PackBody(&pt.HeartReq{}); err != nil {
		t.Fatalf("pack body: %v", err)
	}
	HandleClientMsg(a, &ActorContext{}, m)

	var data []byte
	select {
	case data = <-a.ch:
	case <-time.After(time.Second):
		t.Fatal("expected response written")
	}
	rsp, err := protocol.DecodeMsg(data)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rsp.Cmd != 9993 || rsp.MsgID != 9 || rsp.UID != 77 {
		t.Fatalf("unexpected response: %+v", rsp)
	}
	var res pt.HeartRes
	if err := rsp.UnpackBody(&res); err != nil {
		t.Fatalf("unpack response: %v", err)
	}
	if res.Time != 123 {
		t.Fatalf("unexpected res: %+v", &res)
	}
}

// TestActorDefaultClientDispatch 验证 actor 无需实现 Handler：
// StartActor 启动后客户端消息自动走 HandleClientMsg 并回包。
func TestActorDefaultClientDispatch(t *testing.T) {
	route.Register[*clientTestActor, *pt.HeartReq](9993, func(ctx *route.HandlerContext[*clientTestActor], req *pt.HeartReq) (proto.Message, error) {
		return &pt.HeartRes{Time: 456}, nil
	})

	am := &AManager{}
	am.RegisterCategory(&clientTestActor{})
	a := &clientTestActor{captureWriter: captureWriter{ch: make(chan []byte, 1)}}
	if _, err := StartActor(am, "c", a, nil); err != nil {
		t.Fatalf("start actor: %v", err)
	}
	defer am.CloseActor("c", time.Second)

	m := &protocol.Msg{MsgType: protocol.MsgTypeReq, MsgID: 3, UID: 4, Cmd: 9993}
	if err := m.PackBody(&pt.HeartReq{}); err != nil {
		t.Fatalf("pack body: %v", err)
	}
	if err := am.Deliver("c", m); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	select {
	case data := <-a.ch:
		rsp, err := protocol.DecodeMsg(data)
		if err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if rsp.Cmd != 9994 || rsp.MsgID != 3 || rsp.UID != 4 {
			t.Fatalf("unexpected response: %+v", rsp)
		}
	case <-time.After(time.Second):
		t.Fatal("no response delivered")
	}
}

func TestHandleClientMsgNonMsg(t *testing.T) {
	a := &clientTestActor{captureWriter: captureWriter{ch: make(chan []byte, 1)}}
	HandleClientMsg(a, &ActorContext{}, "not a msg") // 不应 panic，也不应回包
	select {
	case <-a.ch:
		t.Fatal("unexpected write")
	default:
	}
}
