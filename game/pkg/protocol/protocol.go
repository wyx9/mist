// Package protocol 定义业务消息的二进制帧格式与命令号。
// 逻辑帧格式：[2B 大端 cmd][protobuf body]；TCP 传输层再外套 4B 长度头。
package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
)

// ErrFrameTooShort 帧长度不足 2 字节（缺少 cmd）。
var ErrFrameTooShort = errors.New("frame too short")

// EncodeFrame 将 cmd 与 protobuf body 编码为一帧逻辑消息。
func EncodeFrame(cmd uint16, body []byte) []byte {
	frame := make([]byte, 2+len(body))
	binary.BigEndian.PutUint16(frame, cmd)
	copy(frame[2:], body)
	return frame
}

// DecodeFrame 解析一帧逻辑消息，返回 cmd 与 body。
func DecodeFrame(data []byte) (uint16, []byte, error) {
	if len(data) < 2 {
		return 0, nil, fmt.Errorf("%w: got %d bytes", ErrFrameTooShort, len(data))
	}
	return binary.BigEndian.Uint16(data), data[2:], nil
}

// PackCmd 将 proto 消息序列化并封装为逻辑帧。
func PackCmd(cmd uint16, msg proto.Message) ([]byte, error) {
	body, err := proto.Marshal(msg)
	if err != nil {
		return nil, err
	}
	return EncodeFrame(cmd, body), nil
}

// UnpackCmd 解析逻辑帧并将 body 反序列化到 msg，返回 cmd。
func UnpackCmd(data []byte, msg proto.Message) (uint16, error) {
	cmd, body, err := DecodeFrame(data)
	if err != nil {
		return 0, err
	}
	return cmd, proto.Unmarshal(body, msg)
}
