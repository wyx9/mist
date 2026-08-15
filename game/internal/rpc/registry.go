package rpc

import (
	"fmt"
	"reflect"

	"google.golang.org/protobuf/proto"
)

// dispatchFunc 注册项的统一调用入口：断言 actor 上下文、执行具体 handler。
type dispatchFunc func(actor any, comm Comm, from string, payload interface{}) (interface{}, error)

// entry 单个内部消息方法的注册项。
// reqType/resType 存结构体类型（如 pt.RoomGetPlayersReq），用于 reflect.New 构造实例；
// 分发按消息指针类型（*pt.RoomGetPlayersReq）索引，见 add 内的 byType。
type entry struct {
	method  string
	reqType reflect.Type // 请求结构体类型
	resType reflect.Type // 响应结构体类型；Send 无响应时为 nil
	invoke  dispatchFunc
}

var (
	byType   = map[reflect.Type]*entry{}
	byMethod = map[string]*entry{}
)

// Register 注册异步内部消息（Send，无回包）。
// method 为远端路由标识，如 "room.join"；A 为 actor 具体类型，T 为消息类型。
func Register[A any, T proto.Message](method string, h func(ctx *HandlerContext[A], req T) error) {
	var zero T
	add(method, reflect.TypeOf(zero).Elem(), nil, func(actor any, comm Comm, from string, payload interface{}) (interface{}, error) {
		hctx := &HandlerContext[A]{Actor: actor.(A), Comm: comm, from: from}
		return nil, h(hctx, payload.(T))
	})
}

// RegisterCall 注册同步内部消息（Call，带回包）。
// method 为远端路由标识，如 "room.get_players"；R 为响应类型。
func RegisterCall[A any, T proto.Message, R proto.Message](method string, h func(ctx *HandlerContext[A], req T) (R, error)) {
	var zeroReq T
	var zeroRes R
	add(method, reflect.TypeOf(zeroReq).Elem(), reflect.TypeOf(zeroRes).Elem(), func(actor any, comm Comm, from string, payload interface{}) (interface{}, error) {
		hctx := &HandlerContext[A]{Actor: actor.(A), Comm: comm, from: from}
		return h(hctx, payload.(T))
	})
}

func add(method string, reqType, resType reflect.Type, invoke dispatchFunc) {
	if _, ok := byMethod[method]; ok {
		panic(fmt.Sprintf("rpc: duplicate method: %s", method))
	}
	e := &entry{method: method, reqType: reqType, resType: resType, invoke: invoke}
	byMethod[method] = e
	// 分发按消息指针类型索引（reflect.TypeOf(payload) 得到的是 *pt.X）
	byType[reflect.PtrTo(reqType)] = e
}

// Dispatch 本地分发：按 payload 的具体类型查找 handler 并执行。
// 返回 handled=false 表示该类型未注册，由调用方（core）回退到 actor 的 HandleMessage。
func Dispatch(actor any, comm Comm, from string, payload interface{}) (handled bool, result interface{}, err error) {
	e, ok := byType[reflect.TypeOf(payload)]
	if !ok {
		return false, nil, nil
	}
	result, err = e.invoke(actor, comm, from, payload)
	return true, result, err
}

// MethodOf 反查消息类型对应的 method（远端客户端 marshal 前用）。
func MethodOf(payload interface{}) (string, error) {
	e, ok := byType[reflect.TypeOf(payload)]
	if !ok {
		return "", fmt.Errorf("rpc: message type %T not registered", payload)
	}
	return e.method, nil
}

// lookup 按 method 反查注册项（远端服务端 unmarshal 用）。
func lookup(method string) (*entry, error) {
	e, ok := byMethod[method]
	if !ok {
		return nil, fmt.Errorf("rpc: unknown method: %s", method)
	}
	return e, nil
}

// newRequest 按注册项构造一个新的请求消息实例（用于 unmarshal）。
func (e *entry) newRequest() proto.Message {
	return reflect.New(e.reqType).Interface().(proto.Message)
}
