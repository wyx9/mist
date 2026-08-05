// world/main.go
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
	"gopkg.in/yaml.v3"

	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/mysql"
)

type worldConfig struct {
	Http struct {
		Addr string `yaml:"Addr"`
		Port string `yaml:"Port"`
	} `yaml:"Http"`
	AgentAddr   string `yaml:"AgentAddr"`
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
	data, err := os.ReadFile("world/config/config.yaml")
	if err != nil {
		fmt.Println("load config failed:", err)
		os.Exit(1)
	}
	cfg := &worldConfig{}
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
	go startWorld(addr, mysql.DB, rdb, cfg.AgentAddr, cfg.TokenSecret, tokenExpire)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	fmt.Println("World server started on", addr)
	<-sigChan
	fmt.Println("World server shutting down...")
}
