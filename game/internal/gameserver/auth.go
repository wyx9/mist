// internal/gameserver/auth.go
// 认证：JWT token 校验。
package gameserver

import (
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// verifyToken 验证 JWT token，返回 player_name 与 uid（持久化主键）。
func (s *Server) verifyToken(tokenStr string, secret []byte) (string, int64, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return "", 0, err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return "", 0, fmt.Errorf("invalid token claims")
	}
	sub, _ := claims.GetSubject()
	if sub == "" {
		return "", 0, fmt.Errorf("missing sub claim")
	}
	uid := int64(0)
	if v, ok := claims["uid"]; ok {
		switch n := v.(type) {
		case float64:
			uid = int64(n)
		case int64:
			uid = n
		}
	}
	return sub, uid, nil
}
