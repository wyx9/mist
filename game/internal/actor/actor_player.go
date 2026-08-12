package actor

import (
	"errors"
	"time"

	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
	"simple_game/game/internal/actor/core"
	"simple_game/game/internal/actor/entity"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/network"
	"simple_game/game/pkg/protocol"
)

// Player 玩家业务 Actor，实现 core.IActor / core.Persistable / core.MessageHandler / core.IdleTimeoutProvider。
type Player struct {
	name       string             // 玩家名（登录名，路由名）
	uid        int64              // 玩家全局唯一 id（持久化主键，来自账号体系）
	ip         string             // 玩家 IP 地址
	conn       network.NetConn    // 网络连接（协议无关；休眠时为空，重连经 ConnAttached 换绑）
	store      core.PersistStore  // 持久化落地句柄（由 gameserver 创建时注入）
	mgr        *core.AManager     // 框架句柄：Stop 时向常驻 Room actor 投递离开通知
	timer      core.TimerHandle   // actor 内部定时器句柄（消息式，框架注入）
	dirty      bool               // 脏标记：数据变更后置位，持久化 tick 落地
	PlayerData *entity.PlayerData // 玩家数据
}

// StartPlayer 创建并启动玩家 Actor，注册到管理器。
// 连接不在创建时注入：由握手完成 Attach 后经 ConnAttached 消息换绑，保证 actor 串行。
func StartPlayer(am *core.AManager, name string, uid int64, store core.PersistStore) (*Player, error) {
	playerActor := &Player{
		name:  name,
		uid:   uid,
		store: store,
		mgr:   am,
		PlayerData: &entity.PlayerData{
			UID:         uid,
			Name:        name,
			LastLoginAt: time.Now().Unix(),
		},
	}
	if _, err := core.StartActor(am, name, playerActor); err != nil {
		return nil, err
	}
	return playerActor, nil
}

// Start 玩家 Actor 启动时加载数据（Redis -> MySQL -> 默认值）。
func (p *Player) Start() {
	p.loadPlayer()

	p.GetTimer().After(3*time.Second, timerTest{})

}

// Category 返回 Actor 类别标识。
func (p *Player) Category() string { return PlayerActorName }

// Stop 玩家 Actor 停止时同步落地兜底，并通知常驻 Room actor 移除自己。
func (p *Player) Stop() {
	p.flushNow()
	p.notifyRoomLeave()
}

// IdleTimeout 实现 core.IdleTimeoutProvider：断线重连宽限/休眠超时 10 分钟。
func (p *Player) IdleTimeout() time.Duration { return 10 * time.Minute }

// ActorID 实现 core.Persistable：返回玩家全局唯一 uid。
func (p *Player) ActorID() int64 { return p.uid }

// Name 返回玩家名。
func (p *Player) Name() string { return p.name }

// PersistData 实现 core.Persistable：返回待序列化的玩家数据。
func (p *Player) PersistData() any { return p.PlayerData }

// PersistKeyPrefix 实现 core.Persistable：玩家数据 Redis key 前缀。
func (p *Player) PersistKeyPrefix() string { return "actor_data:player:" }

// PersistTable 实现 core.Persistable：玩家数据 MySQL 表名。
func (p *Player) PersistTable() string { return "player_data" }

// PersistDBName 实现 core.Persistable：玩家数据 MySQL 库名，空串表示使用配置默认库。
func (p *Player) PersistDBName() string { return "" }

// SetPersistStore 实现 core.SetPersistStore：创建阶段注入，须在 Start 之前完成。
func (p *Player) SetPersistStore(s core.PersistStore) { p.store = s }

// SetTimerHandle 实现 core.SetTimerHandle：创建阶段注入 actor 内部定时器句柄。
func (p *Player) SetTimerHandle(h core.TimerHandle) { p.timer = h }

func (p *Player) GetTimer() core.TimerHandle {
	return p.timer
}

type timerTest struct{}

// MarkDirty 标记玩家数据已变更，等待下一个持久化 tick 落地。
func (p *Player) MarkDirty() { p.dirty = true }

