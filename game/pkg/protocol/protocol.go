// Package protocol 定义业务消息信封与编解码。
// 信封字段：msg_type/msg_id/uid/token/name/body；路由键为业务消息名。
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
	MsgTypeReq  MsgType = 1
	MsgTypeRsp  MsgType = 2
	MsgTypePush MsgType = 3
)

// Msg 消息信封：消息头 + 业务 protobuf 消息体。
type Msg struct {
	MsgType MsgType
	MsgID   uint64 // 客户端生成，服务端响应原样回填
	UID     int64  // 玩家 uid：登录时绑定
	Token   string // 认证 token：登录必带，其余可选
	Name    string // 业务消息名（如 LoginReq），即路由键
	Body    []byte
}

// 信封字段编号（与标准 protobuf wire 格式兼容，未来可补 .proto 定义）。
const (
	fieldMsgType protowire.Number = 1 // varint: msg_type
	fieldMsgID   protowire.Number = 2 // varint: msg_id
	fieldUID     protowire.Number = 3 // varint: uid (int64)
	fieldToken   protowire.Number = 4 // string: token
	fieldName    protowire.Number = 5 // string: name（业务消息名）
	fieldBody    protowire.Number = 6 // bytes: body
)

// ErrInvalidWire 消息字节流非法（截断或字段类型不匹配）。
var ErrInvalidWire = errors.New("invalid message wire data")

// EncodeMsg 序列化消息信封（零值字段省略）。
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
	if m.Name != "" {
		b = protowire.AppendTag(b, fieldName, protowire.BytesType)
		b = protowire.AppendString(b, m.Name)
	}
	if len(m.Body) > 0 {
		b = protowire.AppendTag(b, fieldBody, protowire.BytesType)
		b = protowire.AppendBytes(b, m.Body)
	}
	return b, nil
}

// DecodeMsg 解析消息信封；空数据为零值，未知字段跳过（向前兼容），坏数据返回错误。
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
		case fieldName:
			v, n := protowire.ConsumeString(data)
			if n < 0 {
				return nil, fmt.Errorf("%w: name: %v", ErrInvalidWire, protowire.ParseError(n))
			}
			m.Name = v
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
