// gateway/server.go
package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/golang-jwt/jwt/v5"

	"simple_game/game/pkg/codec"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/network"
)

// handshakeMsg 客户端握手消息。
type handshakeMsg struct {
	Token   string `json:"token"`
	Version int    `json:"version"`
}

// gameHandshakeMsg 发给 game 的身份握手数据（隧道帧 payload）。
type gameHandshakeMsg struct {
	PlayerName string `json:"player_name"`
	UID        int64  `json:"uid"`
	Version    int    `json:"version"`
}

// playerReg Redis 中存储的玩家注册信息。
type playerReg struct {
	GatewayAddr string `json:"gateway_addr"`
	GameAddr  string `json:"game_addr"`
}

// startGateway 启动 Gateway 网关服务。
func startGateway(listeners []listenerCfg, redisCfg redisCfg, tokenSecret string) {
	// 初始化 Redis 客户端（gateway 独立管理，不使用 pkg.RCtx）
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisCfg.Addr,
		Password: redisCfg.PassWord,
		DB:       redisCfg.DB,
	})

	// 轮询 game 注册表，维护到各 game 的长连接
	go gamePollLoop(rdb)

	for _, lc := range listeners {
		l, err := network.NewListener(lc.Network, lc.Addr, lc.Port)
		if err != nil {
			logger.ERROR("gateway listener create failed:", lc.Network, lc.Addr, lc.Port, err)
			continue
		}
		logger.INFO("gateway listener start:", lc.Network, l.Addr())
		go serveListener(l, rdb, []byte(tokenSecret))
	}
}

// serveListener accept 客户端连接并为每个连接创建 Session。
func serveListener(l network.Listener, rdb *redis.Client, secret []byte) {
	for {
		conn, err := l.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "closed") {
				return
			}
			logger.ERROR("gateway accept error:", err)
			continue
		}
		logger.INFO("gateway accept:", conn.RemoteAddr())
		go handleClient(conn, rdb, secret)
	}
}

// handleClient 处理单个客户端连接：鉴权握手 → 在目标 game 长连接上建隧道 → 双向转发。
func handleClient(clientConn network.NetConn, rdb *redis.Client, secret []byte) {
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

	// 2. 鉴权只在此处做一次：验签（签名 + 过期），提取玩家名与 uid
	playerName, uid, err := verifyToken(hs.Token, secret)
	if err != nil {
		logger.WARNING("token verify failed:", err)
		_ = clientConn.WriteMessage([]byte(`{"status":"error","detail":"invalid token"}`))
		_ = clientConn.Close()
		return
	}
	logger.INFO("client handshake:", clientConn.RemoteAddr(), "player:", playerName, "uid:", uid)

	// 3. 查 Redis 获取目标 game 地址
	val, err := rdb.Get(rdb.Context(), "player:"+playerName).Result()
	if err != nil {
		logger.ERROR("redis get player info failed:", err)
		_ = clientConn.WriteMessage([]byte(`{"status":"error","detail":"registration expired"}`))
		_ = clientConn.Close()
		return
	}
	reg := &playerReg{}
	if err := json.Unmarshal([]byte(val), reg); err != nil {
		logger.ERROR("parse player reg failed:", err)
		_ = clientConn.Close()
		return
	}
	logger.INFO("player reg:", playerName, "game:", reg.GameAddr)

	// 4. 异地登录：同玩家已有会话则先踢旧会话
	if old, ok := sessionMap.Load(playerName); ok {
		oldSess := old.(*Session)
		logger.INFO("kick old session (relogin):", playerName)
		_ = oldSess.ClientConn.Close()
		if l, ok := gameLinks.Load(oldSess.GameAddr); ok {
			_ = l.(*gameLink).sendKick(playerName)
		}
		closeClientSession(oldSess)
	}

	// 5. 定位目标 game 长连接
	linkVal, ok := gameLinks.Load(reg.GameAddr)
	if !ok {
		logger.ERROR("game link not connected:", reg.GameAddr)
		_ = clientConn.WriteMessage([]byte(`{"status":"error","detail":"game unavailable"}`))
		_ = clientConn.Close()
		return
	}
	link := linkVal.(*gameLink)

	// 6. 在长连接上发握手帧并等待 Ack
	hsData, _ := json.Marshal(&gameHandshakeMsg{PlayerName: playerName, UID: uid, Version: hs.Version})
	ackCh := make(chan handshakeResult, 1)
	link.pending.Store(playerName, ackCh)
	if err := link.writeFrame(codec.TunnelHandshake, playerName, hsData); err != nil {
		link.pending.Delete(playerName)
		logger.ERROR("write handshake to game failed:", err)
		_ = clientConn.Close()
		return
	}
	var ack handshakeResult
	select {
	case ack = <-ackCh:
	case <-time.After(handshakeTimeout):
		link.pending.Delete(playerName)
		logger.ERROR("handshake to game timeout:", playerName)
		_ = clientConn.Close()
		return
	}
	if !ack.ok {
		logger.ERROR("game handshake failed:", playerName, ack.error)
		_ = clientConn.WriteMessage([]byte(`{"status":"error","detail":"handshake failed"}`))
		_ = clientConn.Close()
		return
	}
	// 握手成功回执（对齐客户端测试页约定的握手契约）
	if err := clientConn.WriteMessage([]byte(`{"status":"ok"}`)); err != nil {
		logger.ERROR("write handshake ok failed:", err, "player:", playerName)
		_ = clientConn.Close()
		return
	}

	// 7. 建立 session 并启动转发（game -> client 由 gameLink reader 按隧道回投）
	sess := &Session{TunnelID: playerName, UID: uid, ClientConn: clientConn, GameAddr: reg.GameAddr}
	sessionMap.Store(playerName, sess)
	link.sessions.Store(playerName, sess)
	logger.INFO("session up:", playerName, "game:", reg.GameAddr)
	defer func() {
		logger.INFO("session closed:", playerName)
		// 只有确认自己仍是当前会话时才通知 game 拆隧道，
		// 避免旧连接收尾的晚到 kick 误拆重连后新建的隧道
		if closeClientSession(sess) {
			_ = link.sendKick(playerName)
		}
	}()

	// client -> game
	for {
		data, err := clientConn.ReadMessage()
		if err != nil {
			return
		}
		logger.DEBUG("forward client->game:", len(data), "player:", playerName)
		if err := link.sendData(playerName, data); err != nil {
			return
		}
	}
}

// verifyToken 验证 JWT（签名 + 过期），返回玩家名与 uid。
func verifyToken(tokenStr string, secret []byte) (string, int64, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return "", 0, err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return "", 0, fmt.Errorf("invalid token claims")
	}
	sub, _ := claims.GetSubject()
	if sub == "" {
		return "", 0, fmt.Errorf("missing sub claim")
	}
	uid := int64(0)
	if v, ok := claims["uid"]; ok {
		switch n := v.(type) {
		case float64:
			uid = int64(n)
		case int64:
			uid = n
		}
	}
	return sub, uid, nil
}
