package core

import (
	"fmt"

	"simple_game/game/pkg/logger"
)

// Persistable 可选实现：声明 actor 需要持久化，并提供状态快照。
// 返回的指针会被 Load 直接反序列化写入，必须是 actor 长期持有的同一份数据。
type Persistable interface {
	PersistData() any
}

// PersistStore 持久化后端；默认 Redis+MySQL 与自定义实现共用此接口。
type PersistStore interface {
	Load(actorID int64, v any) (bool, error) // Redis -> MySQL 回源
	Save(actorID int64, v any) error         // 写 Redis 热层并登记异步落库
	Delete(actorID int64) error
}

// PersistHandle 框架注入：actor 数据变更后调用 MarkDirty 立即落地。
type PersistHandle interface {
	MarkDirty()
}

// SetPersistHandle 可选注入：创建阶段注入 MarkDirty 句柄（须在 Start 之前）。
type SetPersistHandle interface {
	SetPersistHandle(h PersistHandle)
}

// PersistResolver 由 persist 包实现并注册到 AManager，按类别解析默认落地后端。
type PersistResolver interface {
	Resolve(category string) PersistStore
}

// PersistSave 系统消息：周期落盘与 MarkDirty 立即落盘统一入口，由 actor 主循环拦截。
type PersistSave struct{}

// loadData 启动前加载持久化数据；store 为空表示该 actor 不持久化。
func (ac *ActorBase) loadData() error {
	if ac.persistStore == nil {
		return nil
	}
	p, ok := ac.iActor.(Persistable)
	if !ok {
		return nil
	}
	found, err := ac.persistStore.Load(ac.id, p.PersistData())
	if err != nil {
		return fmt.Errorf("load actor data (uid=%d): %w", ac.id, err)
	}
	if !found {
		logger.INFO("actor no persisted data, use default, uid:", ac.id)
	}
	return nil
}

// saveNow 在 actor 主循环内串行落地当前快照。
func (ac *ActorBase) saveNow() {
	if ac.persistStore == nil {
		return
	}
	p, ok := ac.iActor.(Persistable)
	if !ok {
		return
	}
	if err := ac.persistStore.Save(ac.id, p.PersistData()); err != nil {
		logger.ERROR("actor persist save failed:", err, "uid:", ac.id)
	}
}
