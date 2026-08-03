package actor

import (
	"fmt"
	"sync"
	"time"

	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/network"
)

// categoryIndex 单个类别的索引结构。
type categoryIndex struct {
	actors sync.Map // key(string) → *ActorBase
}

// AManager Actor管理器 — 全局单例，负责所有 Actor 的存储、查找、消息投递。
type AManager struct {
	actorMap   sync.Map // name → *ActorBase  主存储
	categories sync.Map // categoryName(string) → *categoryIndex
	mu         sync.RWMutex
}

// RegisterCategory 注册 Actor 类别。传入原型实例，通过 Category() 获取类别名。
func (am *AManager) RegisterCategory(prototype IActor) {
	am.mu.Lock()
	defer am.mu.Unlock()
	category := prototype.Category()
	if category == "" {
		logger.ERROR("RegisterCategory: empty category from prototype")
		return
	}
	if _, ok := am.categories.Load(category); ok {
		logger.ERROR("category already registered:", category)
		return
	}
	am.categories.Store(category, &categoryIndex{})
}

// RegisterAll 统一注册入口 — Server Phase 3 调用。在此创建所有 Actor 类别原型并注册。
func (am *AManager) RegisterAll() {
	am.RegisterCategory(&Player{})

	logger.INFO("AManager RegisterAll complete")
}

// addToCategories 将 actor 按 Category() 写入对应类别索引。
func (am *AManager) addToCategories(ab *ActorBase) {
	category := ab.iActor.Category()
	if category == "" {
		return
	}
	value, ok := am.categories.Load(category)
	if !ok {
		return
	}
	ci := value.(*categoryIndex)
	ci.actors.Store(ab.name, ab)
}

// removeFromCategories 将 actor 从对应类别索引中删除。
func (am *AManager) removeFromCategories(ab *ActorBase) {
	category := ab.iActor.Category()
	if category == "" {
		return
	}
	value, ok := am.categories.Load(category)
	if !ok {
		return
	}
	ci := value.(*categoryIndex)
	ci.actors.Delete(ab.name)
}

// Add 添加 Actor 到管理器。自动按已注册类别建立索引。
func (am *AManager) Add(ab *ActorBase) error {
	ab.manager = am // 注入管理器引用，供 handler 内部 RPC 使用
	am.actorMap.Store(ab.name, ab)
	am.addToCategories(ab)
	return nil
}

// Remove 从管理器中移除 Actor。从主存储和所有类别索引中删除。
func (am *AManager) Remove(name string) {
	value, ok := am.actorMap.Load(name)
	if !ok {
		return
	}
	ab, ok := value.(*ActorBase)
	if !ok {
		return
	}
	am.actorMap.Delete(name)
	am.removeFromCategories(ab)
}

// CloseActor 关闭指定 Actor 并从管理器中移除，Actor 生命周期统一由管理器负责。
func (am *AManager) CloseActor(name string, timeout time.Duration) error {
	ab := am.Find(name)
	if ab == nil {
		return ErrActorNotFound
	}
	err := ab.Shutdown(timeout)
	// 无论关闭结果如何都从注册表移除，避免残留已关闭的 Actor。
	am.Remove(name)
	if err != nil {
		logger.ERROR("close actor error:", err, "name:", name)
	}
	return err
}

// StartActor 创建并启动一个常驻服务 Actor（无连接），注册到管理器。
func (am *AManager) StartActor(name string, callBack IActor, config *ActorConfig) (*ActorBase, error) {
	ab := NewActorBase(name, callBack, config)
	if ab == nil {
		return nil, fmt.Errorf("create actor base failed: %s", name)
	}
	if err := am.Add(ab); err != nil {
		return nil, err
	}
	return ab, nil
}

// StartPlayer 创建并启动玩家 Actor，注册到管理器。
func (am *AManager) StartPlayer(name string, conn network.NetConn) (*Player, error) {
	playerActor := &Player{
		name: name,
		ip:   conn.RemoteAddr(),
		conn: conn,
		PlayerData: &PlayerData{
			Name:        name,
			LastLoginAt: time.Now().Unix(),
		},
	}
	if _, err := am.StartActor(name, playerActor, nil); err != nil {
		return nil, err
	}
	return playerActor, nil
}

// Find 根据名称查找 Actor 实例。
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

// FindByCategory 按类别和 key 查找 Actor 实例。
func (am *AManager) FindByCategory(category, key string) *ActorBase {
	value, ok := am.categories.Load(category)
	if !ok {
		return nil
	}
	ci := value.(*categoryIndex)
	actorVal, ok := ci.actors.Load(key)
	if !ok {
		return nil
	}
	ab, ok := actorVal.(*ActorBase)
	if !ok {
		return nil
	}
	return ab
}

// Range 遍历所有 Actor。回调返回 false 时停止遍历。
func (am *AManager) Range(fn func(*ActorBase) bool) {
	am.actorMap.Range(func(key, value interface{}) bool {
		ab, ok := value.(*ActorBase)
		if !ok {
			return true
		}
		return fn(ab)
	})
}

// Count 返回当前 Actor 总数。
func (am *AManager) Count() int {
	n := 0
	am.actorMap.Range(func(key, value interface{}) bool {
		n++
		return true
	})
	return n
}

// ShutdownActors 遍历关闭所有 Actor。
func (am *AManager) ShutdownActors(timeout time.Duration) {
	am.Range(func(ab *ActorBase) bool {
		if err := ab.Shutdown(timeout); err != nil {
			logger.ERROR("shutdown actor error:", err, "name:", ab.name)
		}
		return true
	})
}

// Deliver 投递客户端消息（来自网络层）。msg 为协议帧字节。
func (am *AManager) Deliver(name string, data []byte) error {
	return am.deliver(name, &Envelope{From: "server", Kind: MsgClient, Payload: data})
}

// Send 向其他 actor 异步投递消息。
func (am *AManager) Send(to, from string, msg interface{}) error {
	return am.deliver(to, &Envelope{From: from, Kind: MsgSend, Payload: msg})
}

// Call 向其他 actor 发起 RPC 并等待回复。
func (am *AManager) Call(to, from string, msg interface{}, timeout time.Duration) (interface{}, error) {
	if to == from {
		return nil, ErrSelfCall
	}
	ab := am.Find(to)
	if ab == nil {
		return nil, ErrActorNotFound
	}
	if ab.isClosed() {
		return nil, ErrActorClose
	}

	env := &Envelope{
		From:    from,
		Kind:    MsgCall,
		Payload: msg,
		Reply:   make(chan interface{}, 1),
	}
	if err := am.deliver(to, env); err != nil {
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case resp := <-env.Reply:
		if ce, ok := resp.(*CallError); ok {
			return nil, ce
		}
		return resp, nil
	case <-timer.C:
		return nil, ErrSendTimeout
	}
}

// deliver 投递信封到目标 actor 队列。
func (am *AManager) deliver(name string, env *Envelope) error {
	ab := am.Find(name)
	if ab == nil {
		return ErrActorNotFound
	}
	if ab.isClosed() {
		return ErrActorClose
	}
	timer := time.NewTimer(ab.config.SendTimeout)
	defer timer.Stop()
	select {
	case ab.request <- env:
		return nil
	case <-timer.C:
		return ErrSendTimeout
	}
}
