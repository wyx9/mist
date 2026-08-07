// webapi/main.go
package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
	"gopkg.in/yaml.v3"

	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/mysql"
)

type webapiConfig struct {
	Http struct {
		Addr string `yaml:"Addr"`
		Port string `yaml:"Port"`
	} `yaml:"Http"`
	GatewayAddr   string `yaml:"GatewayAddr"`
	TokenSecret string `yaml:"TokenSecret"`
	TokenExpire string `yaml:"TokenExpire"`
	SaveLog     bool   `yaml:"SaveLog"`
	MySql       struct {
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

func main() {
	data, err := os.ReadFile(findConfig("webapi"))
	if err != nil {
		fmt.Println("load config failed:", err)
		os.Exit(1)
	}
	cfg := &webapiConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		fmt.Println("parse config failed:", err)
		os.Exit(1)
	}

	// 日志
	logger.StartLog()
	if cfg.SaveLog {
		_ = logger.InitLogFile()
	}

	// DB / Redis
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s",
		cfg.MySql.User, cfg.MySql.PassWord, cfg.MySql.Addr, cfg.MySql.Port, cfg.MySql.DBName)
	mysql.MysqlStart(dsn)

	// 确保账号表存在（name -> uid 映射）
	if _, err := mysql.DB.Exec(`CREATE TABLE IF NOT EXISTS accounts (
		name       VARCHAR(64) NOT NULL,
		uid        BIGINT      NOT NULL,
		created_at DATETIME(3) NOT NULL,
		PRIMARY KEY (name),
		UNIQUE KEY uk_uid (uid)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		fmt.Println("create accounts table failed:", err)
		os.Exit(1)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.PassWord,
		DB:       cfg.Redis.DB,
	})

	// 解析过期时间
	tokenExpire, err := time.ParseDuration(cfg.TokenExpire)
	if err != nil {
		tokenExpire = 24 * time.Hour
	}

	// 启动
	addr := fmt.Sprintf("%s:%s", cfg.Http.Addr, cfg.Http.Port)
	go startWebAPI(addr, mysql.DB, rdb, cfg.GatewayAddr, cfg.TokenSecret, tokenExpire)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	fmt.Println("WebAPI server started on", addr)
	<-sigChan
	fmt.Println("WebAPI server shutting down...")
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
