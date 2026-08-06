package core

// Persistable 可持久化 actor 接口：具体 actor 必须实现，由 PersistManager 定时驱动落地。
// 表名 / Redis key 前缀 / MySQL 库名均由具体 actor 提供，框架不感知业务配置。
type Persistable interface {
	ActorID() int64
	PersistData() any
	PersistKeyPrefix() string // Redis key 前缀，如 "actor_data:player:"
	PersistTable() string     // MySQL 表名，如 "player_data"
	PersistDBName() string    // MySQL 库名，空串 = 使用配置默认库
}

// PersistStore 提供给具体 actor 的通用落地句柄，由 persist 包实现并注入。
type PersistStore interface {
	// Load 加载数据到 v，返回是否命中；内部按 Redis -> MySQL 顺序回源。
	Load(actorID int64, v any) (bool, error)
	// Save 序列化 v 并写入 Redis（热源），同时登记脏标记等待异步落 MySQL。
	Save(actorID int64, v any) error
	// Delete 删除该 actor 的持久化数据（Redis 与 MySQL 最终一致删除）。
	Delete(actorID int64) error
}

// SetPersistStore 可选接口：具体 actor 在创建时注入 PersistStore（须在 Start 之前完成）。
type SetPersistStore interface {
	SetPersistStore(s PersistStore)
}

// PersistTick 持久化定时消息：PersistManager 定时投递给 Persistable actor，
// actor 在自己的消息循环内处理（保持 actor 串行，参考 skynet 消息驱动调度）。
type PersistTick struct{}
