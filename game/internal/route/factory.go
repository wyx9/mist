package routes

import (
	"google.golang.org/protobuf/proto"
	"reflect"
	"simple_game/game/api/protos/pt"
)

var ProtoBufFactory = make(map[string]func() proto.Message)

func RegisterProtoBufFactory(message any) {
	p, ok := message.(proto.Message)
	if !ok {
		return
	}
	name := reflect.TypeOf(p).Elem().Name()
	ProtoBufFactory[name] = func() proto.Message {
		return p
	}
}

func Init() {
	RegisterProtoBufFactory(new(pt.LoginReq))
	RegisterProtoBufFactory(new(pt.HeartReq))
}
