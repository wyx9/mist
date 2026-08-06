package core

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/protocol"
	"simple_game/game/pkg/uid"
)

// categoryIndex 单个类别的索引结构。
type categoryIndex struct {
	actors sync.Map // key(string) → *ActorBase
}

// AManager Actor管理器 — 全局单例，负责所有 Actor 的存储、查找、消息投递。
type AManager struct {
	actorMap     sync.Map // name → *ActorBase  主存储
	categories   sync.Map // categoryName(string) → *categoryIndex
	mu           sync.RWMutex
	faultHandler func(name string) // actor 故障回调（handler 超时等），由 gameserver 设置
}

// SetFaultHandler 注册 actor 故障回调（如 handler 超时），用于踢掉故障玩家的连接。
func (am *AManager) SetFaultHandler(fn func(name string)) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.faultHandler = fn
}

// notifyFault 通知故障回调（不阻塞，读取回调快照后调用）。
func (am *AManager) notifyFault(name string) {
	am.mu.RLock()
	fn := am.faultHandler
	am.mu.RUnlock()
	if fn != nil {
		fn(name)
	}
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
	if err != nil && !errors.Is(err, ErrActorClose) {
		logger.ERROR("close actor error:", err, "name:", name)
	}
	return err
}

// StartActor 创建并启动一个 Actor，注册到管理器。
// 客户端消息默认走 HandleClientMsg：按具体 actor 类型注入 clientHandler，
// 响应通过 actor 的 WriteMessage 能力（clientMsgWriter）自动回包。
// 注意：Go 方法不支持类型参数，因此这是包级泛型函数。
func StartActor[A IActor](am *AManager, name string, actor A, config *ActorConfig) (*ActorBase, error) {
	// 框架级身份保证：每个 actor 都有全局唯一 uid。
	// 具体 actor 若实现 ActorID() 且返回非零（如 Player 使用账号 uid），则沿用；否则自动分配雪花 ID。
	id := uid.NewUid()
	if p, ok := any(actor).(interface{ ActorID() int64 }); ok && p.ActorID() != 0 {
		id = p.ActorID()
	}
	ab := NewActorBase(name, actor, config)
	if ab == nil {
		return nil, fmt.Errorf("create actor base failed: %s", name)
	}
	ab.SetID(id)
	actorRef := actor
	ab.clientHandler = func(ctx *ActorContext, msg interface{}) {
		HandleClientMsg(actorRef, ctx, msg)
	}
	if err := am.Add(ab); err != nil {
		return nil, err
	}
	return ab, nil
}

// RangeByCategory 遍历指定类别的所有 Actor。回调返回 false 时停止遍历。
func (am *AManager) RangeByCategory(category string, fn func(name string, ab *ActorBase) bool) {
	value, ok := am.categories.Load(category)
	if !ok {
		return
	}
	ci := value.(*categoryIndex)
	ci.actors.Range(func(key, value interface{}) bool {
		ab, ok := value.(*ActorBase)
		if !ok {
			return true
		}
		return fn(key.(string), ab)
	})
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

// Deliver 投递客户端消息（来自网络层）。msg 为已解析的消息信封。
func (am *AManager) Deliver(name string, msg *protocol.Msg) error {
	return am.deliver(name, &Envelope{From: "server", Kind: MsgClient, Payload: msg})
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
