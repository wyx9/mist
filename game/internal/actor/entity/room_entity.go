package entity

// Room 房间内存数据：uid -> 玩家名（不持久化）。
type Room struct {
	Players map[int64]string
}

// RoomPlayerJoin 玩家登录加入房间消息（异步投递，来自 LoginController）。
type RoomPlayerJoin struct {
	UID  int64  // 玩家全局唯一 id
	Name string // 玩家名
}

// RoomPlayerLeave 玩家断开离开房间消息（异步投递，来自 Player.Stop）。
type RoomPlayerLeave struct {
	UID int64 // 玩家全局唯一 id
}
