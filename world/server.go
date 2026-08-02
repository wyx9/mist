// world/server.go
package main

import (
	"net/http"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/jmoiron/sqlx"

	"simple_game/game/pkg/logger"
)

// startWorld 启动 World HTTP 服务。
func startWorld(addr string, db *sqlx.DB, rdb *redis.Client, agentAddr, gameAddr, tokenSecret string, tokenExpire time.Duration) {
	handler := &loginHandler{
		db:          db,
		rdb:         rdb,
		agentAddr:   agentAddr,
		gameAddr:    gameAddr,
		tokenSecret: []byte(tokenSecret),
		tokenExpire: tokenExpire,
	}

	http.HandleFunc("/login", handler.ServeHTTP)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	logger.INFO("world http server starting on", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		logger.ERROR("world http server failed:", err)
	}
}