// HandleMessage 处理内部消息：PersistTick 按脏标记落地；ConnDetached/ConnAttached 维护连接引用。
func (p *Player) HandleMessage(ctx *core.ActorContext, msg interface{}) {
	switch m := msg.(type) {
	case timerTest:
		logger.INFO("player timer test")
	case core.PersistTick:
		p.flushIfDirty()
	case core.ConnDetached:
		p.conn = nil
		logger.INFO("player conn detached, actor sleeping, name:", p.name)
		// 断线即从大厅在线列表移除；actor 保留等待重连复用
		p.notifyRoomLeave()
	case core.ConnAttached:
		conn, ok := m.Conn.(network.NetConn)
		if !ok {
			logger.ERROR("ConnAttached with invalid conn, player:", p.name)
			return
		}
		p.conn = conn
		p.ip = conn.RemoteAddr()
		logger.INFO("player conn attached, name:", p.name)
	case entity.LobbyBroadcast:
		p.pushLobbyBroadcast(m)
	}
}

// pushLobbyBroadcast 将大厅上下线广播编码为 MsgTypePush 写回自己连接。
// 断线重连宽限期内无连接时静默丢弃，避免唤醒休眠 actor 产生噪音日志。
func (p *Player) pushLobbyBroadcast(m entity.LobbyBroadcast) {
	if p.conn == nil {
		return
	}
	var cmd uint16
	var bodyMsg proto.Message
	switch m.Kind {
	case entity.LobbyJoin:
		cmd = protocol.CmdLobbyJoinPush
		bodyMsg = &pt.LobbyJoinPush{Player: &pt.LobbyPlayer{UID: m.UID, Name: m.Name}, Online: int32(m.Online)}
	case entity.LobbyLeave:
		cmd = protocol.CmdLobbyLeavePush
		bodyMsg = &pt.LobbyLeavePush{Player: &pt.LobbyPlayer{UID: m.UID, Name: m.Name}, Online: int32(m.Online)}
	default:
		logger.WARNING("unknown lobby broadcast kind:", m.Kind, "player:", p.name)
		return
	}
	body, err := proto.Marshal(bodyMsg)
	if err != nil {
		logger.ERROR("lobby push marshal failed:", err, "player:", p.name)
		return
	}
	out, err := protocol.EncodeMsg(&protocol.Msg{
		MsgType: protocol.MsgTypePush,
		Cmd:     cmd,
		UID:     m.UID,
		Body:    body,
	})
	if err != nil {
		logger.ERROR("lobby push encode failed:", err, "player:", p.name)
		return
	}
	if err := p.WriteMessage(out); err != nil {
		logger.ERROR("lobby push write failed:", err, "player:", p.name)
	}
}

// loadPlayer 通过持久化句柄加载玩家数据；双 miss 时使用默认结构并标记脏以便首次落地。
func (p *Player) loadPlayer() {
	if p.store == nil {
		logger.WARNING("player persist handle not set, skip load:", p.name)
		return
	}
	found, err := p.store.Load(p.uid, p.PlayerData)
	if err != nil {
		logger.ERROR("player load failed:", err, "name:", p.name)
		return
	}
	if !found {
		p.dirty = true
		logger.INFO("player no persisted data, use default:", p.name)
		return
	}
	logger.INFO("player data loaded:", p.name)
}

// flushNow 无条件落地玩家数据（actor 退出/停机兜底）。
func (p *Player) flushNow() {
	if p.store == nil {
		logger.WARNING("player persist handle not set, skip flush:", p.name)
		return
	}
	if err := p.store.Save(p.uid, p.PlayerData); err != nil {
		logger.ERROR("player persist failed:", err, "name:", p.name)
		return
	}
	p.dirty = false
}

// flushIfDirty 仅当脏标记置位时落地（定时 tick 路径）。
func (p *Player) flushIfDirty() {
	if !p.dirty {
		return
	}
	p.flushNow()
}

// notifyRoomLeave 玩家 actor 关闭时通知常驻 Room actor 移除自己。
// 仅 actor 真正关闭（休眠超时/踢线/停机）触发；纯断开不触发。
func (p *Player) notifyRoomLeave() {
	if p.mgr == nil {
		return
	}
	if err := p.mgr.Send(RoomActorName, p.name, entity.RoomPlayerLeave{UID: p.uid}); err != nil {
		logger.ERROR("notify room leave failed:", err, "name:", p.name)
	}
}

// WriteMessage 实现 core.clientMsgWriter：向该玩家的客户端连接回包；休眠无连接时显式报错。
func (p *Player) WriteMessage(data []byte) error {
	if p.conn == nil {
		return errors.New("player has no connection")
	}
	return p.conn.WriteMessage(data)
}
