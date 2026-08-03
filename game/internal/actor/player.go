package actor

import (
	"encoding/json"
	"fmt"

	"simple_game/game/internal/route"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/network"
	"simple_game/game/pkg/protocol"
	"simple_game/game/pkg/redis"
)

// Player 玩家Actor结构体，实现了IActor接口
type Player struct {
	name       string          // 玩家名称
	ip         string          // 玩家IP地址
	ac         IActor          // Actor接口实现
	conn       network.NetConn // 网络连接（协议无关）
	PlayerData *PlayerData     // 玩家数据
}

// PlayerData 玩家数据结构体，用于存储玩家的基本信息
type PlayerData struct {
	Name        string // 玩家名称
	LastLoginAt int64  // 最后登录时间
}

// Start 玩家Actor启动时的初始化
func (p *Player) Start() {
	p.loadPlayer() // 加载玩家数据
}

// Category 返回 Actor 类别标识。
func (p *Player) Category() string { return "player" }

// Stop 玩家Actor停止时的清理
func (p *Player) Stop() {
	// 持久化玩家数据
	logger.INFO("player stop ,player name :", p.name)
	p.persistence()
}

// PersistenceKey 生成玩家数据的持久化键
func (p *Player) PersistenceKey() string {
	return fmt.Sprintf("player_data_%s", p.PlayerData.Name)
}

// persistence 将玩家数据持久化到Redis
func (p *Player) persistence() {
	data := p.PlayerData
	marshal, _ := json.Marshal(data)
	key := p.PersistenceKey()
	redis.RedisClient.Set(redis.RCtx, key, marshal, -1)
	logger.INFO("player persistence", p.name)
}

// loadPlayer 从Redis加载玩家数据
func (p *Player) loadPlayer() {
	key := p.PersistenceKey()
	cmd := redis.RedisClient.Get(redis.RCtx, key)
	if cmd.Val() == "" {
		return
	}
	playerData := p.PlayerData

	_ = json.Unmarshal([]byte(cmd.Val()), playerData)
	logger.INFO("player persistence", p.name)
}

// Handler 处理接收到的消息
func (p *Player) Handler(ctx *ActorContext, msg interface{}) {
	p.HandlerByClient(msg)
}

// HandlerByClient 处理来自客户端的消息
func (p *Player) HandlerByClient(msg interface{}) {
	bytes, ok := msg.([]byte)
	if !ok {
		return
	}
	// 解码业务帧：cmd + protobuf
	cmd, body, err := protocol.DecodeFrame(bytes)
	if err != nil {
		logger.ERROR("decode frame error:", err)
		return
	}
	// 路由处理消息
	resCmd, res, err := route.Route(p, cmd, body)
	if err != nil {
		logger.ERROR("route error:", cmd, err)
		return
	}
	logger.DEBUG("req cmd :", cmd)
	if res == nil {
		return
	}
	out, err := protocol.PackCmd(resCmd, res)
	if err != nil {
		logger.ERROR("pack response error:", err)
		return
	}
	_ = p.conn.WriteMessage(out)
}
