package core

import (
	"context"
	"strconv"
	"sync/atomic"
	"time"
)

const (
	QueueSize          = 1024            // 默认消息队列大小
	SendTimeout        = 5 * time.Second // 消息投递超时
	HandlerTimeout     = 30 * time.Second
	DefaultIdleTimeout = 2 * time.Minute // 未实现 IdleTimeoutProvider 时的默认空闲/休眠超时
	SweepInterval      = 1 * time.Second // 状态机 sweeper 扫描周期
	CloseTimeout       = 5 * time.Second // 空闲超时关闭 actor 的等待上限
)

// ActorBase Actor 基础结构体，提供生命周期状态机与消息队列能力。
type ActorBase struct {
	name          string                                   // Actor 注册键 = uid 的十进制字符串
	id            int64                                    // 全局唯一 uid，由创建时统一赋值
	iActor        IActor                                   // Actor 接口实现，包含具体的业务逻辑
	clientHandler func(ctx *ActorContext, msg interface{}) // 客户端消息分发：创建时注入，默认走 HandleClientMsg
	manager       *AManager                                // Actor 管理器引用，用于内部 RPC 路由
	request       chan interface{}                         // 请求消息通道，用于接收待处理的消息
	stop          chan struct{}                            // 停止信号通道：循环退出时关闭，投递方监听避免竞态
	state         atomic.Int32                             // ActorState 生命周期状态
	lastActiveAt  atomic.Int64                             // 最近业务活动时间（unix nano），系统消息不刷新
	sleepAt       atomic.Int64                             // 进入休眠的时间（unix nano），用于超时关闭判定
	attached      atomic.Bool                              // 是否持有连接（玩家在线；内部 actor 恒为 false）
	idleTimeout   time.Duration                            // 空闲/休眠超时，Add 时从 IdleTimeoutProvider 缓存；0 表示永不自动关闭
	ctx           context.Context                          // 上下文，用于控制 Actor 的生命周期
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

// wake 唤醒休眠 actor 为运行（EnsureRunning 复用休眠 actor 时调用）。
func (ac *ActorBase) wake() {
	for {
		if ac.state.Load() != int32(StateSleeping) {
			return
		}
		if ac.state.CompareAndSwap(int32(StateSleeping), int32(StateRunning)) {
			ac.sleepAt.Store(0)
			return
		}
	}
}

// Actor 返回具体 actor 实例，供框架层做接口断言（如 Persistable）。
func (ac *ActorBase) Actor() IActor {
	return ac.iActor
}

// State 返回 Actor 当前生命周期状态。
func (ac *ActorBase) State() ActorState {
	return ActorState(ac.state.Load())
}

// Attached 返回 Actor 是否持有连接。
func (ac *ActorBase) Attached() bool {
	return ac.attached.Load()
}

// isClosed 检查 Actor 是否已关闭。
func (ac *ActorBase) isClosed() bool {
	return ac.state.Load() == int32(StateClosed)
}

// setState 设置生命周期状态。
func (ac *ActorBase) setState(s ActorState) {
	ac.state.Store(int32(s))
}

// touchActivity 刷新业务活动时间；若处于休眠则唤醒为运行（仅非系统消息投递时调用）。
func (ac *ActorBase) touchActivity() {
	ac.lastActiveAt.Store(time.Now().UnixNano())
	for {
		if ac.state.Load() != int32(StateSleeping) {
			return
		}
		if ac.state.CompareAndSwap(int32(StateSleeping), int32(StateRunning)) {
			ac.sleepAt.Store(0)
			return
		}
	}
}

// markSleeping 由 sweeper 调用：运行中空闲超时进入休眠并记录休眠起始时间。
func (ac *ActorBase) markSleeping() {
	if ac.state.CompareAndSwap(int32(StateRunning), int32(StateSleeping)) {
		ac.sleepAt.Store(time.Now().UnixNano())
	}
}

// markDetached 由 Detach 调用：连接断开，立即进入休眠并开始计时（重连宽限）。
// actor 已关闭时返回 false。
func (ac *ActorBase) markDetached() bool {
	if ac.state.Load() == int32(StateClosed) {
		return false
	}
	ac.attached.Store(false)
	now := time.Now().UnixNano()
	ac.lastActiveAt.Store(now)
	ac.sleepAt.Store(now)
	ac.setState(StateSleeping)
	return true
}

// markAttached 由 Attach 调用：重连唤醒为运行并重置计时；actor 已关闭时返回 false。
func (ac *ActorBase) markAttached() bool {
	for {
		switch ac.state.Load() {
		case int32(StateRunning):
			ac.attached.Store(true)
			ac.lastActiveAt.Store(time.Now().UnixNano())
			ac.sleepAt.Store(0)
			return true
		case int32(StateSleeping):
			if ac.state.CompareAndSwap(int32(StateSleeping), int32(StateRunning)) {
				ac.attached.Store(true)
				ac.lastActiveAt.Store(time.Now().UnixNano())
				ac.sleepAt.Store(0)
				return true
			}
		default:
			return false
		}
	}
}

// uidKey 将 uid 转为 actor 注册键（十进制字符串）。
func uidKey(uid int64) string { return strconv.FormatInt(uid, 10) }

// NewActorBase 创建新的 Actor 实例并启动主循环。uid 为全局唯一身份，注册键为其十进制字符串。
func NewActorBase(uid int64, callBack IActor) *ActorBase {
	ctx, cancel := context.WithCancel(context.Background())
	ab := &ActorBase{
		name:    uidKey(uid),
		id:      uid,
		iActor:  callBack,
		request: make(chan interface{}, QueueSize),
		stop:    make(chan struct{}),
		ctx:     ctx,
		cancel:  cancel,
	}

	ab.loop() // 启动 Actor 的主循环
	return ab
}

// Shutdown 优雅关闭 Actor：先置关闭状态阻止新投递，再取消上下文并等待循环退出。
func (ac *ActorBase) Shutdown(timeout time.Duration) error {
	if ac.isClosed() {
		return ErrActorClose
	}
	ac.setState(StateClosed)
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
