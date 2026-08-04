package gameserver

import (
	"fmt"

	"simple_game/game/internal/actor"
	"simple_game/game/internal/persist"
	"simple_game/game/pkg/redis"
	"simple_game/game/pkg/storage"
)

// newPersistManager 根据配置构建持久化管理器。
func newPersistManager(cfg *GameConfig, am *actor.AManager) (*persist.Manager, error) {
	specs := make(map[string]persist.CategorySpec, len(cfg.Persistence.Categories))
	for category, cc := range cfg.Persistence.Categories {
		specs[category] = persist.CategorySpec{
			Category:       category,
			RedisKeyPrefix: cc.RedisKeyPrefix,
			Table:          cc.Table,
			DBName:         cc.DBName,
			Codec:          cc.Codec,
			FlushInterval:  cc.FlushInterval,
		}
	}
	mysqlCfg := &storage.MysqlSyncConfig{
		Redis:     redis.RedisClient,
		Interval:  cfg.Persistence.MysqlFlushInterval,
		BatchSize: cfg.Persistence.MysqlBatchSize,
		OpenStore: func(dbName string) (storage.MysqlStore, error) {
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s",
				cfg.MySql.User, cfg.MySql.PassWord, cfg.MySql.Addr, cfg.MySql.Port, dbName)
			return storage.OpenMysqlStore(dsn)
		},
	}
	return persist.NewManager(persist.Options{
		AM:             am,
		RDB:            redis.RedisClient,
		Specs:          specs,
		DefaultDBName:  cfg.MySql.DBName,
		RedisQueueSize: cfg.Persistence.RedisQueueSize,
		MySQLCfg:       mysqlCfg,
	})
}
