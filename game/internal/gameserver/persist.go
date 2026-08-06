package gameserver

import (
	"errors"
	"fmt"

	"simple_game/game/internal/actor"
	core "simple_game/game/internal/actor/core"
	"simple_game/game/internal/persist"
	"simple_game/game/pkg/mysql"
	"simple_game/game/pkg/redis"
	"simple_game/game/pkg/storage"
)

// newPersistManager 构建持久化管理器：落地配置（表名/前缀/库名）由 actor 原型实现提供。
func newPersistManager(cfg *GameConfig, am *core.AManager) (*persist.Manager, error) {
	m, err := persist.NewManager(persist.Options{
		AM:  am,
		RDB: redis.RedisClient,
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
	// 注册落地
	if err := m.Register(&actor.Player{}); err != nil {
		return nil, err
	}
	return m, nil
}
