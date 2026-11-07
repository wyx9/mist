package core

// Persistable 可持久化 actor：表名/前缀/库名由业务实现提供，框架不感知业务配置。
type Persistable interface {
	ActorID() int64
	PersistData() any
	PersistKeyPrefix() string // Redis key 前缀，如 "actor_data:player:"
	PersistTable() string     // MySQL 表名，如 "player_data"
	PersistDBName() string    // 空串 = 使用配置默认库
}

// PersistStore actor 落地句柄，由 persist 包实现注入。
type PersistStore interface {
	Load(actorID int64, v any) (bool, error) // Redis -> MySQL 回源
	Save(actorID int64, v any) error         // 写 Redis 热层并登记异步落库
	Delete(actorID int64) error
}

// SetPersistStore 创建阶段注入 PersistStore（须在 Start 之前）。
type SetPersistStore interface {
	SetPersistStore(s PersistStore)
}

// PersistTick 定时消息：PersistManager 投递给可持久化 actor，由 actor 串行处理。
type PersistTick struct{}
