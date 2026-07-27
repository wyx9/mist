module simple_game/agent

go 1.25.0

require (
	github.com/go-redis/redis/v8 v8.11.5
	gopkg.in/yaml.v3 v3.0.1
	simple_game/game v0.0.0
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/go-sql-driver/mysql v1.10.0 // indirect
	github.com/jmoiron/sqlx v1.4.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	nhooyr.io/websocket v1.8.17 // indirect
)

replace simple_game/game => ../game
