package rpc

import (
	"fmt"
	"reflect"

	"google.golang.org/protobuf/proto"

	"simple_game/game/pkg/actortype"
)

// dispatchFunc 注册项的统一调用入口：断言 actor 上下文、执行具体 handler。
type dispatchFunc func(actor any, comm Comm, from string, payload interface{}) (interface{}, error)

// entry 内部消息注册项：路由键为请求消息名（远端帧 Method 用），
// actorType 供接收端按需创建/复活 actor；byType/byName 双索引分别服务本地分发与远端路由。
type entry struct {
	name      string
	actorType actortype.ActorType
	reqType   reflect.Type
	resType   reflect.Type
	invoke    dispatchFunc
}

var (
	byType = map[reflect.Type]*entry{}
	byName = map[string]*entry{}
)

// Register 注册异步内部消息（Cast，无回包）。
// 路由键由请求消息类型反射得到（消息名）；actorType 为归属的 actor 类型。
func Register[A any, T proto.Message](actorType actortype.ActorType, h func(ctx *HandlerContext[A], req T) error) {
	var zero T
	add(actorType, reflect.TypeOf(zero).Elem(), nil, func(actor any, comm Comm, from string, payload interface{}) (interface{}, error) {
		hctx := &HandlerContext[A]{Actor: actor.(A), Comm: comm, from: from}
		return nil, h(hctx, payload.(T))
	})
}

// RegisterCall 注册同步内部消息（Call，带回包）；路由键同样来自请求消息名。
func RegisterCall[A any, T proto.Message, R proto.Message](actorType actortype.ActorType, h func(ctx *HandlerContext[A], req T) (R, error)) {
	var zeroReq T
	var zeroRes R
	add(actorType, reflect.TypeOf(zeroReq).Elem(), reflect.TypeOf(zeroRes).Elem(), func(actor any, comm Comm, from string, payload interface{}) (interface{}, error) {
		hctx := &HandlerContext[A]{Actor: actor.(A), Comm: comm, from: from}
		return h(hctx, payload.(T))
	})
}

func add(actorType actortype.ActorType, reqType, resType reflect.Type, invoke dispatchFunc) {
	name := reqType.Name()
	if name == "" {
		panic(fmt.Sprintf("rpc: empty message name: %v", reqType))
	}
	if _, ok := byName[name]; ok {
		panic(fmt.Sprintf("rpc: duplicate message name: %s", name))
	}
	e := &entry{name: name, actorType: actorType, reqType: reqType, resType: resType, invoke: invoke}
	byName[name] = e
	// 分发按消息指针类型索引（reflect.TypeOf(payload) 得到的是 *pt.X）
	byType[reflect.PtrTo(reqType)] = e
}

// Dispatch 本地分发：按 payload 具体类型查表；未注册时返回 handled=false，由 core 回退。
func Dispatch(actor any, comm Comm, from string, payload interface{}) (handled bool, result interface{}, err error) {
	e, ok := byType[reflect.TypeOf(payload)]
	if !ok {
		return false, nil, nil
	}
	result, err = e.invoke(actor, comm, from, payload)
	return true, result, err
}

// MethodOf 反查消息类型对应的路由键（Protobuf 消息名，远端调用 marshal 前用）。
func MethodOf(payload interface{}) (string, error) {
	e, ok := byType[reflect.TypeOf(payload)]
	if !ok {
		return "", fmt.Errorf("rpc: message type %T not registered", payload)
	}
	return e.name, nil
}

// lookup 按消息名反查注册项（远端服务端 unmarshal 用）。
func lookup(name string) (*entry, error) {
	e, ok := byName[name]
	if !ok {
		return nil, fmt.Errorf("rpc: unknown message: %s", name)
	}
	return e, nil
}

// newRequest 按注册项构造一个新的请求消息实例（用于 unmarshal）。
func (e *entry) newRequest() proto.Message {
	return reflect.New(e.reqType).Interface().(proto.Message)
}
