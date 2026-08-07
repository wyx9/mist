package actor

import (
	"errors"
	"time"

	"simple_game/game/internal/actor/core"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/network"
)

// Player 玩家业务 Actor，实现 core.IActor / core.Persistable / core.MessageHandler / core.IdleTimeoutProvider。
type Player struct {
	name       string            // 玩家名（登录名，路由名）
	uid        int64             // 玩家全局唯一 id（持久化主键，来自账号体系）
	ip         string            // 玩家 IP 地址
	conn       network.NetConn   // 网络连接（协议无关；休眠时为空，重连经 ConnAttached 换绑）
	store      core.PersistStore // 持久化落地句柄（由 gameserver 创建时注入）
	dirty      bool              // 脏标记：数据变更后置位，持久化 tick 落地
	PlayerData *PlayerData       // 玩家数据
}

// PlayerData 玩家数据，序列化后存储。
type PlayerData struct {
	UID         int64  // 玩家唯一 id
	Name        string // 玩家名
	LastLoginAt int64  // 最后登录时间
}

// RegisterAll 统一注册业务 Actor 类别（Player），Server Phase 3 调用。
func RegisterAll(am *core.AManager) {
	am.RegisterCategory(&Player{})
	logger.INFO("actor RegisterAll complete")
}

// StartPlayer 创建并启动玩家 Actor，注册到管理器。
// 连接不在创建时注入：由握手完成 Attach 后经 ConnAttached 消息换绑，保证 actor 串行。
func StartPlayer(am *core.AManager, name string, uid int64, store core.PersistStore) (*Player, error) {
	playerActor := &Player{
		name:  name,
		uid:   uid,
		store: store,
		PlayerData: &PlayerData{
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
}

// Category 返回 Actor 类别标识。
func (p *Player) Category() string { return "player" }

// Stop 玩家 Actor 停止时同步落地兜底。
func (p *Player) Stop() {
	p.flushNow()
}

// IdleTimeout 实现 core.IdleTimeoutProvider：断线重连宽限/休眠超时 10 分钟。
func (p *Player) IdleTimeout() time.Duration { return 10 * time.Minute }

// ActorID 实现 core.Persistable：返回玩家全局唯一 uid。
func (p *Player) ActorID() int64 { return p.uid }

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

// MarkDirty 标记玩家数据已变更，等待下一个持久化 tick 落地。
func (p *Player) MarkDirty() { p.dirty = true }

// HandleMessage 处理内部消息：PersistTick 按脏标记落地；ConnDetached/ConnAttached 维护连接引用。
func (p *Player) HandleMessage(ctx *core.ActorContext, msg interface{}) {
	switch m := msg.(type) {
	case core.PersistTick:
		p.flushIfDirty()
	case core.ConnDetached:
		p.conn = nil
		logger.INFO("player conn detached, actor sleeping, name:", p.name)
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

// WriteMessage 实现 core.clientMsgWriter：向该玩家的客户端连接回包；休眠无连接时显式报错。
func (p *Player) WriteMessage(data []byte) error {
	if p.conn == nil {
		return errors.New("player has no connection")
	}
	return p.conn.WriteMessage(data)
}
