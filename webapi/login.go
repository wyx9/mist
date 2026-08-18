// webapi/login.go
package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/jmoiron/sqlx"

	"simple_game/game/pkg/uid"
)

// loginHandler 处理 POST /login 请求。
type loginHandler struct {
	db          *sqlx.DB
	rdb         *redis.Client
	gatewayAddr string
	tokenSecret []byte
	tokenExpire time.Duration
	gameSelect  gameSelectCfg
}

type loginRequest struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token     string `json:"token"`
	GatewayAddr string `json:"gateway_addr"`
}

func (h *loginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Password == "" {
		http.Error(w, "name and password required", http.StatusBadRequest)
		return
	}

	// 校验账号密码（简化：首次登录即允许，实际应查 DB 验证）
	// TODO: 接入 DB 验证密码

	// 确保账号存在并获取稳定 uid（持久化主键）
	playerUID, err := ensureAccount(h.db, req.Name)
	if err != nil {
		http.Error(w, "ensure account failed", http.StatusInternalServerError)
		return
	}

	// 按配置策略选择目标 game 节点
	gameAddr, err := h.selectGame(req.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	// 写 Redis 注册信息
	if err := registerPlayer(h.rdb, req.Name, h.gatewayAddr, gameAddr, h.tokenExpire); err != nil {
		http.Error(w, "register player failed", http.StatusInternalServerError)
		return
	}

	// 签发 token
	token, err := generateToken(req.Name, playerUID, h.tokenSecret, h.tokenExpire)
	if err != nil {
		http.Error(w, "generate token failed", http.StatusInternalServerError)
		return
	}

	resp := loginResponse{
		Token:     token,
		GatewayAddr: h.gatewayAddr,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// ensureAccount 查询 name -> uid 映射；不存在则领取全局自增 uid 并入库。
func ensureAccount(db *sqlx.DB, name string) (int64, error) {
	var id int64
	err := db.Get(&id, "SELECT uid FROM accounts WHERE name = ?", name)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	id = uid.NewUid()
	_, err = db.Exec("INSERT INTO accounts (name, uid, created_at) VALUES (?, ?, ?)", name, id, time.Now())
	if err != nil {
		// 并发下其他请求已建号：回查返回既有 uid
		existing := int64(0)
		if err2 := db.Get(&existing, "SELECT uid FROM accounts WHERE name = ?", name); err2 == nil {
			return existing, nil
		}
		return 0, err
	}
	return id, nil
}
