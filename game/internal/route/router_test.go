package route

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
	"simple_game/game/pkg/protocol"
)

func TestRegisterAndRoute(t *testing.T) {
	Register(protocol.CmdLogin, func(ctx any, req *pt.LoginReq) proto.Message {
		return &pt.LoginRes{UUid: req.UUid, Code: 1}
	})

	data, err := proto.Marshal(&pt.LoginReq{UUid: 1001})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resCmd, res, err := Route(nil, protocol.CmdLogin, data)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if resCmd != protocol.CmdLoginRsp {
		t.Fatalf("unexpected resCmd: %d", resCmd)
	}

	loginRes, ok := res.(*pt.LoginRes)
	if !ok {
		t.Fatalf("unexpected response type: %T", res)
	}
	if loginRes.UUid != 1001 || loginRes.Code != 1 {
		t.Fatalf("unexpected response: %+v", loginRes)
	}
}

func TestRouteUnknownCmd(t *testing.T) {
	if _, _, err := Route(nil, 9999, nil); err == nil {
		t.Fatal("expected error for unknown route")
	}
}

func TestRouteUnmarshalError(t *testing.T) {
	Register(9998, func(ctx any, req *pt.SayRequest) proto.Message {
		return nil
	})

	if _, _, err := Route(nil, 9998, []byte{0xff, 0xff, 0xff, 0xff}); err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	Register(9996, func(ctx any, req *pt.HeartReq) proto.Message { return nil })

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	Register(9996, func(ctx any, req *pt.HeartReq) proto.Message { return nil })
}

func TestRouteNoResponse(t *testing.T) {
	Register(9994, func(ctx any, req *pt.HeartReq) proto.Message { return nil })

	resCmd, res, err := Route(nil, 9994, nil)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if resCmd != 9995 {
		t.Fatalf("unexpected resCmd: %d", resCmd)
	}
	if res != nil {
		t.Fatalf("expected nil response, got %v", res)
	}
}
