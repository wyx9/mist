package entity

type PlayerData struct {
	UID         int64  `json:"uid"`
	Name        string `json:"name"`
	LastLoginAt int64  `json:"last_login_at"`
	IsLogin     bool   `json:"is_login"`
}
