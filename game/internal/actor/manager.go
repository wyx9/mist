package engine

import (
	"sync"
	"time"
)

// AManager Actor管理器结构体，负责管理所有Actor实例
type AManager struct {
	actorMap  sync.Map // 存储所有Actor实例的映射表，key为Actor名称，value为Actor实例
	playerMap sync.Map // 存储所有玩家Actor的映射表，key为玩家IP，value为Actor实例
}

// ActorManner 全局Actor管理器实例
var ActorManner = AManager{
	actorMap:  sync.Map{},
	playerMap: sync.Map{},
}

// Add 添加新的Actor实例到管理器中
func (am *AManager) Add(i *ActorBase) error {
	am.actorMap.Store(i.name, i)
	// 如果是玩家Actor，则同时存储到playerMap中
	player, ok := i.iActor.(*Player)
	if ok {
		am.playerMap.Store(player.ip, i)
	}
	return nil
}

// Find 根据名称查找Actor实例
func (am *AManager) Find(name string) *ActorBase {
	value, ok := am.actorMap.Load(name)
	if !ok {
		return nil
	}
	actor, ok := value.(*ActorBase)
	if !ok {
		return nil
	}
	return actor
}

// FindAndClosePlayer 根据IP查找并关闭玩家Actor
func (am *AManager) FindAndClosePlayer(ip string) {
	value, ok := am.playerMap.Load(ip)
	if !ok {
		return
	}
	actor, ok := value.(*ActorBase)
	if !ok {
		return
	}
	_ = actor.Shutdown(5 * time.Second)
}

// CastMsg 向指定Actor发送异步消息
func (am *AManager) CastMsg(name string, msg interface{}) error {
	actor := am.Find(name)
	if actor == nil {
		return ErrActorNotFound
	}
	if actor.isClosed() {
		return ErrActorClose
	}

	timer := time.NewTimer(actor.config.SendTimeout)
	defer timer.Stop()
	select {
	case actor.request <- msg:
		return nil
	case <-timer.C:
		return ErrSendTimeout
	}
}

// CallRequest 同步调用请求结构体
type CallRequest struct {
	Msg      interface{}      // 请求消息
	Response chan interface{} // 响应通道，用于接收处理结果
}

// CallMsg 向指定Actor发送同步调用请求
func (am *AManager) CallMsg(name string, msg interface{}) (interface{}, error) {
	actor := am.Find(name)
	if actor == nil {
		return nil, ErrActorNotFound
	}
	if actor.isClosed() {
		return nil, ErrActorClose
	}

	req := &CallRequest{
		Msg:      msg,
		Response: make(chan interface{}, 1),
	}

	timer := time.NewTimer(actor.config.SendTimeout)
	defer timer.Stop()

	select {
	case actor.request <- req:
	case <-timer.C:
		return nil, ErrSendTimeout
	}

	select {
	case resp := <-req.Response:
		return resp, nil
	case <-timer.C:
		return nil, ErrSendTimeout
	}
}
