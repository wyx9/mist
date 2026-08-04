// world/token.go
package main

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// generateToken 签发 JWT token，携带 playerName 与 uid（游戏服持久化主键）。
func generateToken(playerName string, uid int64, secret []byte, expireDuration time.Duration) (string, error) {
	claims := jwt.MapClaims{
		"sub": playerName,
		"uid": uid,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(expireDuration).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// verifyWorldToken 验证 JWT token（World 端保留验证能力用于调试）。
func verifyWorldToken(tokenStr string, secret []byte) (string, error) {
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
		return "", fmt.Errorf("invalid token")
	}
	sub, _ := claims.GetSubject()
	return sub, nil
}
