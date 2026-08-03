package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"simple_game/game/api/protos/pt"
	"simple_game/game/pkg/network"
	"simple_game/game/pkg/protocol"
)

// 统一测试客户端 — 支持 TCP 和 WebSocket，复用 NetConn 抽象。
//
// 用法:
//
//	go run ./client/unified/ --net=tcp --addr=127.0.0.1:8888
//	go run ./client/unified/ --net=ws  --addr=127.0.0.1:8889
//
// 交互命令:
//
//	/login <name>  — 发送 LoginReq
//	/quit          — 退出
//	/status        — 查看连接状态
//	任意文本        — 发送 LoginReq（Name=输入文本）

const (
	heartbeatInterval = 3 * time.Second
	heartbeatTimeout  = 30 * time.Second
	reconnectDelay    = 5 * time.Second
	dialTimeout       = 5 * time.Second
	readTimeout       = 35 * time.Second
)

var (
	netFlag = flag.String("net", "tcp", "网络协议: tcp 或 ws")
	addr    = flag.String("addr", "127.0.0.1:8888", "服务器地址 (host:port)")

	lastHeartbeatResp int64 // 最后一次心跳响应时间戳
	isConnected       int32 // 连接状态 0=断开 1=正常
	actorID           = fmt.Sprintf("test_%d", time.Now().Unix())
)

func main() {
	flag.Parse()

	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║   Simple Game - 统一测试客户端          ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Printf("协议: %s  地址: %s  角色ID: %s\n\n", *netFlag, *addr, actorID)

	// 信号处理
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 后台自动重连
	go autoReconnect()

	// 主控循环：读取控制台输入
	go inputLoop()

	<-sigChan
	fmt.Println("\n收到退出信号，关闭客户端...")
	atomic.StoreInt32(&isConnected, 0)
	os.Exit(0)
}

// autoReconnect 自动连接/重连循环
func autoReconnect() {
	for {
		if atomic.LoadInt32(&isConnected) == 1 {
			time.Sleep(1 * time.Second)
			continue
		}

		fmt.Printf("[重新连接] 正在连接 %s://%s ...\n", *netFlag, *addr)
		conn, err := network.Dial(*netFlag, *addr)
		if err != nil {
			fmt.Printf("[错误] 连接失败: %v，%v 后重试\n", err, reconnectDelay)
			time.Sleep(reconnectDelay)
			continue
		}

		fmt.Println("[连接成功] 已连接到服务器")
		atomic.StoreInt32(&isConnected, 1)
		atomic.StoreInt64(&lastHeartbeatResp, time.Now().Unix())

		// 启动消息读取和心跳
		done := make(chan struct{})
		go reader(conn, done)
		go heartbeat(conn, done)

		// 等待连接断开
		<-done
		atomic.StoreInt32(&isConnected, 0)
		_ = conn.Close()
		fmt.Println("[断开] 连接已断开，准备重连...")
	}
}

// reader 读取服务端回包
func reader(conn network.NetConn, done chan struct{}) {
	defer close(done)

	for atomic.LoadInt32(&isConnected) == 1 {
		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
		data, err := conn.ReadMessage()
		if err != nil {
			fmt.Printf("[读取错误] %v\n", err)
			return
		}
		atomic.StoreInt64(&lastHeartbeatResp, time.Now().Unix())
		cmd, body, err := protocol.DecodeFrame(data)
		if err != nil {
			fmt.Printf("[解码错误] %v\n", err)
			continue
		}
		fmt.Printf("[收到] cmd=%d body=%d 字节\n", cmd, len(body))
	}
}

// heartbeat 定时发送心跳
func heartbeat(conn network.NetConn, done chan struct{}) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if atomic.LoadInt32(&isConnected) == 0 {
				return
			}

			pack, err := protocol.PackCmd(protocol.CmdHeart, &pt.HeartReq{})
			if err != nil {
				fmt.Printf("[心跳错误] 打包失败: %v\n", err)
				continue
			}
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := conn.WriteMessage(pack); err != nil {
				fmt.Printf("[心跳错误] 发送失败: %v\n", err)
				return
			}

			// 检查心跳超时
			ago := time.Now().Unix() - atomic.LoadInt64(&lastHeartbeatResp)
			if ago > int64(heartbeatTimeout.Seconds()) {
				fmt.Println("[心跳超时] 服务器无响应")
				return
			}
		}
	}
}

// inputLoop 控制台输入循环
func inputLoop() {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("输入 /login <name> 登录，/quit 退出，/status 查看状态")
	fmt.Print("或直接输入文本发送 LoginReq\n\n")

	for scanner.Scan() {
		text := scanner.Text()
		if text == "" {
			continue
		}

		switch {
		case text == "/quit":
			atomic.StoreInt32(&isConnected, 0)
			fmt.Println("退出...")
			os.Exit(0)

		case text == "/status":
			printStatus()

		case len(text) > 6 && text[:7] == "/login ":
			sendLogin(text[7:])

		default:
			sendLogin(text)
		}
	}
}

// sendLogin 发送 LoginReq
func sendLogin(name string) {
	if atomic.LoadInt32(&isConnected) == 0 {
		fmt.Println("[错误] 未连接到服务器")
		return
	}

	req := &pt.LoginReq{
		UUid:     12312312,
		Name:     name,
		PassWord: "123",
	}
	pack, err := protocol.PackCmd(protocol.CmdLogin, req)
	if err != nil {
		fmt.Printf("[登录错误] 打包失败: %v\n", err)
		return
	}

	// LoginReq 需要新建连接发送（服务端要求首包必须是 LoginReq）
	conn, err := network.Dial(*netFlag, *addr)
	if err != nil {
		fmt.Printf("[登录错误] 连接失败: %v\n", err)
		return
	}

	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := conn.WriteMessage(pack); err != nil {
		fmt.Printf("[登录错误] 发送失败: %v\n", err)
		_ = conn.Close()
		return
	}

	// 读取登录响应
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	data, err := conn.ReadMessage()
	if err != nil {
		fmt.Printf("[登录错误] 读取响应失败: %v\n", err)
		_ = conn.Close()
		return
	}
	cmd, _, err := protocol.DecodeFrame(data)
	if err != nil {
		fmt.Printf("[登录成功] 响应解码失败: %v\n", err)
		_ = conn.Close()
		return
	}
	fmt.Printf("[登录成功] 响应 cmd=%d\n", cmd)
	_ = conn.Close()
}

// printStatus 打印当前连接状态
func printStatus() {
	status := "断开"
	if atomic.LoadInt32(&isConnected) == 1 {
		status = "已连接"
	}
	lastHb := time.Unix(atomic.LoadInt64(&lastHeartbeatResp), 0).Format("15:04:05")
	fmt.Printf("协议: %s  地址: %s  状态: %s  最后心跳: %s\n",
		*netFlag, *addr, status, lastHb)
}
