package rpc

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
	"simple_game/game/pkg/uid"
)

var (
	// ErrTimeout 远端调用超时。
	ErrTimeout = errors.New("rpc call timeout")
	// ErrNoTransport 无 gateway 长连接可写（gateway 未连接或已断开）。
	ErrNoTransport = errors.New("rpc: no gateway transport")
)

// RemoteRouter 跨进程路由能力：由 rpc.Client 实现，注入 core.AManager。
type RemoteRouter interface {
	Call(to int64, from string, msg interface{}, timeout time.Duration) (interface{}, error)
	Cast(to int64, from string, msg interface{}) error
}

// Transport 写帧能力：由 gameserver 注入，负责把 RpcFrame 序列化字节打包为
// TunnelRPC 隧道帧写入 gateway 长连接。
type Transport interface {
	WriteFrame(payload []byte) error
}

// pendingCall 等待中的远端调用：关联回包通道与响应类型。
type pendingCall struct {
	ch      chan interface{} // 回包：响应值或 error
	resType reflect.Type     // 响应类型（可能为 nil）
}

// Client 远端 RPC 客户端：单例，生命周期跟随 game Server。
// 持有到 gateway 的写路径（transport），维护 reqID -> pending 关联表。
type Client struct {
	node string // 本节点地址（origin_node）

	mu        sync.RWMutex
	transport Transport

	pending sync.Map // reqID(string) -> *pendingCall
}

// NewClient 构造 RPC 客户端；node 为本节点在注册表中的地址。
func NewClient(node string) *Client {
	return &Client{node: node}
}

// SetTransport 设置当前 gateway 长连接写路径（gateway 连接建立时调用）。
func (c *Client) SetTransport(t Transport) {
	c.mu.Lock()
	c.transport = t
	c.mu.Unlock()
}

// OnDisconnect gateway 长连接断开：清空 transport 并使所有等待中的调用失败返回。
func (c *Client) OnDisconnect() {
	c.mu.Lock()
	c.transport = nil
	c.mu.Unlock()

	c.pending.Range(func(k, v interface{}) bool {
		id := k.(string)
		pc := v.(*pendingCall)
		select {
		case pc.ch <- fmt.Errorf("rpc: transport disconnected"):
		default:
		}
		c.pending.Delete(id)
		return true
	})
}

func (c *Client) getTransport() Transport {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.transport
}

// Call 远端同步调用：marshal 请求、登记 pending、写帧、阻塞等回包。
func (c *Client) Call(to int64, from string, msg interface{}, timeout time.Duration) (interface{}, error) {
	method, err := MethodOf(msg)
	if err != nil {
		return nil, err
	}
	e, err := lookup(method)
	if err != nil {
		return nil, err
	}
	payload, err := proto.Marshal(msg.(proto.Message))
	if err != nil {
		return nil, err
	}

	reqID := strconv.FormatInt(uid.NewUid(), 10)
	pc := &pendingCall{ch: make(chan interface{}, 1), resType: e.resType}
	c.pending.Store(reqID, pc)

	frame := &pt.RpcFrame{
		Kind:        pt.RpcKind_RPC_REQUEST,
		ReqId:       reqID,
		UID:         to,
		Method:      method,
		OriginNode:  c.node,
		OriginActor: from,
		Payload:     payload,
		IsCall:      true,
		TimeoutMs:   timeout.Milliseconds(),
	}
	if err := c.write(frame); err != nil {
		c.pending.Delete(reqID)
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case resp := <-pc.ch:
		if e, ok := resp.(error); ok {
			return nil, e
		}
		return resp, nil
	case <-timer.C:
		c.pending.Delete(reqID)
		return nil, ErrTimeout
	}
}

// Cast 远端异步投递：marshal 请求并写帧，不等回包。
func (c *Client) Cast(to int64, from string, msg interface{}) error {
	method, err := MethodOf(msg)
	if err != nil {
		return err
	}
	payload, err := proto.Marshal(msg.(proto.Message))
	if err != nil {
		return err
	}

	frame := &pt.RpcFrame{
		Kind:        pt.RpcKind_RPC_REQUEST,
		ReqId:       strconv.FormatInt(uid.NewUid(), 10),
		UID:         to,
		Method:      method,
		OriginNode:  c.node,
		OriginActor: from,
		Payload:     payload,
		IsCall:      false,
	}
	return c.write(frame)
}

func (c *Client) write(frame *pt.RpcFrame) error {
	t := c.getTransport()
	if t == nil {
		return ErrNoTransport
	}
	data, err := MarshalFrame(frame)
	if err != nil {
		return err
	}
	return t.WriteFrame(data)
}

// OnResponse 处理到达的 RPC 响应：按 reqID 唤醒等待中的调用。
// 迟到的回包（已超时清理）静默丢弃。
func (c *Client) OnResponse(frame *pt.RpcFrame) {
	v, ok := c.pending.Load(frame.ReqId)
	if !ok {
		return
	}
	pc := v.(*pendingCall)
	c.pending.Delete(frame.ReqId)

	if frame.Error != "" {
		pc.ch <- fmt.Errorf("rpc: %s", frame.Error)
		return
	}
	if pc.resType == nil || len(frame.Payload) == 0 {
		pc.ch <- nil
		return
	}
	resp := reflect.New(pc.resType).Interface().(proto.Message)
	if err := proto.Unmarshal(frame.Payload, resp); err != nil {
		pc.ch <- fmt.Errorf("rpc: unmarshal response: %w", err)
		return
	}
	pc.ch <- resp
}
