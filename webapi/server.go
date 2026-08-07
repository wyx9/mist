// webapi/server.go
package main

import (
	"net/http"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/jmoiron/sqlx"

	"simple_game/game/pkg/logger"
)

// startWebAPI 启动 WebAPI HTTP 服务。
func startWebAPI(addr string, db *sqlx.DB, rdb *redis.Client, gatewayAddr, tokenSecret string, tokenExpire time.Duration) {
	handler := &loginHandler{
		db:          db,
		rdb:         rdb,
		gatewayAddr:   gatewayAddr,
		tokenSecret: []byte(tokenSecret),
		tokenExpire: tokenExpire,
	}

	http.HandleFunc("/login", cors(handler.ServeHTTP))
	http.HandleFunc("/health", cors(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	logger.INFO("webapi http server starting on", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		logger.ERROR("webapi http server failed:", err)
	}
}

// cors 允许浏览器测试页跨域访问（开发用途）。
func cors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}
