// game/main.go
package main

import (
	"flag"
	"fmt"
	"os"

	"simple_game/game/internal/gameserver"
	"simple_game/game/pkg/uid"
)

func main() {
	configPath := flag.String("config", "", "配置文件路径（空则自动探测）")
	port := flag.String("port", "", "覆盖监听端口 Listen.Port")
	registerAddr := flag.String("register", "", "覆盖注册地址 RegisterAddr")
	machineID := flag.Int64("machineid", -1, "雪花机器 id（0-1023；-1=使用本机 IP 推导，默认）")
	flag.Parse()

	// 同机多实例必须使用不同 machineID，避免雪花 ID 冲突
	if *machineID >= 0 {
		uid.SetMachineId(*machineID)
	}

	srv := gameserver.NewServer()
	err := srv.Start(gameserver.StartOptions{
		ConfigPath:   *configPath,
		ListenPort:   *port,
		RegisterAddr: *registerAddr,
	})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "start failed:", err)
		os.Exit(1)
	}
}
