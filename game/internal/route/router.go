package route

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/protocol"
)

// ActorComm 跨 actor 通信能力，由 actor/core.ActorContext 实现。
// handler 与其它 actor 通信只能走 Cast/Call（目标为 uid），禁止直接操作其内部状态。
type ActorComm interface {
	Cast(to int64, msg interface{}) error
	Call(to int64, msg interface{}, timeout time.Duration) (interface{}, error)
}

// HandlerContext 类型化 handler 上下文：
// Actor 为当前 actor 对象/数据（如 *actor.Player），Msg 为当前消息信封，
// Comm 提供跨 actor 通信。
type HandlerContext[A any] struct {
	Actor A
	Msg   *protocol.Msg
	Comm  ActorComm
}

// UID 返回当前消息的玩家 uid。
func (c *HandlerContext[A]) UID() int64 {
	if c.Msg == nil {
		return 0
	}
	return c.Msg.UID
}

// MsgID 返回当前消息 ID。
func (c *HandlerContext[A]) MsgID() uint64 {
	if c.Msg == nil {
		return 0
	}
	return c.Msg.MsgID
}

// Token 返回当前消息携带的 token。
func (c *HandlerContext[A]) Token() string {
	if c.Msg == nil {
		return ""
	}
	return c.Msg.Token
}

// Name 返回当前消息的业务消息名（Protobuf 消息名）。
func (c *HandlerContext[A]) Name() string {
	if c.Msg == nil {
		return ""
	}
	return c.Msg.Name
}

// ClientWriter 具备向客户端写消息能力的 actor（如 *actor.Player 持有连接）。
type ClientWriter interface {
	WriteMessage(data []byte) error
}

// Push 服务端主动向当前玩家客户端推送消息（MsgTypePush）。
// 推送帧的消息名由 msg 的类型反射得到，无需命令号。
// 只能在 actor 串行循环内调用（controller handler 满足），写入无锁竞争；
// actor 不具备写连接能力时返回错误。
func (c *HandlerContext[A]) Push(msg proto.Message) error {
	name := MessageName(msg)
	if name == "" {
		return fmt.Errorf("push message name empty: %T", msg)
	}
	body, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("push marshal: %w", err)
	}
	out, err := protocol.EncodeMsg(&protocol.Msg{
		MsgType: protocol.MsgTypePush,
		Name:    name,
		UID:     c.UID(),
		Body:    body,
	})
	if err != nil {
		return fmt.Errorf("push encode: %w", err)
	}
	w, ok := any(c.Actor).(ClientWriter)
	if !ok {
		return errors.New("actor does not support client push")
	}
	return w.WriteMessage(out)
}

// MessageName 返回 Protobuf 消息名（如 LoginReq），即路由键与信封 Name 的取值。
// 本仓库 proto 生成的 Go 类型名与消息名一致；nil 消息返回空串。
func MessageName(m proto.Message) string {
	if m == nil {
		return ""
	}
	return reflect.TypeOf(m).Elem().Name()
}

// unmarshalError 标记 body 反序列化失败，供 Route 区分 BadRequest / ServerError。
type unmarshalError struct{ err error }

func (e *unmarshalError) Error() string { return e.err.Error() }
func (e *unmarshalError) Unwrap() error { return e.err }

// contextMismatchError 表示 actor 上下文类型与注册不符（框架内部错误，不下发响应）。
type contextMismatchError struct{ err error }

func (e *contextMismatchError) Error() string { return e.err.Error() }
func (e *contextMismatchError) Unwrap() error { return e.err }

// dispatchFunc 路由注册项：负责断言 actor 上下文、反序列化并调用具体 handler。
type dispatchFunc func(ctx any, msg *protocol.Msg) (proto.Message, error)

type routeEntry struct {
	name   string // 业务消息名（Protobuf 消息名），即注册键
	handle dispatchFunc
}

var routes = map[string]routeEntry{}

