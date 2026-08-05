module simple_game/agent

go 1.25.0

require (
	github.com/go-redis/redis/v8 v8.11.5
	github.com/golang-jwt/jwt/v5 v5.3.1
	gopkg.in/yaml.v3 v3.0.1
	simple_game/game v0.0.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	nhooyr.io/websocket v1.8.17 // indirect
)

replace simple_game/game => ../game
