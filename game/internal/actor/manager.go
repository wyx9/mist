package actor

import (
	"sync"
	"time"

	"simple_game/game/pkg/logger"
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

// ActorManner 全局Actor管理器实例
var ActorManner = &AManager{}

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

// CastMsg 向指定 Actor 发送异步消息。
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

// CallMsg 向指定 Actor 发送同步调用请求。
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
