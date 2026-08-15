package entity

// Room 内存数据：uid -> 玩家名（不持久化）。
type Room struct {
	Players map[int64]string
}
