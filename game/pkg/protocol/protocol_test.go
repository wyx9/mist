package protocol

import (
	"testing"

	"simple_game/game/api/protos/pt"
)

func TestEncodeDecodeFrame(t *testing.T) {
	frame := EncodeFrame(CmdLogin, []byte{0x01, 0x02, 0x03})
	cmd, body, err := DecodeFrame(frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cmd != CmdLogin {
		t.Fatalf("unexpected cmd: %d", cmd)
	}
	if len(body) != 3 || body[0] != 0x01 || body[2] != 0x03 {
		t.Fatalf("unexpected body: %v", body)
	}
}

func TestDecodeFrameEmptyBody(t *testing.T) {
	frame := EncodeFrame(CmdHeart, nil)
	cmd, body, err := DecodeFrame(frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cmd != CmdHeart || len(body) != 0 {
		t.Fatalf("unexpected result: cmd=%d body=%v", cmd, body)
	}
}

func TestDecodeFrameTooShort(t *testing.T) {
	if _, _, err := DecodeFrame([]byte{0x00}); err == nil {
		t.Fatal("expected error for frame shorter than 2 bytes")
	}
	if _, _, err := DecodeFrame(nil); err == nil {
		t.Fatal("expected error for nil frame")
	}
}

func TestPackCmdUnpackCmd(t *testing.T) {
	frame, err := PackCmd(CmdLogin, &pt.LoginReq{UUid: 1001, Name: "test"})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}

	var req pt.LoginReq
	cmd, err := UnpackCmd(frame, &req)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	if cmd != CmdLogin {
		t.Fatalf("unexpected cmd: %d", cmd)
	}
	if req.UUid != 1001 || req.Name != "test" {
		t.Fatalf("unexpected req: %+v", &req)
	}
}

func TestUnpackCmdBadBody(t *testing.T) {
	frame := EncodeFrame(CmdLogin, []byte{0xff, 0xff, 0xff})
	var req pt.LoginReq
	if _, err := UnpackCmd(frame, &req); err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestRspCmd(t *testing.T) {
	if got := RspCmd(CmdLogin); got != CmdLoginRsp {
		t.Fatalf("RspCmd(CmdLogin) = %d, want %d", got, CmdLoginRsp)
	}
	if got := RspCmd(CmdHeart); got != CmdHeartRsp {
		t.Fatalf("RspCmd(CmdHeart) = %d, want %d", got, CmdHeartRsp)
	}
	// 未绑定：默认 = 请求命令号 + 1
	if got := RspCmd(100); got != 101 {
		t.Fatalf("RspCmd(100) = %d, want 101", got)
	}
}
