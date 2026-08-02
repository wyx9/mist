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
	go startWorld(addr, mysql.DB, rdb, cfg.AgentAddr,
		fmt.Sprintf("%s:%s", cfg.Http.Addr, "9900"), // gameAddr 默认
		cfg.TokenSecret, tokenExpire)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	fmt.Println("World server started on", addr)
	<-sigChan
	fmt.Println("World server shutting down...")
}
