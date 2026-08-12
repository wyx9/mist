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

// RoomPlayerInfo 大厅玩家快照条目（Room -> controller，RPC 回复）。
type RoomPlayerInfo struct {
	UID  int64
	Name string
}

// RoomGetPlayers 大厅在线玩家快照查询（Call/RPC，回复 []RoomPlayerInfo）。
type RoomGetPlayers struct{}

// LobbyBroadcastKind 大厅上下线广播类型。
type LobbyBroadcastKind int

const (
	LobbyJoin LobbyBroadcastKind = iota + 1
	LobbyLeave
)

// LobbyBroadcast 大厅上下线广播（Room -> 各玩家 actor，由玩家写回自己的连接）。
type LobbyBroadcast struct {
	Kind   LobbyBroadcastKind
	UID    int64
	Name   string
	Online int
}
