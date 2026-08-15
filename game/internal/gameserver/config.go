// game/config.go
package gameserver

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type GameConfig struct {
	Listen struct {
		Addr string `yaml:"Addr"`
		Port string `yaml:"Port"`
	} `yaml:"Listen"`
	Grpc struct {
		Addr string `yaml:"Addr"`
		Port string `yaml:"Port"`
	} `yaml:"Grpc"`
	SaveLog      bool   `yaml:"SaveLog"`
	TokenSecret  string `yaml:"TokenSecret"`
	RegisterAddr string `yaml:"RegisterAddr"`
	MySql        struct {
		Addr     string `yaml:"Addr"`
		Port     int    `yaml:"Port"`
		User     string `yaml:"User"`
		PassWord string `yaml:"PassWord"`
		DBName   string `yaml:"DBName"`
	} `yaml:"MySql"`
	Redis struct {
		Addr     string `yaml:"Addr"`
		PassWord string `yaml:"PassWord"`
		DB       int    `yaml:"DB"`
	} `yaml:"Redis"`
}

// StartOptions Start 的启动参数。字段留空时行为与原有逻辑完全一致（自动探测配置、无覆盖）。
type StartOptions struct {
	ConfigPath   string // 配置文件路径；空则自动探测（findConfigPath）
	ListenPort   string // 覆盖 Listen.Port；空则不覆盖
	RegisterAddr string // 覆盖 RegisterAddr；空则不覆盖
}

func LoadGameConfig(path string) (*GameConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &GameConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// findConfigPath 探测配置文件路径，支持从仓库根目录、服务目录或可执行文件所在目录启动。
func findConfigPath(name string) string {
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
