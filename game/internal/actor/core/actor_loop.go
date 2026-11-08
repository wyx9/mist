package core

import (
	"fmt"
	"time"

	"simple_game/game/internal/rpc"
	"simple_game/game/pkg/logger"
)

// start 执行启动初始化：先加载持久化数据，再调用 Start；任一步失败或 panic
// 都终止启动并进入关闭态（对齐 skynet service bootstrap 失败即退出）。
func (ac *ActorBase) start() (err error) {
	defer func() {
		if r := recover(); r != nil {
			logger.ERROR("actor start panic:", r, "uid:", ac.id)
			err = fmt.Errorf("actor start panic: %v", r)
			ac.setState(StateClosed)
			close(ac.stop)
		}
	}()
	if err := ac.loadData(); err != nil {
		ac.setState(StateClosed)
		close(ac.stop)
		return err
	}
	if err := ac.iActor.Start(); err != nil {
		ac.setState(StateClosed)
		close(ac.stop)
		return err
	}
	go ac.runLoop()
	return nil
}

// runLoop Actor 主循环：单协程串行处理消息（对齐 skynet service 单 worker 模型）。
// request 通道永不关闭，投递方监听 stop 通道避免关闭竞态。
func (ac *ActorBase) runLoop() {
	defer func() {
		ac.saveNow()
		ac.iActor.Stop()
		ac.setState(StateClosed)
		close(ac.stop)
	}()
	for {
		select {
		case <-ac.ctx.Done(): // 上下文取消时退出
			logger.INFO("Actor context canceled, uid:", ac.id)
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
}

// handleEnvelope 串行处理一条消息；handler 超时由看门狗兜底（置关闭、踢线），
// panic 由 recoverHandler 隔离；MsgCall 未回复时兜底回 nil，避免调用方挂起。
func (ac *ActorBase) handleEnvelope(env *Envelope) {
	ctx := &ActorContext{
		manager: ac.manager,
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
	logger.ERROR("message handling timed out for actor:", ac.id)
	replyCallError(ctx, "handler timeout")
	ac.setState(StateClosed)
	ac.cancel()
	if ac.manager != nil {
		ac.manager.notifyFault(ac.id)
	}
}

// recoverHandler handler panic 隔离：记录日志并兜底回复调用方，不影响其他 actor。
func (ac *ActorBase) recoverHandler(ctx *ActorContext) {
	if r := recover(); r != nil {
		logger.ERROR("handler panic:", r, "actor:", ac.id)
		replyCallError(ctx, fmt.Sprintf("handler panic: %v", r))
	}
}

// dispatch 按消息类型分发：MsgClient 走 clientHandler；PersistSave 由框架直接落盘；
// 其余 MsgSend/MsgCall 优先走 rpc 注册表，未注册时回退到 actor 的 HandleMessage。
func (ac *ActorBase) dispatch(env *Envelope, ctx *ActorContext) {
	switch env.Kind {
	case MsgClient:
		if ac.clientHandler != nil {
			ac.clientHandler(ctx, env.Payload)
		} else {
			logger.WARNING("actor has no client handler, drop message:", ac.id)
		}
	case MsgSend:
		// 落地消息提前处理
		if _, ok := env.Payload.(PersistSave); ok {
			ac.saveNow()
			return
		}
		handled, _, herr := rpc.Dispatch(ac.iActor, ctx, env.From, env.Payload)
		if handled {
			if herr != nil {
				logger.ERROR("internal handler error:", herr, "actor:", ac.id)
			}
			return
		}
		if h, ok := ac.iActor.(MessageHandler); ok {
			h.HandleMessage(ctx, env.Payload)
		} else {
			logger.WARNING("actor does not handle internal messages, drop:", ac.id)
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
