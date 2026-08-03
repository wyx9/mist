package actor

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"simple_game/game/pkg/logger"
)

// IActor 定义了Actor的基本接口
// Actor是一个独立的执行单元，负责处理特定类型的消息和任务
type IActor interface {
	Start()                                     // Actor启动时的初始化
	Stop()                                      // Actor停止时的清理
	Handler(ctx *ActorContext, msg interface{}) // 消息处理函数，通过 ctx 与其他 Actor 通信或回包
	Category() string                           // Actor类别标识: "player", "npc", "room" 等
}

// 定义错误常量
var (
	ErrActorNotFound = errors.New("actor not found")       // Actor不存在错误
	ErrActorClose    = errors.New("actor close")           // Actor已关闭错误
	ErrSendTimeout   = errors.New("send timeout")          // 发送超时错误
	ErrSelfCall      = errors.New("self call not allowed") // 禁止 Actor 自调用（防死锁）
)

// MsgKind 消息类型。
type MsgKind int

const (
	MsgClient MsgKind = iota // 客户端消息（来自网络层）
	MsgSend                  // 内部异步投递
	MsgCall                  // 内部 RPC 调用
)

// Envelope 消息信封：携带来源与回包渠道，统一在 actor 队列中流转。
type Envelope struct {
	From    string           // 来源：客户端消息为 "server"，内部消息为发起方 actor 名
	Kind    MsgKind          // 消息类型
	Payload interface{}      // 消息内容
	Reply   chan interface{} // 仅 MsgCall 使用，RPC 回包渠道
}

// CallError RPC 调用失败（handler panic / 处理超时）。
type CallError struct {
	Err string
}

func (e *CallError) Error() string { return "call error: " + e.Err }

// ActorContext 提供给 handler 的执行上下文，支持向其他 actor 发送消息与发起 RPC。
type ActorContext struct {
	manager *AManager
	self    string
	env     *Envelope
	replied atomic.Bool
}

// Name 返回当前 actor 名称。
func (c *ActorContext) Name() string { return c.self }

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

// Send 向其他 actor 异步投递消息。
func (c *ActorContext) Send(to string, msg interface{}) error {
	return c.manager.Send(to, c.self, msg)
}

// Call 向其他 actor 发起 RPC 并等待回复。
func (c *ActorContext) Call(to string, msg interface{}, timeout time.Duration) (interface{}, error) {
	return c.manager.Call(to, c.self, msg, timeout)
}

// Reply 显式回复当前消息；仅对 RPC（MsgCall）有效，且只允许回复一次。
func (c *ActorContext) Reply(result interface{}) {
	if c.env == nil || c.env.Kind != MsgCall || !c.replied.CompareAndSwap(false, true) {
		logger.WARNING("ActorContext.Reply ignored, actor:", c.self)
		return
	}
	c.env.Reply <- result
}

// ActorConfig Actor配置结构体，用于配置Actor的运行参数
type ActorConfig = struct {
	QueueSize      int           // 消息队列大小，决定可以缓存的消息数量
	SendTimeout    time.Duration // 发送超时时间，消息发送的最大等待时间
	HandlerTimeout time.Duration // 处理超时时间，消息处理的最大执行时间
}

// DefaultConfig 默认配置，当没有提供自定义配置时使用
var DefaultConfig = ActorConfig{
	QueueSize:      1024,            // 默认队列大小1024
	SendTimeout:    5 * time.Second, // 默认发送超时5秒
	HandlerTimeout: 5 * time.Second, // 默认处理超时5秒
}

// ActorBase Actor基础结构体，提供Actor的基本功能实现
type ActorBase struct {
	name    string             // Actor名称，用于标识不同的Actor实例
	iActor  IActor             // Actor接口实现，包含具体的业务逻辑
	manager *AManager          // Actor管理器引用，用于内部 RPC 路由
	request chan interface{}   // 请求消息通道，用于接收待处理的消息
	stop    chan struct{}      // 停止信号通道，用于通知Actor停止运行
	config  ActorConfig        // Actor配置，包含运行参数
	closed  int32              // 关闭状态标志(原子操作)，用于安全地标记Actor状态
	ctx     context.Context    // 上下文，用于控制Actor的生命周期
	cancel  context.CancelFunc // 取消函数，用于取消上下文
}

// isClosed 检查Actor是否已关闭
func (ac *ActorBase) isClosed() bool {
	return atomic.LoadInt32(&ac.closed) == 1
}

// setClose 设置Actor为关闭状态
func (ac *ActorBase) setClose() {
	atomic.StoreInt32(&ac.closed, 1)
}

// loop Actor的主循环，处理消息
func (ac *ActorBase) loop() {
	// 调用Actor的启动函数
	ac.iActor.Start()

	go func() {
		defer func() {
			ac.iActor.Stop()  // 停止Actor
			ac.setClose()     // 设置关闭状态
			close(ac.request) // 关闭请求通道
			close(ac.stop)    // 关闭停止通道
		}()

		for {
			select {
			case <-ac.ctx.Done(): // 上下文取消时退出
				logger.INFO("Actor context canceled , name:", ac.name)
				return

			case raw, ok := <-ac.request: // 处理消息信封
				if !ok {
					return
				}
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

	handleCtx, cancel := context.WithTimeout(ac.ctx, ac.config.HandlerTimeout)
	defer cancel()
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				logger.ERROR("handler panic:", r, "actor:", ac.name)
				replyCallError(env, ctx, fmt.Sprintf("handler panic: %v", r))
			}
		}()
		ac.iActor.Handler(ctx, env.Payload)
		// RPC 兜底：handler 未显式回复时自动回 nil，避免调用方永久挂起
		if env.Kind == MsgCall && ctx.replied.CompareAndSwap(false, true) {
			env.Reply <- nil
		}
	}()

	select {
	case <-done:
	case <-handleCtx.Done():
		logger.ERROR("message handling timed out for actor ,", ac.name)
		replyCallError(env, ctx, "handler timeout")
	}
}

// replyCallError 若 RPC 尚未回复，则以 CallError 兜底回复。
func replyCallError(env *Envelope, ctx *ActorContext, msg string) {
	if env.Kind == MsgCall && ctx.replied.CompareAndSwap(false, true) {
		env.Reply <- &CallError{Err: msg}
	}
}

// Shutdown 优雅关闭Actor
func (ac *ActorBase) Shutdown(timeout time.Duration) error {
	if ac.isClosed() {
		return ErrActorClose
	}
	ac.cancel()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-timer.C:
		return ErrSendTimeout
	case <-ac.stop:
		return nil
	}
}

// NewActorBase 创建新的Actor实例
func NewActorBase(name string, callBack IActor, config *ActorConfig) *ActorBase {
	if config == nil {
		config = &DefaultConfig
	}

	ctx, cancel := context.WithCancel(context.Background())
	ab := &ActorBase{
		name:    name,
		iActor:  callBack,
		request: make(chan interface{}, config.QueueSize),
		stop:    make(chan struct{}),
		ctx:     ctx,
		cancel:  cancel,
		config:  *config,
	}

	ab.loop() // 启动Actor的主循环
	return ab
}
