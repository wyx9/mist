// agent/server.go
package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/go-redis/redis/v8"

	"simple_game/game/pkg/codec"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/network"
)

// handshakeMsg 客户端握手消息（spec 5.1b）。
type handshakeMsg struct {
	Token   string `json:"token"`
	Version int    `json:"version"`
}

// playerReg Redis 中存储的玩家注册信息。
type playerReg struct {
	AgentAddr string `json:"agent_addr"`
	GameAddr  string `json:"game_addr"`
}

// startAgent 启动 Agent 网关服务。
func startAgent(listeners []listenerCfg, redisCfg redisCfg) {
	// 初始化 Redis 客户端（agent 独立管理，不使用 pkg.RCtx）
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisCfg.Addr,
		Password: redisCfg.PassWord,
		DB:       redisCfg.DB,
	})

	for _, lc := range listeners {
		l, err := network.NewListener(lc.Network, lc.Addr, lc.Port)
		if err != nil {
			logger.ERROR("agent listener create failed:", lc.Network, lc.Addr, lc.Port, err)
			continue
		}
		logger.INFO("agent listener start:", lc.Network, l.Addr())
		go serveListener(l, rdb)
	}
}

// serveListener accept 客户端连接并为每个连接创建 Session。
func serveListener(l network.Listener, rdb *redis.Client) {
	for {
		conn, err := l.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "closed") {
				return
			}
			logger.ERROR("agent accept error:", err)
			continue
		}
		go handleClient(conn, rdb)
	}
}

// handleClient 处理单个客户端连接：握手 → 建连 Game → 双向转发。
func handleClient(clientConn network.NetConn, rdb *redis.Client) {
	// 1. 读握手消息
	data, err := clientConn.ReadMessage()
	if err != nil {
		logger.ERROR("read handshake from client failed:", err)
		_ = clientConn.Close()
		return
	}

	hs := &handshakeMsg{}
	if err := json.Unmarshal(data, hs); err != nil {
		logger.ERROR("parse handshake failed:", err)
		_ = clientConn.Close()
		return
	}

	// 2. 从 token 中提取 player_name（不验证签名 — 由 Game 验证）
	playerName := extractSubFromToken(hs.Token)
	if playerName == "" {
		logger.ERROR("extract player name from token failed")
		_ = clientConn.Close()
		return
	}

	// 3. 查 Redis 获取 game_addr
	val, err := rdb.Get(rdb.Context(), "player:"+playerName).Result()
	if err != nil {
		logger.ERROR("redis get player info failed:", err)
		_ = clientConn.Close()
		return
	}
	reg := &playerReg{}
	if err := json.Unmarshal([]byte(val), reg); err != nil {
		logger.ERROR("parse player reg failed:", err)
		_ = clientConn.Close()
		return
	}

	// 4. 建连 Game
	gameConn, err := network.Dial("tcp", reg.GameAddr)
	if err != nil {
		logger.ERROR("dial game failed:", reg.GameAddr, err)
		_ = clientConn.WriteMessage([]byte(`{"error":"game unavailable"}`))
		_ = clientConn.Close()
		return
	}

	// 5. 转发握手消息到 Game
	_ = gameConn.WriteMessage(data)

	// 6. 等待 Game 握手确认
	resp, err := gameConn.ReadMessage()
	if err != nil {
		logger.ERROR("read game handshake response failed:", err)
		_ = clientConn.Close()
		_ = gameConn.Close()
		return
	}
	// 转发握手结果给客户端
	tm := codec.UnpackTunnel(resp)
	var clientResp []byte
	if tm != nil && tm.Name == "HandshakeOk" {
		clientResp = []byte(`{"status":"ok"}`)
	} else {
		clientResp = []byte(`{"status":"error","detail":"handshake failed"}`)
	}
	_ = clientConn.WriteMessage(clientResp)

	// 7. 创建 Session 并启动双向转发
	sess := &Session{
		TunnelID:   playerName,
		ClientConn: clientConn,
		GameConn:   gameConn,
	}
	sessionMap.Store(playerName, sess)
	defer func() {
		sessionMap.Delete(playerName)
		_ = clientConn.Close()
		_ = gameConn.Close()
	}()

	// 启动双向转发
	go clientToGame(sess)
	gameToClient(sess)
}

// clientToGame 客户端→Game 转发。
func clientToGame(sess *Session) {
	for {
		data, err := sess.ClientConn.ReadMessage()
		if err != nil {
			_ = sess.GameConn.Close()
			return
		}
		// 纯字节转发：不解析、不重打包（连接已在握手时建立 player 映射）
		if err := sess.GameConn.WriteMessage(data); err != nil {
			_ = sess.ClientConn.Close()
			return
		}
	}
}

// gameToClient Game→客户端 转发。
func gameToClient(sess *Session) {
	for {
		data, err := sess.GameConn.ReadMessage()
		if err != nil {
			_ = sess.ClientConn.Close()
			return
		}
		// 纯字节转发
		if err := sess.ClientConn.WriteMessage(data); err != nil {
			_ = sess.GameConn.Close()
			return
		}
	}
}

// extractSubFromToken 从 JWT token 中提取 sub（player_name），不验证签名。
func extractSubFromToken(tokenStr string) string {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return ""
	}
	// JWT payload 是 base64url 编码的 JSON
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.Sub
}
