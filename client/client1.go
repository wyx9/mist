package main

import (
	"fmt"
	"sync/atomic"
	"time"

	"simple_game/game/api/protos/pt"
	"simple_game/game/pkg/network"
	"simple_game/game/pkg/protocol"
)

// 心跳相关常量
const (
	HeartbeatInterval = 3 * time.Second  // 发送心跳包的间隔
	ClientTimeOut     = 30 * time.Second // 客户端超时时间
)

// 客户端状态
var (
	lastHeartbeatResponse int64 // 最后一次收到心跳响应的时间戳
	isConnected           int32 // 连接状态标志
)

// Reader 客户端读取消息
func Reader(conn network.NetConn) {
	defer conn.Close()
	for {
		data, err := conn.ReadMessage()
		if err != nil {
			atomic.StoreInt32(&isConnected, 0) // 标记连接已断开
			fmt.Println("连接断开:", err)
			return
		}

		// 更新最后心跳响应时间
		atomic.StoreInt64(&lastHeartbeatResponse, time.Now().Unix())
		cmd, body, err := protocol.DecodeFrame(data)
		if err != nil {
			fmt.Println("解码帧失败:", err)
			continue
		}
		fmt.Printf("收到消息：cmd=%d body=%d 字节\n", cmd, len(body))
	}
}

// sendHeartbeat 发送心跳包
func sendHeartbeat(conn network.NetConn) {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()

	for range ticker.C {
		if atomic.LoadInt32(&isConnected) == 0 {
			return
		}

		// 创建心跳请求
		packBy, err := protocol.PackCmd(protocol.CmdHeart, &pt.HeartReq{})
		if err != nil {
			fmt.Println("打包心跳失败:", err)
			continue
		}

		// 发送心跳包
		if err := conn.WriteMessage(packBy); err != nil {
			fmt.Println("发送心跳包失败:", err)
			continue
		}

		// 检查是否超时
		if time.Now().Unix()-atomic.LoadInt64(&lastHeartbeatResponse) > int64(ClientTimeOut.Seconds()) {
			fmt.Println("心跳超时，断开连接")
			atomic.StoreInt32(&isConnected, 0)
			_ = conn.Close()
			return
		}
	}
}

// StartClient 启动客户端，断线自动重连
func StartClient(networkName, address string) {
	for {
		if err := connectAndServe(networkName, address); err != nil {
			fmt.Println("连接失败:", err)
			time.Sleep(5 * time.Second) // 重连延迟
			continue
		}

		if atomic.LoadInt32(&isConnected) == 0 {
			continue // 如果连接断开，尝试重连
		}
	}
}

// connectAndServe 连接服务器并处理登录/心跳
func connectAndServe(networkName, address string) error {
	conn, err := network.Dial(networkName, address)
	if err != nil {
		return fmt.Errorf("连接服务器失败: %v", err)
	}

	fmt.Println("已连接到服务器")
	atomic.StoreInt32(&isConnected, 1)
	atomic.StoreInt64(&lastHeartbeatResponse, time.Now().Unix())

	// 启动读取协程
	go Reader(conn)
	// 启动心跳协程
	go sendHeartbeat(conn)

	defer conn.Close()

	// 主消息循环
	for atomic.LoadInt32(&isConnected) == 1 {
		var msg string
		_, _ = fmt.Scanln(&msg)

		if atomic.LoadInt32(&isConnected) == 0 {
			break
		}

		// 创建登录请求
		req := pt.LoginReq{
			UUid:     12312312,
			Name:     msg,
			PassWord: "123",
		}
		packBy, err := protocol.PackCmd(protocol.CmdLogin, &req)
		if err != nil {
			fmt.Println("打包登录请求失败:", err)
			continue
		}

		// 发送消息
		if err := conn.WriteMessage(packBy); err != nil {
			fmt.Println("发送消息失败:", err)
			atomic.StoreInt32(&isConnected, 0)
			break
		}
	}

	return nil
}

func main() {
	StartClient("tcp", "127.0.0.1:8888")
}
