// internal/gameserver/network.go
// 网络接入与会话隧道：监听 accept、Agent 连接握手与消息读循环、隧道回包。
package gameserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"simple_game/game/pkg/codec"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/network"
)

// serve 监听 Agent 连接并分发给 handler。
func (s *Server) serve(addr string, tokenSecret string) {
	parts := strings.Split(addr, ":")
	if len(parts) != 2 {
		logger.ERROR("invalid game listen addr:", addr)
		return
	}

	l, err := network.NewListener("tcp", parts[0], parts[1])
	if err != nil {
		logger.ERROR("game listener create failed:", err)
		return
	}
	s.listener = l
	logger.INFO("game server listening on", addr)

	for {
		conn, err := l.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "closed") {
				return
			}
			logger.ERROR("game accept error:", err)
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleAgentConn(conn, []byte(tokenSecret))
		}()
	}
}

// handshakeMsg 客户端握手消息。
type handshakeMsg struct {
	Token   string `json:"token"`
	Version int    `json:"version"`
}

// handleAgentConn 处理单条 Agent 隧道连接。
func (s *Server) handleAgentConn(conn network.NetConn, secret []byte) {
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	data, err := conn.ReadMessage()
	if err != nil {
		logger.ERROR("read handshake from agent failed:", err)
		return
	}

	hs := &handshakeMsg{}
	if err := json.Unmarshal(data, hs); err != nil {
		logger.ERROR("parse handshake failed:", err)
		return
	}

	playerName, err := s.verifyToken(hs.Token, secret)
	if err != nil {
		logger.ERROR("token verify failed:", err)
		_ = conn.WriteMessage([]byte(`{"error":"invalid token"}`))
		return
	}

	tunnelID := playerName
	s.agentConns.Store(tunnelID, conn)
	defer func() {
		_ = s.ActorManner.CloseActor(tunnelID, 5*time.Second)
		s.agentConns.Delete(tunnelID)
	}()

	_, err = s.ActorManner.StartPlayer(tunnelID, conn)
	if err != nil {
		logger.ERROR("start player actor failed:", err)
		return
	}

	_ = conn.WriteMessage(codec.PackTunnelRaw("HandshakeOk", nil, tunnelID))

	for {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if err := s.handleMsg(data, tunnelID); err != nil {
			logger.ERROR("handleMsg error:", err)
		}
	}
}

// ReplyToAgent 通过隧道回复 Agent。
func (s *Server) ReplyToAgent(tunnelID string, data []byte) error {
	val, ok := s.agentConns.Load(tunnelID)
	if !ok {
		return fmt.Errorf("agent conn not found: %s", tunnelID)
	}
	return val.(network.NetConn).WriteMessage(data)
}
