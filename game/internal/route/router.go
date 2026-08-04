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

// ActorComm 跨 actor 通信能力，由 actor.ActorContext 实现。
// handler 与其它 actor 通信只能走 Send/Call，禁止直接操作其内部状态。
type ActorComm interface {
	Send(to string, msg interface{}) error
	Call(to string, msg interface{}, timeout time.Duration) (interface{}, error)
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

// Cmd 返回当前消息命令号。
func (c *HandlerContext[A]) Cmd() uint16 {
	if c.Msg == nil {
		return 0
	}
	return c.Msg.Cmd
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
	resCmd uint16
	name   string
	handle dispatchFunc
}

var routes = map[uint16]routeEntry{}

// Register 泛型注册入口：A 为 actor 具体类型（如 *actor.Player / *actor.Room），
// T 为请求消息类型；handler 收到类型化的 HandlerContext[A] 与已反序列化的 T。
// 响应命令号由 protocol.ReqRsp 绑定表决定；命令号重复注册会直接 panic。
func Register[A any, T proto.Message](reqCmd uint16, h func(ctx *HandlerContext[A], req T) (proto.Message, error)) {
	var zero T
	typ := reflect.TypeOf(zero).Elem()
	if _, ok := routes[reqCmd]; ok {
		panic(fmt.Sprintf("duplicate route cmd: %d", reqCmd))
	}
	routes[reqCmd] = routeEntry{
		resCmd: protocol.RspCmd(reqCmd),
		name:   typ.Name(),
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

// Route 按消息信封分发，返回组装好的响应信封。
// 成功：MsgType=Rsp、Cmd=resCmd、MsgID/UID 原样回填；
// handler 出错 / body 反序列化失败 / panic：记日志并返回 CmdError + pt.ErrorRes 响应；
// nil msg / 未知 cmd / actor 类型不匹配：返回 error，不下发响应。
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
	entry, ok := routes[msg.Cmd]
	if !ok {
		return nil, errors.New("not found handler")
	}
	res, err := entry.handle(ctx, msg)
	if err != nil {
		var ce *contextMismatchError
		if errors.As(err, &ce) {
			return nil, err
		}
		logger.ERROR("handle error:", msg.Cmd, err)
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
		logger.ERROR("marshal response error:", msg.Cmd, err)
		return errorRsp(msg, protocol.CodeServerError, err.Error()), nil
	}
	return &protocol.Msg{
		MsgType: protocol.MsgTypeRsp,
		MsgID:   msg.MsgID,
		UID:     msg.UID,
		Cmd:     entry.resCmd,
		Body:    body,
	}, nil
}

// errorRsp 组装统一错误响应：Cmd=CmdError，body=pt.ErrorRes。
func errorRsp(req *protocol.Msg, code int32, msgStr string) *protocol.Msg {
	body, _ := proto.Marshal(&pt.ErrorRes{Code: code, Msg: msgStr})
	rsp := &protocol.Msg{
		MsgType: protocol.MsgTypeRsp,
		Cmd:     protocol.CmdError,
		Body:    body,
	}
	if req != nil {
		rsp.MsgID = req.MsgID
		rsp.UID = req.UID
	}
	return rsp
}
