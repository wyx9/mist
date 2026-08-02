// agent/session.go
package main

import (
	"sync"

	"simple_game/game/pkg/network"
)

// Session 客户端连接 ↔ Game 连接的映射。
type Session struct {
	TunnelID   string
	ClientConn network.NetConn
	GameConn   network.NetConn
}

// sessionMap 全局会话表。
var sessionMap sync.Map // string → *Session
