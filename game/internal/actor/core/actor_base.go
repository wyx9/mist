package core

import (
	"context"
	"sync/atomic"
)

// ActorBase Actor基础结构体，提供Actor的基本功能实现
type ActorBase struct {
	name          string                                   // Actor名称，用于标识不同的Actor实例
	id            int64                                    // 全局唯一 uid（雪花/账号 id），由 StartActor 创建时统一赋值
	iActor        IActor                                   // Actor接口实现，包含具体的业务逻辑
	clientHandler func(ctx *ActorContext, msg interface{}) // 客户端消息分发：StartActor 注入，默认走 HandleClientMsg
	manager       *AManager                                // Actor管理器引用，用于内部 RPC 路由
	request       chan interface{}                         // 请求消息通道，用于接收待处理的消息
	stop          chan struct{}                            // 停止信号通道，用于通知Actor停止运行
	config        ActorConfig                              // Actor配置，包含运行参数
	closed        int32                                    // 关闭状态标志(原子操作)，用于安全地标记Actor状态
	ctx           context.Context                          // 上下文，用于控制Actor的生命周期
	cancel        context.CancelFunc                       // 取消函数，用于取消上下文
}

// ID 返回 Actor 全局唯一 uid。
func (ac *ActorBase) ID() int64 {
	return atomic.LoadInt64(&ac.id)
}

// SetID 设置 Actor uid（仅创建阶段调用，须在注册进管理器之前完成）。
func (ac *ActorBase) SetID(id int64) {
	atomic.StoreInt64(&ac.id, id)
}

// Actor 返回具体 actor 实例，供框架层做接口断言（如 Persistable）。
func (ac *ActorBase) Actor() IActor {
	return ac.iActor
}

// isClosed 检查Actor是否已关闭
func (ac *ActorBase) isClosed() bool {
	return atomic.LoadInt32(&ac.closed) == 1
}

// setClose 设置Actor为关闭状态
func (ac *ActorBase) setClose() {
	atomic.StoreInt32(&ac.closed, 1)
}
