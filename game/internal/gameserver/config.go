// game/config.go
package gameserver

import (
	"os"
	"time"

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
	SaveLog     bool   `yaml:"SaveLog"`
	TokenSecret string `yaml:"TokenSecret"`
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
	Persistence PersistenceConfig `yaml:"Persistence"`
}

// PersistenceConfig actor 持久化配置。
type PersistenceConfig struct {
	RedisFlushInterval time.Duration          `yaml:"RedisFlushInterval"`
	MysqlFlushInterval time.Duration          `yaml:"MysqlFlushInterval"`
	MysqlBatchSize     int                    `yaml:"MysqlBatchSize"`
	RedisQueueSize     int                    `yaml:"RedisQueueSize"`
	Categories         map[string]CategoryCfg `yaml:"Categories"`
}

// CategoryCfg 单类 actor 的持久化配置。
type CategoryCfg struct {
	RedisKeyPrefix string        `yaml:"RedisKeyPrefix"`
	Table          string        `yaml:"Table"`
	DBName         string        `yaml:"DBName"`
	Codec          string        `yaml:"Codec"`
	FlushInterval  time.Duration `yaml:"FlushInterval"`
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
