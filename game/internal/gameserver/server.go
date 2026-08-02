// game/server.go
package engine

import (
	"encoding/json"
	"fmt"
	"simple_game/game/pkg"
	"simple_game/game/tunnel"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Game 配置（在 main.go 中从 config 文件加载）
var (
	GameListenAddr string
	TokenSecret    []byte
	agentConnMap   = make(map[string]pkg.NetConn) // tunnelID → Agent 连接
)

// 握手消息结构（对应 spec 5.1b）
type handshakeMsg struct {
	Token   string `json:"token"`
	Version int    `json:"version"`
}

// Start 启动 Game 服务：监听 Agent 连接。
func Start(addr string, tokenSecret string) {
	GameListenAddr = addr
	TokenSecret = []byte(tokenSecret)

	// 解析 addr
	parts := strings.Split(addr, ":")
	if len(parts) != 2 {
		pkg.ERROR("invalid game listen addr:", addr)
		return
	}

	l, err := pkg.NewListener("tcp", parts[0], parts[1])
	if err != nil {
		pkg.ERROR("game listener create failed:", err)
		return
	}
	pkg.INFO("game server listening on", addr)

	for {
		conn, err := l.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "closed") {
				return
			}
			pkg.ERROR("game accept error:", err)
			continue
		}
		go handleAgentConn(conn)
	}
}

// handleAgentConn 处理单条 Agent 隧道连接。
func handleAgentConn(conn pkg.NetConn) {
	defer conn.Close()

	// 1. 读取握手消息（token + tunnelID 信息已由 Agent 在隧道首包中发送）
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	data, err := conn.ReadMessage()
	if err != nil {
		pkg.ERROR("read handshake from agent failed:", err)
		return
	}

	// 解析握手：{"token":"...", "version":1}
	hs := &handshakeMsg{}
	if err := json.Unmarshal(data, hs); err != nil {
		pkg.ERROR("parse handshake failed:", err)
		return
	}

	// 2. 验证 token
	playerName, err := verifyToken(hs.Token, TokenSecret)
	if err != nil {
		pkg.ERROR("token verify failed:", err)
		_ = conn.WriteMessage([]byte(`{"error":"invalid token"}`))
		return
	}

	// 3. 注册 tunnelID → conn 映射（Agent 以 playerName 作为 tunnelID）
	tunnelID := playerName
	agentConnMap[tunnelID] = conn
	defer func() {
		ActorManner.FindAndClosePlayer(tunnelID)
		delete(agentConnMap, tunnelID)
	}()

	// 4. 加载玩家数据 + 启动 Actor
	_, err = StartNewPlayerActor(tunnelID, conn)
	if err != nil {
		pkg.ERROR("start player actor failed:", err)
		return
	}

	// 5. 回复 Agent 握手成功
	_ = conn.WriteMessage(tunnel.PackTunnelRaw("HandshakeOk", nil, tunnelID))

	// 6. 主消息循环
	for {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if err := handleMsg(conn, data, tunnelID); err != nil {
			pkg.ERROR("handleMsg error:", err)
		}
	}
}

// verifyToken 验证 JWT token，返回 player_name。
func verifyToken(tokenStr string, secret []byte) (string, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return "", err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return "", fmt.Errorf("invalid token claims")
	}
	sub, _ := claims.GetSubject()
	if sub == "" {
		return "", fmt.Errorf("missing sub claim")
	}
	return sub, nil
}
