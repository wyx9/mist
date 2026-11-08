package gameserver

import (
	"errors"
	"fmt"
	"time"

	"simple_game/game/internal/actor"
	core "simple_game/game/internal/actor/core"
	"simple_game/game/internal/persist"
	"simple_game/game/pkg/mysql"
	"simple_game/game/pkg/redis"
	"simple_game/game/pkg/storage"
)

// newPersistManager 构建持久化管理器：类别按约定派生表名/前缀，并注册为默认落地后端。
func newPersistManager(cfg *GameConfig, am *core.AManager) (*persist.Manager, error) {
	m, err := persist.NewManager(persist.Options{
		AM:            am,
		RDB:           redis.RedisClient,
		FlushInterval: time.Duration(cfg.Persist.FlushInterval) * time.Second,
		OpenStore: func(dbName string) (storage.MysqlStore, error) {
			// 默认库复用全局 mysql.DB（共享连接，不负责关闭）；
			// 仅当 actor 指定了其他库名时才新开独立连接。
			if dbName == "" || dbName == cfg.MySql.DBName {
				if mysql.DB == nil {
					return nil, errors.New("mysql not initialized")
				}
				return storage.WrapMysqlStore(mysql.DB), nil
			}
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s",
				cfg.MySql.User, cfg.MySql.PassWord, cfg.MySql.Addr, cfg.MySql.Port, dbName)
			return storage.OpenMysqlStore(dsn)
		},
	})
	if err != nil {
		return nil, err
	}
	if err := m.RegisterCategory(string(actor.ActorTypePlayer)); err != nil {
		return nil, err
	}
	am.SetPersistResolver(m)
	return m, nil
}
