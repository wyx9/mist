package actor

import (
	"errors"
	"time"

	"simple_game/game/api/protos/pt"
	"simple_game/game/internal/actor/core"
	"simple_game/game/internal/actor/entity"
	"simple_game/game/pkg/actortype"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/network"
	"simple_game/game/pkg/tool"
)

// Player 玩家业务 Actor，实现 core.IActor / core.Persistable / core.MessageHandler / core.IdleTimeoutProvider。
type Player struct {
	name       string             // 玩家名
	uid        int64              // 玩家全局唯一 id（持久化主键，来自账号体系）
	ip         string             // 玩家 IP 地址
	conn       network.NetConn    // 网络连接（协议无关；休眠时为空，重连经 ConnAttached 换绑）
	mgr        *core.AManager     // 框架句柄：Stop 时向常驻 Room actor 投递离开通知
	timer      core.TimerHandle   // actor 内部定时器句柄（消息式，框架注入）
	persist    core.PersistHandle // 立即落地句柄（框架注入）：数据变更后调 MarkDirty
	PlayerData *entity.PlayerData // 玩家数据
}

// NewPlayer 构造玩家 actor（uid 为身份；name 由 Start 加载持久化或登录路径 SetName 补齐）。
// 连接不在创建时注入：由握手完成 Attach 后经 ConnAttached 消息换绑，保证 actor 串行。
func NewPlayer(am *core.AManager, uid int64) *Player {
	return &Player{
		uid: uid,
		mgr: am,
		PlayerData: &entity.PlayerData{
			UID:         uid,
			LastLoginAt: time.Now().Unix(),
		},
	}
}

// SetName 设置玩家展示名（新玩家登录时由握手帧补齐）。
func (p *Player) SetName(name string) {
	p.name = name
	p.PlayerData.Name = name
}

// Start 玩家 Actor 启动：持久化数据已由框架在 Start 之前加载。
func (p *Player) Start() error {
	p.GetTimer().After(3*time.Second, timerTest{})
	return nil
}

// Type 返回 Actor 类型标识。
func (p *Player) Type() actortype.ActorType { return ActorTypePlayer }

// Stop 玩家 Actor 停止：数据已由框架落地，这里只通知常驻 Room 移除自己。
func (p *Player) Stop() {
	p.notifyRoomLeave()
}

// IdleTimeout 实现 core.IdleTimeoutProvider：断线重连宽限/休眠超时 10 分钟。
func (p *Player) IdleTimeout() time.Duration { return 10 * time.Minute }

// Routable 实现 core.Routable：玩家 actor 全局可路由（登录名全局唯一）。
func (p *Player) Routable() {}

// ActorID 返回玩家全局唯一 uid（Persistable）。
func (p *Player) ActorID() int64 { return p.uid }

// Name 返回玩家展示名。
func (p *Player) Name() string { return p.name }

// PersistData 返回待序列化的玩家数据。
func (p *Player) PersistData() any { return p.PlayerData }

// PersistKeyPrefix 返回 Redis key 前缀。
func (p *Player) PersistKeyPrefix() string { return "actor_data:player:" }

// PersistTable 返回 MySQL 表名。
func (p *Player) PersistTable() string { return "player_data" }

// PersistDBName 返回 MySQL 库名，空串表示使用配置默认库。
func (p *Player) PersistDBName() string { return "" }

// SetPersistHandle 创建阶段注入立即落地句柄（须在 Start 之前）。
func (p *Player) SetPersistHandle(h core.PersistHandle) { p.persist = h }

// SetTimerHandle 实现 core.SetTimerHandle：创建阶段注入 actor 内部定时器句柄。
func (p *Player) SetTimerHandle(h core.TimerHandle) { p.timer = h }

func (p *Player) GetTimer() core.TimerHandle {
	return p.timer
}

type timerTest struct{}

// MarkDirty 标记玩家数据已变更，触发一次立即落地。
func (p *Player) MarkDirty() {
	if p.persist != nil {
		p.persist.MarkDirty()
	}
}

// HandleMessage 处理内部消息：ConnDetached/ConnAttached 维护连接引用。
func (p *Player) HandleMessage(ctx *core.ActorContext, msg interface{}) {
	switch m := msg.(type) {
	case timerTest:
		logger.INFO("player timer test")
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
	}
}

// notifyRoomLeave 玩家 actor 关闭时通知常驻 Room actor 移除自己。
// 仅 actor 真正关闭（休眠超时/踢线/停机）触发；纯断开不触发。
func (p *Player) notifyRoomLeave() {
	if p.mgr == nil {
		return
	}
	if err := p.mgr.Cast(RoomUID, tool.ToString(p.uid), &pt.RoomPlayerLeave{UID: p.uid}); err != nil {
		logger.ERROR("notify room leave failed:", err, "uid:", p.uid)
	}
}

// Connected 返回玩家当前是否持有连接（断线休眠期返回 false）。
func (p *Player) Connected() bool { return p.conn != nil }

// WriteMessage 实现 core.clientMsgWriter：向该玩家的客户端连接回包；休眠无连接时显式报错。
func (p *Player) WriteMessage(data []byte) error {
	if p.conn == nil {
		return errors.New("player has no connection")
	}
	return p.conn.WriteMessage(data)
}
