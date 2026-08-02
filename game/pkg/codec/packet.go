package libs

import (
	"encoding/json"
	"google.golang.org/protobuf/proto"
	"reflect"
)

type PacketMsg struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

func Pack2Msg(data any) []byte {
	p := PacketMsg{}
	if msg, ok := data.(proto.Message); ok {
		of := reflect.TypeOf(data)
		p.Name = of.Elem().Name()
		marshal, _ := proto.Marshal(msg)
		p.Data = marshal
	}
	if msg, ok := data.(string); ok {
		p.Name = msg
		p.Data = []byte(msg)
	}
	return EnCodePack(&p)
}

func DeCodePack(by []byte) *PacketMsg {
	p := &PacketMsg{}
	if err := json.Unmarshal(by, p); err != nil {
		return nil
	}
	return p
}

func EnCodePack(pm *PacketMsg) []byte {
	marshal, err := json.Marshal(pm)
	if err != nil {
		return []byte{}
	}
	return marshal
}
