package rpc

import (
	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
)

// MarshalFrame 序列化 RpcFrame 信封。
func MarshalFrame(f *pt.RpcFrame) ([]byte, error) {
	return proto.Marshal(f)
}

// UnmarshalFrame 解析 RpcFrame 信封。
func UnmarshalFrame(data []byte, f *pt.RpcFrame) error {
	return proto.Unmarshal(data, f)
}
