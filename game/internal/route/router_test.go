package route

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
	"simple_game/game/pkg/protocol"
)

// fakeActor 仅用于测试的 actor 类型。
type fakeActor struct{ name string }

// otherActor 用于 actor 类型不匹配测试。
type otherActor struct{}

func TestRegisterAndRoute(t *testing.T) {
	Register[fakeActor, *pt.LoginReq](protocol.CmdLogin, func(ctx *HandlerContext[fakeActor], req *pt.LoginReq) (proto.Message, error) {
		return &pt.LoginRes{UUid: req.UUid, Code: 1}, nil
	})

	req := &protocol.Msg{
		MsgType: protocol.MsgTypeReq,
		MsgID:   5,
		UID:     1001,
		Cmd:     protocol.CmdLogin,
	}
	if err := req.PackBody(&pt.LoginReq{UUid: 1001}); err != nil {
		t.Fatalf("pack body: %v", err)
	}
	hctx := &HandlerContext[fakeActor]{Actor: fakeActor{name: "a"}, Msg: req}

	rsp, err := Route(hctx, req)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if rsp.MsgType != protocol.MsgTypeRsp {
		t.Fatalf("unexpected msg type: %d", rsp.MsgType)
	}
	if rsp.MsgID != 5 || rsp.UID != 1001 {
		t.Fatalf("unexpected id echo: msgID=%d uid=%d", rsp.MsgID, rsp.UID)
	}
	if rsp.Cmd != protocol.CmdLoginRsp {
		t.Fatalf("unexpected resCmd: %d", rsp.Cmd)
	}

	var loginRes pt.LoginRes
	if err := rsp.UnpackBody(&loginRes); err != nil {
		t.Fatalf("unpack response: %v", err)
	}
	if loginRes.UUid != 1001 || loginRes.Code != 1 {
		t.Fatalf("unexpected response: %+v", &loginRes)
	}
}

func TestRouteHandlerError(t *testing.T) {
	Register[fakeActor, *pt.HeartReq](9991, func(ctx *HandlerContext[fakeActor], req *pt.HeartReq) (proto.Message, error) {
		return nil, errors.New("boom")
	})
	req := &protocol.Msg{MsgID: 7, UID: 88, Cmd: 9991}
	hctx := &HandlerContext[fakeActor]{Actor: fakeActor{}, Msg: req}

	rsp, err := Route(hctx, req)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if rsp == nil {
		t.Fatal("expected error response")
	}
	if rsp.Cmd != protocol.CmdError {
		t.Fatalf("unexpected cmd: %d", rsp.Cmd)
	}
	if rsp.MsgID != 7 || rsp.UID != 88 {
		t.Fatalf("unexpected id echo: msgID=%d uid=%d", rsp.MsgID, rsp.UID)
	}
	var er pt.ErrorRes
	if err := rsp.UnpackBody(&er); err != nil {
		t.Fatalf("unpack error res: %v", err)
	}
	if er.Code != protocol.CodeServerError || er.Msg == "" {
		t.Fatalf("unexpected error res: %+v", &er)
	}
}

func TestRouteBadBody(t *testing.T) {
	Register[fakeActor, *pt.SayRequest](9998, func(ctx *HandlerContext[fakeActor], req *pt.SayRequest) (proto.Message, error) {
		return nil, nil
	})
	req := &protocol.Msg{Cmd: 9998, Body: []byte{0xff, 0xff, 0xff, 0xff}}
	hctx := &HandlerContext[fakeActor]{Actor: fakeActor{}, Msg: req}

	rsp, err := Route(hctx, req)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	var er pt.ErrorRes
	if err := rsp.UnpackBody(&er); err != nil {
		t.Fatalf("unpack error res: %v", err)
	}
	if er.Code != protocol.CodeBadRequest {
		t.Fatalf("unexpected code: %d", er.Code)
	}
}

func TestRouteHandlerPanic(t *testing.T) {
	Register[fakeActor, *pt.HeartReq](9997, func(ctx *HandlerContext[fakeActor], req *pt.HeartReq) (proto.Message, error) {
		panic("boom")
	})
	req := &protocol.Msg{Cmd: 9997, MsgID: 3}
	hctx := &HandlerContext[fakeActor]{Actor: fakeActor{}, Msg: req}

	rsp, err := Route(hctx, req)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if rsp == nil || rsp.Cmd != protocol.CmdError {
		t.Fatalf("expected error response, got %+v", rsp)
	}
	var er pt.ErrorRes
	if err := rsp.UnpackBody(&er); err != nil {
		t.Fatalf("unpack error res: %v", err)
	}
	if er.Code != protocol.CodeServerError {
		t.Fatalf("unexpected code: %d", er.Code)
	}
}

func TestRouteActorMismatch(t *testing.T) {
	Register[fakeActor, *pt.HeartReq](9995, func(ctx *HandlerContext[fakeActor], req *pt.HeartReq) (proto.Message, error) {
		return nil, nil
	})
	req := &protocol.Msg{Cmd: 9995}
	wrongCtx := &HandlerContext[otherActor]{Actor: otherActor{}, Msg: req}
	if _, err := Route(wrongCtx, req); err == nil {
		t.Fatal("expected actor context mismatch error")
	}
}

func TestRouteUnknownCmd(t *testing.T) {
	if _, err := Route(nil, &protocol.Msg{Cmd: 9999}); err == nil {
		t.Fatal("expected error for unknown route")
	}
}

func TestRouteNoResponse(t *testing.T) {
	Register[fakeActor, *pt.HeartReq](9994, func(ctx *HandlerContext[fakeActor], req *pt.HeartReq) (proto.Message, error) {
		return nil, nil
	})

	req := &protocol.Msg{Cmd: 9994}
	hctx := &HandlerContext[fakeActor]{Actor: fakeActor{}, Msg: req}
	rsp, err := Route(hctx, req)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if rsp != nil {
		t.Fatalf("expected nil response, got %v", rsp)
	}
}

func TestRouteNilMsg(t *testing.T) {
	if _, err := Route(nil, nil); err == nil {
		t.Fatal("expected error for nil message")
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	Register[fakeActor, *pt.HeartReq](9996, func(ctx *HandlerContext[fakeActor], req *pt.HeartReq) (proto.Message, error) {
		return nil, nil
	})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	Register[fakeActor, *pt.HeartReq](9996, func(ctx *HandlerContext[fakeActor], req *pt.HeartReq) (proto.Message, error) {
		return nil, nil
	})
}
