package storage

import "encoding/json"

// Codec 数据序列化接口：JSON 起步，后续可增加 protobuf 实现而不改存储层。
type Codec interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// JSONCodec 默认 JSON 实现。
type JSONCodec struct{}

func (JSONCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (JSONCodec) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
