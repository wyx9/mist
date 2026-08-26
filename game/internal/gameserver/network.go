// internal/gameserver/network.go
// 网络接入：监听 Gateway 长连接，隧道帧分发见 tunnel.go。
package gameserver

import (
	"strings"

	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/network"
)

// serve 监听 Gateway 长连接并分发给 handler。
func (s *Server) serve(addr string) {
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
		go s.acceptGatewayConn(conn)
	}
}

// acceptGatewayConn 处理一条新接入的 gateway 长连接（goroutine 入口，随 wg 管理生命周期）。
func (s *Server) acceptGatewayConn(conn network.NetConn) {
	defer s.wg.Done()
	link := &gatewayLink{conn: conn}
	s.links.Store(link, link)
	s.handleGatewayLink(link)
}
