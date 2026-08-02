package engine

import (
	"encoding/json"
	"fmt"
	"simple_game/game/libs"
	"simple_game/game/pkg"
	"simple_game/game/routes"
	"time"
)

// Player 玩家Actor结构体，实现了IActor接口
type Player struct {
	name       string      // 玩家名称
	ip         string      // 玩家IP地址
	ac         IActor      // Actor接口实现
	conn       pkg.NetConn // 网络连接（协议无关）
	PlayerData *PlayerData // 玩家数据
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

// Stop 玩家Actor停止时的清理
func (p *Player) Stop() {
	// 持久化玩家数据
	pkg.INFO("player stop ,player name :", p.name)
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
	pkg.RedisClient.Set(pkg.RCtx, key, marshal, -1)
	pkg.INFO("player persistence", p.name)
}

// loadPlayer 从Redis加载玩家数据
func (p *Player) loadPlayer() {
	key := p.PersistenceKey()
	cmd := pkg.RedisClient.Get(pkg.RCtx, key)
	if cmd.Val() == "" {
		return
	}
	playerData := p.PlayerData

	_ = json.Unmarshal([]byte(cmd.Val()), playerData)
	pkg.INFO("player persistence", p.name)
}

// Handler 处理接收到的消息
func (p *Player) Handler(msg interface{}) interface{} {
	p.HandlerByClient(msg)
	return nil
}

// HandlerByClient 处理来自客户端的消息
func (p *Player) HandlerByClient(msg interface{}) {
	bytes, ok := msg.([]byte)
	if !ok {
		return
	}
	// 解码消息包
	codePack := libs.DeCodePack(bytes)
	if codePack == nil {
		return
	}
	// 路由处理消息
	handler, res, err := routes.Route(p, codePack.Name, codePack.Data)
	if err != nil {
		pkg.ERROR("handler is nil , ", handler)
	} else {
		pkg.DEBUG("req :", codePack.Name)
		marshal := libs.Pack2Msg(res)
		pkg.DEBUG("res :", string(marshal))
		_ = p.conn.WriteMessage(marshal)
	}
}

// StartNewPlayerActor 创建并启动新的玩家Actor
func StartNewPlayerActor(name string, conn pkg.NetConn) (*Player, error) {
	playerActor := &Player{
		name: name,
		ip:   conn.RemoteAddr(),
		conn: conn,
		PlayerData: &PlayerData{
			Name:        name,
			LastLoginAt: time.Now().Unix(),
		},
	}
	_ = NewActorBase(name, playerActor, nil)
	return playerActor, nil
}
