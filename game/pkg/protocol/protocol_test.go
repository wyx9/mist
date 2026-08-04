package protocol

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	"simple_game/game/api/protos/pt"
)

func TestEncodeDecodeMsg(t *testing.T) {
	msg := &Msg{
		MsgType: MsgTypeReq,
		MsgID:   42,
		UID:     1001,
		Token:   "token-abc",
		Cmd:     CmdLogin,
		Body:    []byte{0x01, 0x02, 0x03},
	}
	data, err := EncodeMsg(msg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	got, err := DecodeMsg(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.MsgType != MsgTypeReq {
		t.Fatalf("unexpected msg type: %d", got.MsgType)
	}
	if got.MsgID != 42 || got.UID != 1001 || got.Token != "token-abc" || got.Cmd != CmdLogin {
		t.Fatalf("unexpected header: %+v", got)
	}
	if len(got.Body) != 3 || got.Body[0] != 0x01 || got.Body[2] != 0x03 {
		t.Fatalf("unexpected body: %v", got.Body)
	}
}

func TestEncodeDecodeMsgEmptyBody(t *testing.T) {
	msg := &Msg{
		MsgType: MsgTypeReq,
		MsgID:   7,
		UID:     123,
		Cmd:     CmdHeart,
	}
	data, err := EncodeMsg(msg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	got, err := DecodeMsg(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.MsgType != MsgTypeReq || got.MsgID != 7 || got.UID != 123 || got.Cmd != CmdHeart {
		t.Fatalf("unexpected result: %+v", got)
	}
	if len(got.Body) != 0 {
		t.Fatalf("unexpected body: %v", got.Body)
	}
}

func TestDecodeMsgEmpty(t *testing.T) {
	msg, err := DecodeMsg(nil)
	if err != nil {
		t.Fatalf("decode empty: %v", err)
	}
	if msg.MsgType != 0 || msg.MsgID != 0 || msg.UID != 0 || msg.Token != "" || msg.Cmd != 0 || len(msg.Body) != 0 {
		t.Fatalf("expected zero msg, got %+v", msg)
	}
}

func TestDecodeMsgBadWire(t *testing.T) {
	// 只有 tag 没有值：字段 1 (varint) 被截断。
	if _, err := DecodeMsg([]byte{0x08}); err == nil {
		t.Fatal("expected error for truncated varint")
	}
	// 孤立字节无法解析为合法 tag。
	if _, err := DecodeMsg([]byte{0xff}); err == nil {
		t.Fatal("expected error for garbage tag")
	}
	// 字段类型不匹配：body(6) 用 varint 编码。
	b := protowire.AppendTag(nil, fieldBody, protowire.VarintType)
	b = protowire.AppendVarint(b, 1)
	if _, err := DecodeMsg(b); err == nil {
		t.Fatal("expected error for mismatched wire type")
	}
}

func TestDecodeMsgUnknownField(t *testing.T) {
	// 未知字段 99 应被跳过，其余字段正常解析。
	b := protowire.AppendTag(nil, 99, protowire.VarintType)
	b = protowire.AppendVarint(b, 123)
	b = protowire.AppendTag(b, fieldMsgType, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(MsgTypeRsp))
	b = protowire.AppendTag(b, fieldMsgID, protowire.VarintType)
	b = protowire.AppendVarint(b, 9)

	msg, err := DecodeMsg(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if msg.MsgType != MsgTypeRsp || msg.MsgID != 9 {
		t.Fatalf("unexpected result: %+v", msg)
	}
}

func TestPackBodyUnpackBody(t *testing.T) {
	msg := &Msg{MsgType: MsgTypeReq, MsgID: 1, UID: 1001, Cmd: CmdLogin}
	if err := msg.PackBody(&pt.LoginReq{UUid: 1001, Name: "test"}); err != nil {
		t.Fatalf("pack body: %v", err)
	}
	if len(msg.Body) == 0 {
		t.Fatal("expected non-empty body")
	}

	var req pt.LoginReq
	if err := msg.UnpackBody(&req); err != nil {
		t.Fatalf("unpack body: %v", err)
	}
	if req.UUid != 1001 || req.Name != "test" {
		t.Fatalf("unexpected req: %+v", &req)
	}
}

func TestUnpackBodyBadBody(t *testing.T) {
	msg := &Msg{Body: []byte{0xff, 0xff, 0xff}}
	var req pt.LoginReq
	if err := msg.UnpackBody(&req); err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestEncodeMsgNil(t *testing.T) {
	if _, err := EncodeMsg(nil); err == nil {
		t.Fatal("expected error for nil message")
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
