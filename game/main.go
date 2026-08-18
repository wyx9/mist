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
	machineID := flag.Int64("machineid", -1, "机器 id（保留兼容；uid 由 Redis 全局计数生成，已不参与）")
	flag.Parse()

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
