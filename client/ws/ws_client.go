package main

import (
	"fmt"
	"sync/atomic"
	"time"

	"simple_game/game/api/protos/pt"
	"simple_game/game/pkg/network"
	"simple_game/game/pkg/protocol"
)

// WebSocket 测试客户端，业务帧格式与 TCP 一致：[2B cmd][protobuf]，
// 承载在 WS 二进制帧中（对应服务端 wsConn.MessageBinary）。
//
// 运行：go run ./client/ws

const (
	serverAddr      = "127.0.0.1:8889"
	heartbeatPeriod = 3 * time.Second  // 心跳间隔
	clientTimeout   = 30 * time.Second // 心跳超时
)

var (
	lastHeartbeatResp int64
	isConnected       int32
)

func main() {
	for {
		if err := connectAndServe(); err != nil {
			fmt.Println("连接失败:", err)
			time.Sleep(5 * time.Second)
			continue
		}
		if atomic.LoadInt32(&isConnected) == 0 {
			continue
		}
	}
}

func connectAndServe() error {
	conn, err := network.Dial("ws", serverAddr)
	if err != nil {
		return fmt.Errorf("连接服务器失败: %v", err)
	}
	defer conn.Close()

	fmt.Println("已连接到服务器")
	atomic.StoreInt32(&isConnected, 1)
	atomic.StoreInt64(&lastHeartbeatResp, time.Now().Unix())

	// 读取协程
	go reader(conn)
	// 心跳协程
	go sendHeartbeat(conn)

	// 主循环：从控制台读取输入，发送 LoginReq
	for atomic.LoadInt32(&isConnected) == 1 {
		var msg string
		_, _ = fmt.Scanln(&msg)

		if atomic.LoadInt32(&isConnected) == 0 {
			break
		}

		req := &pt.LoginReq{
			UUid:     12312312,
			Name:     msg,
			PassWord: "123",
		}
		packBy, err := protocol.PackCmd(protocol.CmdLogin, req)
		if err != nil {
			fmt.Println("打包登录请求失败:", err)
			continue
		}

		if err := conn.WriteMessage(packBy); err != nil {
			fmt.Println("发送消息失败:", err)
			atomic.StoreInt32(&isConnected, 0)
			break
		}
	}
	return nil
}

func reader(conn network.NetConn) {
	for {
		data, err := conn.ReadMessage()
		if err != nil {
			atomic.StoreInt32(&isConnected, 0)
			fmt.Println("连接断开:", err)
			return
		}
		atomic.StoreInt64(&lastHeartbeatResp, time.Now().Unix())

		cmd, body, err := protocol.DecodeFrame(data)
		if err != nil {
			fmt.Println("解码帧失败:", err)
			continue
		}
		fmt.Printf("收到消息：cmd=%d body=%d 字节\n", cmd, len(body))
	}
}

func sendHeartbeat(conn network.NetConn) {
	ticker := time.NewTicker(heartbeatPeriod)
	defer ticker.Stop()

	for range ticker.C {
		if atomic.LoadInt32(&isConnected) == 0 {
			return
		}

		packBy, err := protocol.PackCmd(protocol.CmdHeart, &pt.HeartReq{})
		if err != nil {
			fmt.Println("打包心跳失败:", err)
			continue
		}

		if err := conn.WriteMessage(packBy); err != nil {
			fmt.Println("发送心跳包失败:", err)
			continue
		}

		if time.Now().Unix()-atomic.LoadInt64(&lastHeartbeatResp) > int64(clientTimeout.Seconds()) {
			fmt.Println("心跳超时，断开连接")
			atomic.StoreInt32(&isConnected, 0)
			_ = conn.Close()
			return
		}
	}
}
