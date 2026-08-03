// internal/gameserver/auth.go
// 认证：JWT token 校验。
package gameserver

import (
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// verifyToken 验证 JWT token，返回 player_name。
func (s *Server) verifyToken(tokenStr string, secret []byte) (string, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return "", err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return "", fmt.Errorf("invalid token claims")
	}
	sub, _ := claims.GetSubject()
	if sub == "" {
		return "", fmt.Errorf("missing sub claim")
	}
	return sub, nil
}
