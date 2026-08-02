package engine

import (
	"context"
	"errors"
	"simple_game/game/pkg"
	"sync/atomic"
	"time"
)

// IActor 定义了Actor的基本接口
// Actor是一个独立的执行单元，负责处理特定类型的消息和任务
type IActor interface {
	Start()                              // Actor启动时的初始化
	Stop()                               // Actor停止时的清理
	Handler(msg interface{}) interface{} // 消息处理函数，处理接收到的消息并返回结果
}

// 定义错误常量
var (
	ErrActorNotFound = errors.New("actor not found") // Actor不存在错误
	ErrActorClose    = errors.New("actor close")     // Actor已关闭错误
	ErrSendTimeout   = errors.New("send timeout")    // 发送超时错误
)

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
				pkg.INFO("Actor context canceled , name:", ac.name)
				return

			case req, ok := <-ac.request: // 处理请求消息
				if !ok {
					return
				}

				// 创建处理超时上下文
				handleCtx, cancel := context.WithTimeout(ac.ctx, ac.config.HandlerTimeout)
				done := make(chan struct{})

				go func() {
					defer close(done)
					switch r := req.(type) {
					case CallRequest: // 处理同步调用请求
						result := ac.iActor.Handler(r.Msg)
						r.Response <- result
					default: // 处理异步消息
						ac.iActor.Handler(req)
					}
				}()

				// 等待处理完成或超时
				select {
				case <-done:
				case <-handleCtx.Done():
					pkg.ERROR("message handling timed out for actor ,", ac.name)
				}
				cancel()
			}
		}
	}()
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

	// 将Actor添加到管理器
	if err := ActorManner.Add(ab); err != nil {
		return nil
	}

	ab.loop() // 启动Actor的主循环
	return ab
}
