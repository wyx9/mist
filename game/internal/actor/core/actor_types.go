package core

import "errors"

// IActor 定义 Actor 的基本接口。
// 客户端消息（MsgClient）由框架统一通过 HandleClientMsg 分发，无需在此声明；
// 需要接收内部异步消息/RPC 的 actor 实现可选的 MessageHandler 接口。
type IActor interface {
	Start()           // Actor启动时的初始化
	Stop()            // Actor停止时的清理
	Category() string // Actor类别标识: "player", "npc", "room" 等
}

// MessageHandler 可选的内部消息处理器：实现了它的 actor 可以接收
// MsgSend（异步内部消息）与 MsgCall（RPC）；未实现时异步消息被丢弃、
// RPC 自动回复 nil。
type MessageHandler interface {
	HandleMessage(ctx *ActorContext, msg interface{})
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
