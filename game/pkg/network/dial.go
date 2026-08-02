package pkg

import (
	"context"
	"fmt"
	"net"
	"time"

	"nhooyr.io/websocket"
)

// Dial 根据协议类型创建客户端 NetConn 连接。
// network: "tcp" 或 "ws"
// addr: 对于 tcp 为 "host:port"；对于 ws 为 "host:port"
func Dial(network, addr string) (NetConn, error) {
	switch network {
	case "tcp":
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			return nil, fmt.Errorf("tcp dial %s: %w", addr, err)
		}
		return &tcpConn{conn: conn}, nil
	case "ws", "websocket":
		url := fmt.Sprintf("ws://%s/ws", addr)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, _, err := websocket.Dial(ctx, url, nil)
		if err != nil {
			return nil, fmt.Errorf("ws dial %s: %w", url, err)
		}
		return &wsConn{
			conn:   c,
			remote: addr,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported network: %s", network)
	}
}
