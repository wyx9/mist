package pkg

import (
	"fmt"
	"net"
	"time"
)

// NetConn 统一连接接口，屏蔽不同协议（TCP/WebSocket/KCP...）的读写差异。
// 业务层只面向 []byte，不关心底层分帧方式。
type NetConn interface {
	// ReadMessage 读取一条完整的业务消息（已分帧）
	ReadMessage() ([]byte, error)
	// WriteMessage 写入一条完整的业务消息（已组帧）
	WriteMessage([]byte) error
	// RemoteAddr 返回对端地址字符串
	RemoteAddr() string
	// Close 关闭连接
	Close() error
	// SetReadDeadline 设置读取截止时间，超时后 ReadMessage 返回错误
	SetReadDeadline(t time.Time) error
	// SetWriteDeadline 设置写入截止时间，超时后 WriteMessage 返回错误
	SetWriteDeadline(t time.Time) error
}

// Listener 统一接入层接口，屏蔽不同协议的 Accept 差异。
type Listener interface {
	// Accept 阻塞等待并返回一个新的连接
	Accept() (NetConn, error)
	// Close 关闭监听器
	Close() error
	// Addr 返回监听地址描述
	Addr() string
}

// NewListener 根据协议类型创建对应的 Listener 实例。
// network/addr/port 直接以字符串传入，避免与 config 包的类型耦合。
func NewListener(network, addr, port string) (Listener, error) {
	switch network {
	case "tcp":
		return newTCPListener(addr, port)
	case "ws", "websocket":
		return newWSListener(addr, port)
	default:
		return nil, fmt.Errorf("unsupported network: %s", network)
	}
}

// ---------- tcpConn / tcpListener ----------

// tcpConn 包装 net.Conn，复用现有的 4 字节大端长度头分包逻辑（pkg.RecvData / pkg.SendData）。
type tcpConn struct {
	conn          net.Conn
	writeDeadline time.Time
}

func (c *tcpConn) ReadMessage() ([]byte, error) {
	return RecvData(c.conn)
}

func (c *tcpConn) WriteMessage(data []byte) error {
	if !c.writeDeadline.IsZero() {
		_ = c.conn.SetWriteDeadline(c.writeDeadline)
		defer c.conn.SetWriteDeadline(time.Time{})
	}
	return SendData(c.conn, data)
}

func (c *tcpConn) RemoteAddr() string {
	return c.conn.RemoteAddr().String()
}

func (c *tcpConn) Close() error {
	return c.conn.Close()
}

func (c *tcpConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *tcpConn) SetWriteDeadline(t time.Time) error {
	c.writeDeadline = t
	return nil
}

// tcpListener 包装 net.Listener。
type tcpListener struct {
	addr     string
	listener net.Listener
}

func newTCPListener(addr, port string) (*tcpListener, error) {
	fullAddr := fmt.Sprintf("%s:%s", addr, port)
	l, err := net.Listen("tcp", fullAddr)
	if err != nil {
		return nil, err
	}
	return &tcpListener{addr: fullAddr, listener: l}, nil
}

func (l *tcpListener) Accept() (NetConn, error) {
	c, err := l.listener.Accept()
	if err != nil {
		return nil, err
	}
	return &tcpConn{conn: c}, nil
}

func (l *tcpListener) Close() error {
	return l.listener.Close()
}

func (l *tcpListener) Addr() string {
	return l.addr
}
