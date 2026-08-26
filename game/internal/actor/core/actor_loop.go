package core

import (
	"fmt"
	"time"

	"simple_game/game/internal/rpc"
	"simple_game/game/pkg/logger"
)

// loop Actor 的主循环：单协程从队列取消息并同步处理，保证 actor 内严格串行
// （对齐 skynet service 的单 worker 串行处理模型，不每消息开 goroutine）。
// request 通道永不关闭：投递方通过监听 stop 通道避免与关闭竞态。
func (ac *ActorBase) loop() {
	// 调用 Actor 的启动函数
	ac.iActor.Start()
	go func() {
		defer func() {
			ac.iActor.Stop()
			ac.setState(StateClosed)
			close(ac.stop)
		}()
		for {
			select {
			case <-ac.ctx.Done(): // 上下文取消时退出
				logger.INFO("Actor context canceled, name:", ac.name)
				return

			case raw := <-ac.request: // 处理消息信封（同步串行）
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

// handleEnvelope 同步处理一条消息信封（单协程串行，无每消息 goroutine）。
// handler panic 由 recoverHandler 隔离，不影响其他 actor；
// RPC（MsgCall）未显式回复时兜底回 nil，避免调用方永久挂起。
// handler 超时由看门狗（time.AfterFunc → onHandlerTimeout）兜底：置关闭、取消循环、
// 通知 gameserver 踢线；单协程无法协作取消正在执行的 handler，待其返回后循环自然退出。
func (ac *ActorBase) handleEnvelope(env *Envelope) {
	ctx := &ActorContext{
		manager: ac.manager,
		self:    ac.name,
		uid:     ac.id,
		env:     env,
	}

	// 看门狗：HandlerTimeout 内未处理完视为 actor 故障（踢线 + 关闭）
	watchdog := time.AfterFunc(HandlerTimeout, func() { ac.onHandlerTimeout(ctx) })
	defer watchdog.Stop()

	defer ac.recoverHandler(ctx)
	ac.dispatch(env, ctx)
	// RPC 兜底：handler 未显式回复时自动回 nil，避免调用方永久挂起
	if env.Kind == MsgCall && ctx.replied.CompareAndSwap(false, true) {
		env.Reply <- nil
	}
}

// onHandlerTimeout handler 处理超时：回复超时错误、置关闭阻止新投递、取消主循环，
// 并通知 gameserver 踢掉该玩家连接（由看门狗回调触发）。
func (ac *ActorBase) onHandlerTimeout(ctx *ActorContext) {
	logger.ERROR("message handling timed out for actor:", ac.name)
	replyCallError(ctx, "handler timeout")
	ac.setState(StateClosed)
	ac.cancel()
	if ac.manager != nil {
		ac.manager.notifyFault(ac.name)
	}
}

// recoverHandler handler panic 隔离：记录日志并兜底回复调用方，不影响其他 actor。
func (ac *ActorBase) recoverHandler(ctx *ActorContext) {
	if r := recover(); r != nil {
		logger.ERROR("handler panic:", r, "actor:", ac.name)
		replyCallError(ctx, fmt.Sprintf("handler panic: %v", r))
	}
}

// dispatch 按消息类型分发：
//   - MsgClient：走创建时注入的 clientHandler（默认 HandleClientMsg）；
//   - MsgSend / MsgCall：优先走内部消息注册表（rpc.Dispatch，业务消息在 controller 层注册），
//     未注册时回退到 actor 的 MessageHandler（框架生命周期消息 PersistTick/ConnDetached 等）。
func (ac *ActorBase) dispatch(env *Envelope, ctx *ActorContext) {
	switch env.Kind {
	case MsgClient:
		if ac.clientHandler != nil {
			ac.clientHandler(ctx, env.Payload)
		} else {
			logger.WARNING("actor has no client handler, drop message:", ac.name)
		}
	case MsgSend:
		handled, _, herr := rpc.Dispatch(ac.iActor, ctx, env.From, env.Payload)
		if handled {
			if herr != nil {
				logger.ERROR("internal handler error:", herr, "actor:", ac.name)
			}
			return
		}
		if h, ok := ac.iActor.(MessageHandler); ok {
			h.HandleMessage(ctx, env.Payload)
		} else {
			logger.WARNING("actor does not handle internal messages, drop:", ac.name)
		}
	case MsgCall:
		handled, result, herr := rpc.Dispatch(ac.iActor, ctx, env.From, env.Payload)
		if handled {
			if herr != nil {
				ctx.Reply(&CallError{Err: herr.Error()})
			} else {
				ctx.Reply(result)
			}
			return
		}
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
