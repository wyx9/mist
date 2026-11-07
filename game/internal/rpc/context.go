package rpc

import "time"

// Comm 跨 actor 通信能力（由 core.ActorContext 实现）：只能走消息，禁止直接改其他 actor 状态。
type Comm interface {
	Cast(to int64, msg interface{}) error
	Call(to int64, msg interface{}, timeout time.Duration) (interface{}, error)
}

// HandlerContext 类型化内部消息 handler 上下文。
type HandlerContext[A any] struct {
	Actor A
	Comm  Comm
	from  string
}

// From 返回当前消息的来源 actor uid（远端调用时为对端来源）。
func (c *HandlerContext[A]) From() string { return c.from }
