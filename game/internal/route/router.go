package route

import (
	"errors"
	"fmt"
	"reflect"

	"google.golang.org/protobuf/proto"

	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/protocol"
)

// dispatchFunc 路由注册项：负责新建消息实例、反序列化并调用具体 handler。
type dispatchFunc func(ctx any, data []byte) (proto.Message, error)

type routeEntry struct {
	resCmd uint16
	name   string
	handle dispatchFunc
}

var routes = map[uint16]routeEntry{}

// Register 泛型注册入口：reqCmd 为请求命令号，响应命令号由 protocol.ReqRsp 绑定表决定。
// handler 收到已反序列化、类型安全的 T；命令号重复注册会直接 panic。
func Register[T proto.Message](reqCmd uint16, h func(ctx any, req T) proto.Message) {
	var zero T
	typ := reflect.TypeOf(zero).Elem()
	if _, ok := routes[reqCmd]; ok {
		panic(fmt.Sprintf("duplicate route cmd: %d", reqCmd))
	}
	routes[reqCmd] = routeEntry{
		resCmd: protocol.RspCmd(reqCmd),
		name:   typ.Name(),
		handle: func(ctx any, data []byte) (proto.Message, error) {
			req := reflect.New(typ).Interface().(T)
			if err := proto.Unmarshal(data, req); err != nil {
				return nil, err
			}
			return h(ctx, req), nil
		},
	}
}

// Route 按命令号分发，返回响应命令号与响应消息。
func Route(ctx any, cmd uint16, data []byte) (resCmd uint16, res proto.Message, err error) {
	defer func() {
		if r := recover(); r != nil {
			logger.ERROR("HandleRequest error:", r)
			err = fmt.Errorf("handler panic: %v", r)
		}
	}()
	entry, ok := routes[cmd]
	if !ok {
		return 0, nil, errors.New("not found handler")
	}
	res, err = entry.handle(ctx, data)
	if err != nil {
		return 0, nil, err
	}
	return entry.resCmd, res, nil
}
