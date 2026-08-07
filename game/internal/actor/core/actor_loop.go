package core

import (
	"context"
	"fmt"

	"simple_game/game/pkg/logger"
)

// loop Actor 的主循环：处理消息，循环退出时执行 Stop 清理并通知关闭。
// request 通道永不关闭：投递方通过监听 stop 通道避免与关闭竞态。
func (ac *ActorBase) loop() {
	// 调用 Actor 的启动函数
	ac.iActor.Start()

	go func() {
		defer func() {
			ac.iActor.Stop() // 停止 Actor（落盘兜底）
			ac.setState(StateClosed)
			close(ac.stop) // 通知 Shutdown 等待方
		}()

		for {
			select {
			case <-ac.ctx.Done(): // 上下文取消时退出
				logger.INFO("Actor context canceled, name:", ac.name)
				return

			case raw := <-ac.request: // 处理消息信封
				env, ok := raw.(*Envelope)
				if !ok {
					logger.ERROR("unexpected message type in actor queue:", raw)
					continue
				}
				ac.handleEnvelope(env)
			}
		}
	}()
}

// handleEnvelope 处理一条消息信封。
func (ac *ActorBase) handleEnvelope(env *Envelope) {
	ctx := &ActorContext{
		manager: ac.manager,
		self:    ac.name,
		env:     env,
	}

	handleCtx, cancel := context.WithTimeout(ac.ctx, HandlerTimeout)
	defer cancel()
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				logger.ERROR("handler panic:", r, "actor:", ac.name)
				replyCallError(ctx, fmt.Sprintf("handler panic: %v", r))
			}
		}()
		ac.dispatch(env, ctx)
		// RPC 兜底：handler 未显式回复时自动回 nil，避免调用方永久挂起
		if env.Kind == MsgCall && ctx.replied.CompareAndSwap(false, true) {
			env.Reply <- nil
		}
	}()

	select {
	case <-done:
	case <-handleCtx.Done():
		logger.ERROR("message handling timed out for actor:", ac.name)
		replyCallError(ctx, "handler timeout")
		// 超时视为 actor 故障：先置关闭阻止新投递，自停循环，并通知 gameserver 踢掉该玩家连接
		ac.setState(StateClosed)
		ac.cancel()
		if ac.manager != nil {
			ac.manager.notifyFault(ac.name)
		}
	}
}

// dispatch 按消息类型分发：
//   - MsgClient：走 StartActor 注入的 clientHandler（默认 HandleClientMsg）；
//   - MsgSend / MsgCall：走可选的 MessageHandler 接口，未实现时异步消息丢弃、RPC 兜底回 nil。
func (ac *ActorBase) dispatch(env *Envelope, ctx *ActorContext) {
	switch env.Kind {
	case MsgClient:
		if ac.clientHandler != nil {
			ac.clientHandler(ctx, env.Payload)
		} else {
			logger.WARNING("actor has no client handler, drop message:", ac.name)
		}
	case MsgSend:
		if h, ok := ac.iActor.(MessageHandler); ok {
			h.HandleMessage(ctx, env.Payload)
		} else {
			logger.WARNING("actor does not handle internal messages, drop:", ac.name)
		}
	case MsgCall:
		if h, ok := ac.iActor.(MessageHandler); ok {
			h.HandleMessage(ctx, env.Payload)
		}
		// 未实现 MessageHandler 时保持静默，由调用方 RPC 兜底回复 nil。
	}
}

// replyCallError 若 RPC 尚未回复，则以 CallError 兜底回复。
func replyCallError(ctx *ActorContext, msg string) {
	if ctx.env.Kind == MsgCall && ctx.replied.CompareAndSwap(false, true) {
		ctx.env.Reply <- &CallError{Err: msg}
	}
}
