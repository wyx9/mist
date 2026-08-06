package core

import (
	"context"
	"time"
)

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
