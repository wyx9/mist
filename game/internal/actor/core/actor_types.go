package core

import (
	"errors"
	"time"

	"simple_game/game/pkg/timer"
)

// IActor Actor 基础接口：生命周期与类别标识。
type IActor interface {
	Start()           // Actor 启动时的初始化
	Stop()            // Actor 停止时的清理
	Category() string // Actor 类别标识: "player", "npc", "room" 等
}

// MessageHandler 可选的内部消息处理器：实现后可通过 Send（异步）与 Call（RPC）接收消息；
// 未实现时异步消息被丢弃，RPC 自动回复 nil。
type MessageHandler interface {
	HandleMessage(ctx *ActorContext, msg interface{})
}

// IdleTimeoutProvider 可选的空闲超时接口：具体 actor 自定义休眠/关闭超时；
// 返回 <=0 表示该 actor 永不因空闲自动关闭；未实现时使用包内默认值。
type IdleTimeoutProvider interface {
	IdleTimeout() time.Duration
}

// TimerHandle actor 内部定时器句柄（消息式）：到期后把 msg 投递给当前 actor 自己，
// 在 HandleMessage 中按消息类型处理；纯内存，进程重启即消失。
type TimerHandle interface {
	After(d time.Duration, msg interface{}) *timer.Task // 一次性
	Every(d time.Duration, msg interface{}) *timer.Task // 周期
	Cancel(task *timer.Task)                            // 取消
}

// SetTimerHandle 可选注入接口：StartActor 创建时注入绑定当前 actor 的定时器句柄。
type SetTimerHandle interface {
	SetTimerHandle(h TimerHandle)
}

// 定义错误常量
var (
	ErrActorNotFound = errors.New("actor not found")       // Actor 不存在
	ErrActorClose    = errors.New("actor close")           // Actor 已关闭
	ErrSendTimeout   = errors.New("send timeout")          // 发送超时
	ErrSelfCall      = errors.New("self call not allowed") // 禁止 Actor 自调用（防止死锁）
)

// ActorState Actor 生命周期状态。
type ActorState int32

const (
	StateRunning  ActorState = iota // 运行中：正常处理消息
	StateSleeping                   // 休眠：保留 actor 与数据，等待唤醒或超时关闭
	StateClosed                     // 已关闭：资源已释放
)

// MsgKind 消息类型。
type MsgKind int

const (
	MsgClient MsgKind = iota // 客户端消息（来自网络层）
	MsgSend                  // 内部异步投递
	MsgCall                  // 内部 RPC 调用
)

// Envelope 消息信封：携带来源与回包通道，统一在 actor 队列中流转。
type Envelope struct {
	From    string           // 来源：客户端消息为 "server"，内部消息为发起方 actor 名称
	Kind    MsgKind          // 消息类型
	Payload interface{}      // 消息内容
	Reply   chan interface{} // 仅 MsgCall 使用，RPC 回包通道
	System  bool             // 系统消息（PersistTick/连接生命周期）：不计入业务活动时间
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
