package pkg

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"nhooyr.io/websocket"
)

// wsConn 包装 *websocket.Conn，实现 NetConn 接口。
// 消息格式与 TCP 一致：JSON(PacketMsg)，承载在 WebSocket 文本帧中。
type wsConn struct {
	conn          *websocket.Conn
	remote        string
	deadline      time.Time
	writeDeadline time.Time
}

// defaultWSTimeout WS 读取默认超时，防止未设置 deadline 时无限阻塞。
const defaultWSTimeout = 30 * time.Second

func (c *wsConn) ReadMessage() ([]byte, error) {
	ctx := context.Background()
	if !c.deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, c.deadline)
		defer cancel()
	} else {
		// 未设置 deadline 时使用默认超时，防止无限阻塞
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultWSTimeout)
		defer cancel()
	}
	_, data, err := c.conn.Read(ctx)
	return data, err
}

func (c *wsConn) WriteMessage(data []byte) error {
	// 文本帧承载 JSON(PacketMsg)，与 routes/controller 的预期一致
	ctx := context.Background()
	if !c.writeDeadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, c.writeDeadline)
		defer cancel()
	}
	return c.conn.Write(ctx, websocket.MessageText, data)
}

func (c *wsConn) RemoteAddr() string {
	return c.remote
}

func (c *wsConn) Close() error {
	return c.conn.Close(websocket.StatusNormalClosure, "")
}

func (c *wsConn) SetReadDeadline(t time.Time) error {
	c.deadline = t
	return nil
}

func (c *wsConn) SetWriteDeadline(t time.Time) error {
	c.writeDeadline = t
	return nil
}

// wsListener 通过 http.Server + /ws 路径升级 WebSocket 连接。
type wsListener struct {
	addr     string
	server   *http.Server
	connChan chan NetConn
}

func newWSListener(addr, port string) (*wsListener, error) {
	fullAddr := fmt.Sprintf("%s:%s", addr, port)

	l := &wsListener{
		addr:     fullAddr,
		connChan: make(chan NetConn, 128),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", l.handleWS)
	mux.HandleFunc("/", l.handleWS) // 兼容根路径接入

	l.server = &http.Server{
		Addr:    fullAddr,
		Handler: mux,
	}

	ln, err := net.Listen("tcp", fullAddr)
	if err != nil {
		return nil, err
	}

	go func() {
		if err := l.server.Serve(ln); err != nil && err.Error() != "http: Server closed" {
			ERROR("ws listener serve error:", err)
		}
	}()

	return l, nil
}

// handleWS 处理 WebSocket 升级请求。
func (l *wsListener) handleWS(w http.ResponseWriter, r *http.Request) {
	// InsecureSkipVerify: 开发/测试阶段允许任意 Origin 连接，生产环境应改为 OriginPatterns 白名单
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		ERROR("websocket accept failed:", err)
		return
	}

	conn := &wsConn{
		conn:   c,
		remote: r.RemoteAddr,
	}

	select {
	case l.connChan <- conn:
	default:
		// 队列满则拒绝连接并记录日志
		ERROR("ws listener accept queue full, rejecting connection from", r.RemoteAddr)
		_ = c.Close(websocket.StatusTryAgainLater, "server busy")
	}
}

func (l *wsListener) Accept() (NetConn, error) {
	c, ok := <-l.connChan
	if !ok {
		return nil, fmt.Errorf("listener closed")
	}
	return c, nil
}

func (l *wsListener) Close() error {
	// 先 Shutdown HTTP server（停止接受新连接，等待活跃 handler 返回），
	// 再 close channel（此时已无 goroutine 在写 connChan，不会 panic）
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = l.server.Shutdown(shutdownCtx)
	close(l.connChan)
	return nil
}

func (l *wsListener) Addr() string {
	return l.addr
}
