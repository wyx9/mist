package core

import (
	"sync/atomic"
	"time"

	"simple_game/game/pkg/logger"
)

// ActorContext 提供给 handler 的执行上下文，支持向其他 actor 发送消息与发起 RPC。
type ActorContext struct {
	manager *AManager
	uid     int64 // 当前 actor 的 uid
	env     *Envelope
	replied atomic.Bool
}

// SelfUID 返回当前 actor 的 uid。
func (c *ActorContext) SelfUID() int64 { return c.uid }

// From 返回当前消息来源。
func (c *ActorContext) From() string {
	if c.env == nil {
		return ""
	}
	return c.env.From
}

// Kind 返回当前消息类型。
func (c *ActorContext) Kind() MsgKind {
	if c.env == nil {
		return MsgClient
	}
	return c.env.Kind
}

// Cast 向其他 actor 异步投递消息。
func (c *ActorContext) Cast(to int64, msg interface{}) error {
	return c.manager.Cast(to, uidKey(c.uid), msg)
}

// Call 向其他 actor 发起 RPC 并等待回复。禁止自调用（会死锁）。
func (c *ActorContext) Call(to int64, msg interface{}, timeout time.Duration) (interface{}, error) {
	if to == c.uid {
		return nil, ErrSelfCall
	}
	return c.manager.Call(to, uidKey(c.uid), msg, timeout)
}

// Reply 显式回复当前消息；仅对 RPC（MsgCall）有效，且只允许回复一次。
func (c *ActorContext) Reply(result interface{}) {
	if c.env == nil || c.env.Kind != MsgCall || !c.replied.CompareAndSwap(false, true) {
		logger.WARNING("ActorContext.Reply ignored, actor:", c.uid)
		return
	}
	c.env.Reply <- result
}
