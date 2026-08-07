// gateway/main.go
package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"gopkg.in/yaml.v3"

	"simple_game/game/pkg/logger"
)

type gatewayConfig struct {
	Listeners   []listenerCfg `yaml:"Listeners"`
	TokenSecret string        `yaml:"TokenSecret"`
	Redis       redisCfg      `yaml:"Redis"`
}

type listenerCfg struct {
	Network string `yaml:"Network"`
	Addr    string `yaml:"Addr"`
	Port    string `yaml:"Port"`
}

type redisCfg struct {
	Addr     string `yaml:"Addr"`
	PassWord string `yaml:"PassWord"`
	DB       int    `yaml:"DB"`
}

func main() {
	data, err := os.ReadFile(findConfig("gateway"))
	if err != nil {
		fmt.Println("load config failed:", err)
		os.Exit(1)
	}
	cfg := &gatewayConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		fmt.Println("parse config failed:", err)
		os.Exit(1)
	}

	// 启动日志消费，否则 gateway 的日志不会输出到控制台
	logger.StartLog()

	startGateway(cfg.Listeners, cfg.Redis, cfg.TokenSecret)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	fmt.Println("Gateway server started")
	<-sigChan
	fmt.Println("Gateway server shutting down...")
}

// findConfig 探测配置文件路径，支持从仓库根目录、服务目录或可执行文件所在目录启动。
func findConfig(name string) string {
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	candidates := []string{
		filepath.Join(name, "config", "config.yaml"),
		filepath.Join("config", "config.yaml"),
		filepath.Join(exeDir, "config", "config.yaml"),
		filepath.Join(exeDir, name, "config", "config.yaml"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return candidates[0]
}
