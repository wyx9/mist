package core

import (
	"errors"
	"time"

	"simple_game/game/pkg/actortype"
	"simple_game/game/pkg/timer"
)

// IActor Actor 基础接口：生命周期与类型标识。
type IActor interface {
	Start()                       // Actor 启动时的初始化
	Stop()                        // Actor 停止时的清理
	Type() actortype.ActorType    // Actor 类型标识: "player", "room" 等
}

// ActorFactory 以 uid 创建某类 actor（展示名等由 Start 加载或调用方后续补齐）。
type ActorFactory func(uid int64) (IActor, error)

// MessageHandler 可选的内部消息处理器；未实现时异步消息丢弃，RPC 回 nil。
type MessageHandler interface {
	HandleMessage(ctx *ActorContext, msg interface{})
}

// IdleTimeoutProvider 自定义空闲/休眠超时；<=0 表示永不自动关闭。
type IdleTimeoutProvider interface {
	IdleTimeout() time.Duration
}

// TimerHandle actor 内部定时器（消息式，纯内存）。
type TimerHandle interface {
	After(d time.Duration, msg interface{}) *timer.Task
	Every(d time.Duration, msg interface{}) *timer.Task
	Cancel(task *timer.Task)
}

// SetTimerHandle 可选注入接口：创建时注入绑定当前 actor 的定时器句柄。
type SetTimerHandle interface {
	SetTimerHandle(h TimerHandle)
}

// Routable 标记接口：实现即可全局路由（创建/关闭时登记位置表）；Player 实现，Room 不实现。
type Routable interface {
	Routable()
}

var (
	ErrActorNotFound = errors.New("actor not found")
	ErrActorClose    = errors.New("actor close")
	ErrSendTimeout   = errors.New("send timeout")
	ErrSelfCall      = errors.New("self call not allowed")        // 禁止自调用（会死锁）
	ErrActorNotOwner = errors.New("actor not owned by this node") // 位置已被他节点占有
)

// ActorState Actor 生命周期状态。
type ActorState int32

const (
	StateRunning  ActorState = iota
	StateSleeping
	StateClosed
)

// MsgKind 消息类型。
type MsgKind int

const (
	MsgClient MsgKind = iota // 客户端消息
	MsgSend                  // 内部异步投递
	MsgCall                  // 内部 RPC 调用
)

// Envelope 消息信封：来源 + 内容 + RPC 回包通道。
type Envelope struct {
	From    string           // 客户端消息为 "server"，内部消息为发起方 actor uid
	Kind    MsgKind
	Payload interface{}
	Reply   chan interface{} // 仅 MsgCall 使用
	System  bool             // 系统消息不计入活动时间
}

// CallError RPC 调用失败（handler panic / 处理超时）。
type CallError struct {
	Err string
}

func (e *CallError) Error() string { return "call error: " + e.Err }

// ConnDetached 系统消息：连接断开通知，actor 在自身消息循环内清理连接引用。
type ConnDetached struct{}

// ConnAttached 系统消息：重连时注入新连接，actor 在自身消息循环内换绑连接。
type ConnAttached struct {
	Conn any
}
