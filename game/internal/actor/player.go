package actor

import (
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/network"
)

// Player 玩家 Actor，实现 IActor / Persistable / MessageHandler。
type Player struct {
	name       string          // 玩家名（登录名/路由名）
	uid        int64           // 玩家全局唯一 id（持久化主键，来自账号体系）
	ip         string          // 玩家 IP 地址
	conn       network.NetConn // 网络连接（协议无关）
	handle     PersistHandle   // 持久化句柄（由 gameserver 创建时注入）
	dirty      bool            // 脏标记：数据变更后置位，持久化 tick 落地
	PlayerData *PlayerData     // 玩家数据
}

// PlayerData 玩家数据，序列化后存储。
type PlayerData struct {
	UID         int64  // 玩家唯一 id
	Name        string // 玩家名
	LastLoginAt int64  // 最后登录时间
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

// ActorID 实现 Persistable：返回玩家全局唯一 uid。
func (p *Player) ActorID() int64 { return p.uid }

// PersistData 实现 Persistable：返回待序列化的玩家数据。
func (p *Player) PersistData() any { return p.PlayerData }

// SetPersistHandle 实现 SetPersistHandle：创建阶段注入，须在 Start 之前完成。
func (p *Player) SetPersistHandle(h PersistHandle) { p.handle = h }

// MarkDirty 标记玩家数据已变更，等待下一个持久化 tick 落地。
func (p *Player) MarkDirty() { p.dirty = true }

// HandleMessage 处理内部消息：PersistTick 时按脏标记落地。
func (p *Player) HandleMessage(ctx *ActorContext, msg interface{}) {
	if _, ok := msg.(PersistTick); ok {
		p.flushIfDirty()
	}
}

// loadPlayer 通过持久化句柄加载玩家数据；双 miss 时使用默认结构并标记脏以便首次落地。
func (p *Player) loadPlayer() {
	if p.handle == nil {
		logger.WARNING("player persist handle not set, skip load:", p.name)
		return
	}
	found, err := p.handle.Load(p.uid, p.PlayerData)
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
	if p.handle == nil {
		logger.WARNING("player persist handle not set, skip flush:", p.name)
		return
	}
	if err := p.handle.Save(p.uid, p.PlayerData); err != nil {
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

// WriteMessage 实现 clientMsgWriter：向该玩家的客户端连接回包。
func (p *Player) WriteMessage(data []byte) error {
	return p.conn.WriteMessage(data)
}
