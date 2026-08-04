// Package protocol 定义业务消息的信封结构体与命令号。
// 逻辑消息为 protobuf 信封 Msg（含消息类型/消息 ID/uid/token/cmd/body），
// TCP 传输层外套 4B 长度头，WS 直接以二进制帧承载。
package protocol

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// MsgType 消息类型，区分请求/响应/推送。
type MsgType uint16

const (
	MsgTypeReq  MsgType = 1 // 请求（含心跳）
	MsgTypeRsp  MsgType = 2 // 响应
	MsgTypePush MsgType = 3 // 推送（服务端主动下发，本期预留）
)

// Msg 消息信封结构体：消息头字段 + 业务 protobuf 消息体。
type Msg struct {
	MsgType MsgType // 消息类型：请求/响应/推送
	MsgID   uint64  // 消息 ID：客户端生成递增，服务端响应原样回填
	UID     int64   // 玩家 uid：登录时绑定，后续消息携带
	Token   string  // 认证 token：登录消息必带，其余消息可选携带
	Cmd     uint16  // 业务命令号：CmdLogin / CmdHeart 等
	Body    []byte  // 业务 protobuf 消息体
}

// 信封字段编号（与标准 protobuf wire 格式兼容，未来可补 .proto 定义）。
const (
	fieldMsgType protowire.Number = 1 // varint: msg_type
	fieldMsgID   protowire.Number = 2 // varint: msg_id
	fieldUID     protowire.Number = 3 // varint: uid (int64)
	fieldToken   protowire.Number = 4 // string: token
	fieldCmd     protowire.Number = 5 // varint: cmd
	fieldBody    protowire.Number = 6 // bytes: body
)

// ErrInvalidWire 消息字节流非法（截断或字段类型不匹配）。
var ErrInvalidWire = errors.New("invalid message wire data")

// EncodeMsg 将消息信封序列化为 protobuf wire 字节。
// 零值字段按 proto3 语义省略，未知调用方无需关心。
func EncodeMsg(m *Msg) ([]byte, error) {
	if m == nil {
		return nil, errors.New("nil message")
	}
	var b []byte
	if m.MsgType != 0 {
		b = protowire.AppendTag(b, fieldMsgType, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(m.MsgType))
	}
	if m.MsgID != 0 {
		b = protowire.AppendTag(b, fieldMsgID, protowire.VarintType)
		b = protowire.AppendVarint(b, m.MsgID)
	}
	if m.UID != 0 {
		b = protowire.AppendTag(b, fieldUID, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(m.UID))
	}
	if m.Token != "" {
		b = protowire.AppendTag(b, fieldToken, protowire.BytesType)
		b = protowire.AppendString(b, m.Token)
	}
	if m.Cmd != 0 {
		b = protowire.AppendTag(b, fieldCmd, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(m.Cmd))
	}
	if len(m.Body) > 0 {
		b = protowire.AppendTag(b, fieldBody, protowire.BytesType)
		b = protowire.AppendBytes(b, m.Body)
	}
	return b, nil
}

// DecodeMsg 将 protobuf wire 字节解析为消息信封结构体。
// 空数据解析为零值 Msg；未知字段跳过（保证向前兼容）；截断/坏数据返回错误。
func DecodeMsg(data []byte) (*Msg, error) {
	m := &Msg{}
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil, fmt.Errorf("%w: tag: %v", ErrInvalidWire, protowire.ParseError(n))
		}
		data = data[n:]
		switch num {
		case fieldMsgType:
			v, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return nil, fmt.Errorf("%w: msg_type: %v", ErrInvalidWire, protowire.ParseError(n))
			}
			m.MsgType = MsgType(v)
			data = data[n:]
		case fieldMsgID:
			v, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return nil, fmt.Errorf("%w: msg_id: %v", ErrInvalidWire, protowire.ParseError(n))
			}
			m.MsgID = v
			data = data[n:]
		case fieldUID:
			v, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return nil, fmt.Errorf("%w: uid: %v", ErrInvalidWire, protowire.ParseError(n))
			}
			m.UID = int64(v)
			data = data[n:]
		case fieldToken:
			v, n := protowire.ConsumeString(data)
			if n < 0 {
				return nil, fmt.Errorf("%w: token: %v", ErrInvalidWire, protowire.ParseError(n))
			}
			m.Token = v
			data = data[n:]
		case fieldCmd:
			v, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return nil, fmt.Errorf("%w: cmd: %v", ErrInvalidWire, protowire.ParseError(n))
			}
			m.Cmd = uint16(v)
			data = data[n:]
		case fieldBody:
			v, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return nil, fmt.Errorf("%w: body: %v", ErrInvalidWire, protowire.ParseError(n))
			}
			m.Body = v
			data = data[n:]
		default:
			// 未知字段按 wire 类型跳过，保持向前兼容。
			n := protowire.ConsumeFieldValue(num, typ, data)
			if n < 0 {
				return nil, fmt.Errorf("%w: unknown field %d: %v", ErrInvalidWire, num, protowire.ParseError(n))
			}
			data = data[n:]
		}
	}
	return m, nil
}

// PackBody 将业务 proto 消息序列化后写入 Body 字段。
func (m *Msg) PackBody(msg proto.Message) error {
	body, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	m.Body = body
	return nil
}

// UnpackBody 将 Body 字段反序列化到业务 proto 消息。
func (m *Msg) UnpackBody(msg proto.Message) error {
	return proto.Unmarshal(m.Body, msg)
}
