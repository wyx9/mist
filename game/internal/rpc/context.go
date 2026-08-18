package rpc

import "time"

// Comm 跨 actor 通信能力，由 core.ActorContext 实现。
// handler 与其它 actor 通信只能走 Cast/Call（目标为 uid），禁止直接操作其内部状态。
type Comm interface {
	Cast(to int64, msg interface{}) error
	Call(to int64, msg interface{}, timeout time.Duration) (interface{}, error)
}

// HandlerContext 类型化内部消息 handler 上下文：
// Actor 为当前 actor 对象/数据（如 *actor.Room），Comm 提供跨 actor 通信。
type HandlerContext[A any] struct {
	Actor A
	Comm  Comm
	from  string
}

// From 返回当前内部消息的来源 actor 名（远端调用时为对端节点的来源 actor 名）。
func (c *HandlerContext[A]) From() string { return c.from }
