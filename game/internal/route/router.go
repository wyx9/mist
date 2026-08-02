package routes

import (
	"errors"
	"google.golang.org/protobuf/proto"
	"simple_game/game/pkg"
)

type Handler func(ctx interface{}, req any) proto.Message

var routes = map[string]Handler{}

func AddRoute(protocol string, handler Handler) {
	routes[protocol] = handler
}

func Route(ctx interface{}, name string, msg any) (Handler, proto.Message, error) {
	defer func() {
		if err := recover(); err != nil {
			pkg.ERROR("HandleRequest error:", err)
		}
	}()
	handler, ok := routes[name]
	if !ok {
		return nil, nil, errors.New("not found handler")
	}
	f, ok := ProtoBufFactory[name]
	if !ok {
		return nil, nil, errors.New("not found proto factory for: " + name)
	}
	_ = proto.Unmarshal(msg.([]byte), f())
	message := handler(ctx, f())
	return handler, message, nil

}
