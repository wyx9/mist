package rpc_test

import (
	"bufio"
	"net"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
	"simple_game/game/internal/actor/core"
	"simple_game/game/internal/rpc"
	"simple_game/game/pkg/codec"
)

// testActor 测试用 actor，实现 core.IActor。
type testActor struct{}

func (a *testActor) Start()           {}
func (a *testActor) Stop()            {}
func (a *testActor) Category() string { return "test" }

// tcpTransport 实现 rpc.Transport：把 RpcFrame 序列化字节经 4B 长度头写入 TCP。
type tcpTransport struct{ conn net.Conn }

func (t *tcpTransport) WriteFrame(payload []byte) error {
	return codec.SendData(t.conn, payload)
}

// startRPCServer 启动接收侧 RPC 服务（TCP 监听 + rpc.Serve 分发到本地 actor），返回监听地址。
func startRPCServer(t *testing.T, am *core.AManager) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		rd := bufio.NewReader(conn)
		for {
			data, err := codec.RecvFrame(rd)
			if err != nil {
				return
			}
			frame := &pt.RpcFrame{}
			if err := proto.Unmarshal(data, frame); err != nil {
				return
			}
			if err := rpc.Serve(am, frame, func(resp *pt.RpcFrame) error {
				b, err := proto.Marshal(resp)
				if err != nil {
					return err
				}
				return codec.SendData(conn, b)
			}); err != nil {
				t.Logf("rpc serve: %v", err)
			}
		}
	}()
	return ln.Addr().String()
}

// newTestClient 构造连到指定地址的 RPC 客户端，并启动回包读取协程。
func newTestClient(t *testing.T, addr string) *rpc.Client {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client := rpc.NewClient("test-node")
	client.SetTransport(&tcpTransport{conn: conn})
	go func() {
		rd := bufio.NewReader(conn)
		for {
			data, err := codec.RecvFrame(rd)
			if err != nil {
				return
			}
			frame := &pt.RpcFrame{}
			if err := proto.Unmarshal(data, frame); err != nil {
				return
			}
			client.OnResponse(frame)
		}
	}()
	return client
}

// TestRemoteCallAndSend 验证跨进程 RPC 的完整收发链路：
// 客户端 rpc.Client.Call/Send → TCP → 接收侧 rpc.Serve → AManager → 注册的 handler → 回包。
func TestRemoteCallAndSend(t *testing.T) {
	am := &core.AManager{}
	if _, err := core.StartActor(am, "echo", &testActor{}); err != nil {
		t.Fatalf("start actor: %v", err)
	}

	// 同步（Call）handler
	rpc.RegisterCall[*testActor, *pt.RoomGetPlayersReq, *pt.RoomGetPlayersRes]("test.echo",
		func(ctx *rpc.HandlerContext[*testActor], req *pt.RoomGetPlayersReq) (*pt.RoomGetPlayersRes, error) {
			return &pt.RoomGetPlayersRes{Players: []*pt.RoomPlayerInfo{{UID: 42, Name: "echo"}}}, nil
		})

	// 异步（Send）handler
	noted := make(chan string, 1)
	rpc.Register[*testActor, *pt.RoomPlayerJoin]("test.note",
		func(ctx *rpc.HandlerContext[*testActor], req *pt.RoomPlayerJoin) error {
			noted <- "ok"
			return nil
		})

	addr := startRPCServer(t, am)
	client := newTestClient(t, addr)

	// 1. 同步远程调用：应拿到类型化响应
	res, err := client.Call("echo", "client", &pt.RoomGetPlayersReq{}, 3*time.Second)
	if err != nil {
		t.Fatalf("remote call: %v", err)
	}
	got, ok := res.(*pt.RoomGetPlayersRes)
	if !ok {
		t.Fatalf("reply type: %T", res)
	}
	if len(got.Players) != 1 || got.Players[0].UID != 42 || got.Players[0].Name != "echo" {
		t.Fatalf("unexpected reply: %+v", got)
	}

	// 2. 异步远程投递：应触发对端 handler
	if err := client.Cast("echo", "client", &pt.RoomPlayerJoin{UID: 1, Name: "x"}); err != nil {
		t.Fatalf("remote cast: %v", err)
	}
	select {
	case v := <-noted:
		if v != "ok" {
			t.Fatalf("unexpected note: %s", v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("send handler not invoked")
	}

	// 3. 目标 actor 不存在：应收到服务端错误回包（而非超时）
	if _, err := client.Call("nobody", "client", &pt.RoomGetPlayersReq{}, 2*time.Second); err == nil {
		t.Fatal("expected error for missing actor, got nil")
	} else if err == rpc.ErrTimeout {
		t.Fatal("expected server error, got timeout")
	}
}
