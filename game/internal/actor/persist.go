package actor

// Persistable 可持久化 actor 接口：具体 actor 实现后由 PersistManager 定时驱动落地。
// ActorID 返回全局唯一 uid（Player 使用账号体系分配的玩家 id，内部 actor 由创建方定义）。
type Persistable interface {
	ActorID() int64
	PersistData() any
}

// PersistHandle 提供给具体 actor 的通用落地句柄，由 persist 包实现并注入。
type PersistHandle interface {
	// Load 加载数据到 v，返回是否命中；内部按 Redis -> MySQL 顺序回源。
	Load(actorID int64, v any) (bool, error)
	// Save 序列化 v 并写入 Redis（热源），同时登记脏标记等待异步落 MySQL。
	Save(actorID int64, v any) error
	// Delete 删除该 actor 的持久化数据（Redis 与 MySQL 最终一致删除）。
	Delete(actorID int64) error
}

// SetPersistHandle 可选接口：具体 actor 在创建时注入 PersistHandle（须在 Start 之前完成）。
type SetPersistHandle interface {
	SetPersistHandle(h PersistHandle)
}

// PersistTick 持久化定时消息：PersistManager 按类别定时投递给 Persistable actor，
// actor 在自己的消息循环内处理（保持 actor 串行，参考 skynet 消息驱动调度）。
type PersistTick struct{}