// Register 泛型注册入口：A 为 actor 具体类型（如 *actor.Player / *actor.Room），
// T 为请求消息类型。路由键由 T 反射得到（Protobuf 消息名），无需命令号：
//
//	route.Register[*actor.Player, *pt.LoginReq](LoginController)
//
// handler 收到类型化的 HandlerContext[A] 与已反序列化的 T，返回响应消息；
// 响应消息名由返回值类型反射得到，因此不维护请求→响应配对表。
// 消息名重复注册会直接 panic（跨 proto 包同名消息在注册期暴露）。
func Register[A any, T proto.Message](h func(ctx *HandlerContext[A], req T) (proto.Message, error)) {
	var zero T
	typ := reflect.TypeOf(zero).Elem()
	name := typ.Name()
	if name == "" {
		panic(fmt.Sprintf("empty message name: %T", zero))
	}
	if _, ok := routes[name]; ok {
		panic(fmt.Sprintf("duplicate route name: %s", name))
	}
	routes[name] = routeEntry{
		name: name,
		handle: func(ctx any, msg *protocol.Msg) (proto.Message, error) {
			hctx, ok := ctx.(*HandlerContext[A])
			if !ok {
				return nil, &contextMismatchError{
					err: fmt.Errorf("actor context mismatch: want %T got %T",
						(*HandlerContext[A])(nil), ctx)}
			}
			req := reflect.New(typ).Interface().(T)
			if err := proto.Unmarshal(msg.Body, req); err != nil {
				return nil, &unmarshalError{err: err}
			}
			return h(hctx, req)
		},
	}
}

// Route 按消息信封的 Name 分发，返回组装好的响应信封。
// 成功：MsgType=Rsp、Name=响应消息名（handler 返回值的类型反射得到）、MsgID/UID 原样回填；
// handler 出错 / body 反序列化失败 / panic：记日志并返回 Name=ErrorRes + pt.ErrorRes 响应；
// nil msg / 未知消息名 / actor 类型不匹配：返回 error，不下发响应。
func Route(ctx any, msg *protocol.Msg) (rsp *protocol.Msg, err error) {
	defer func() {
		if r := recover(); r != nil {
			logger.ERROR("HandleRequest panic:", r)
			rsp = errorRsp(msg, protocol.CodeServerError, fmt.Sprintf("handler panic: %v", r))
			err = nil
		}
	}()
	if msg == nil {
		return nil, errors.New("nil message")
	}
	entry, ok := routes[msg.Name]
	if !ok {
		return nil, fmt.Errorf("not found handler: %s", msg.Name)
	}
	res, err := entry.handle(ctx, msg)
	if err != nil {
		var ce *contextMismatchError
		if errors.As(err, &ce) {
			return nil, err
		}
		logger.ERROR("handle error:", msg.Name, err)
		var ue *unmarshalError
		if errors.As(err, &ue) {
			return errorRsp(msg, protocol.CodeBadRequest, ue.Error()), nil
		}
		return errorRsp(msg, protocol.CodeServerError, err.Error()), nil
	}
	if res == nil {
		return nil, nil
	}
	body, err := proto.Marshal(res)
	if err != nil {
		logger.ERROR("marshal response error:", msg.Name, err)
		return errorRsp(msg, protocol.CodeServerError, err.Error()), nil
	}
	resName := MessageName(res)
	if resName == "" {
		logger.ERROR("response message name empty:", fmt.Sprintf("%T", res), "req:", msg.Name)
		return errorRsp(msg, protocol.CodeServerError, "response message name empty"), nil
	}
	return &protocol.Msg{
		MsgType: protocol.MsgTypeRsp,
		MsgID:   msg.MsgID,
		UID:     msg.UID,
		Name:    resName,
		Body:    body,
	}, nil
}

// errorRsp 组装统一错误响应：Name=ErrorRes（由 pt.ErrorRes 类型反射得到），body=pt.ErrorRes。
func errorRsp(req *protocol.Msg, code int32, msgStr string) *protocol.Msg {
	body, err := proto.Marshal(&pt.ErrorRes{Code: code, Msg: msgStr})
	if err != nil {
		logger.ERROR("marshal error response failed:", err)
		return nil
	}
	rsp := &protocol.Msg{
		MsgType: protocol.MsgTypeRsp,
		Name:    MessageName(&pt.ErrorRes{}),
		Body:    body,
	}
	if req != nil {
		rsp.MsgID = req.MsgID
		rsp.UID = req.UID
	}
	return rsp
}
