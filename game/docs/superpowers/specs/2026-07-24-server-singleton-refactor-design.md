# Game Server 单例重构 + 目录整理 设计文档

> 状态: 设计完成 | 日期: 2026-07-24

## 1. 目标

1. 将 game/ 下分散的全局变量和启动逻辑收敛为 `Server` 单例，统一生命周期管理
2. 整理目录结构，按职责分类模块，参考 Go 社区命名惯例

## 2. Server 单例设计

### 2.1 结构体

```go
// internal/gameserver/server.go
type Server struct {
    cfg *Config

    // 网络层
    listener pkg.Listener
    grpc     *grpc.Server

    // 消息路由层
    workerPool *dispatch.WorkerPool

    // Actor 管理层
    actorMgr *actor.Manager

    // Agent 连接表 — tunnelID → pkg.NetConn
    agentConns sync.Map

    // 生命周期
    ctx    context.Context
    cancel context.CancelFunc
    wg     sync.WaitGroup

    started atomic.Bool
    stopped atomic.Bool
}
```

### 2.2 单例机制

```go
var (
    instance *Server
    once     sync.Once
)

func NewServer(cfg *Config) *Server {
    once.Do(func() { instance = &Server{cfg: cfg} })
    return instance
}

func Instance() *Server { return instance }
```

### 2.3 Facade 方法

外部模块通过 `engine.Instance()` 调用，不直接访问内部字段：

| 方法 | 说明 |
|------|------|
| `CastMsg(name, msg)` | 异步消息 → actor.Manager |
| `CallMsg(name, msg)` | 同步调用 → actor.Manager |
| `Dispatch(tunnelID, task)` | 投递到 WorkerPool |
| `ReplyToAgent(tunnelID, data)` | 通过隧道回复 Agent |
| `ClosePlayer(ip)` | 关闭玩家 Actor |
| `RegisterActor(a)` | 注册 Actor |

### 2.4 启动顺序

```
Phase 1: 配置 + 日志
  Server.LoadConfig(path)
  logger.Start()

Phase 2: 基础设施
  mysql.Start(dsn)
  redis.Start(addr, pwd, db)

Phase 3: 业务初始化
  route.InitFactory()
  route.Register()
  dispatch.NewPool(runtime.NumCPU())

Phase 4: 对外服务 (goroutine)
  go grpc.Start(addr)
  go server.listen(addr, tokenSecret)

Phase 5: 就绪
  log "Game server started on ..."
  <-sigChan
```

### 2.5 关闭流程（逆序）

```
SIGTERM
  → listener.Close()          停止接受新连接
  → agentConns.Range → ClosePlayer  断开所有玩家
  → actorMgr.WaitAll(15s)    等待 Actor 排空队列
  → workerPool.Shutdown()    排空 worker 队列
  → grpc.GracefulStop()      优雅关闭 gRPC
  → mysql.Close() / redis.Close()
```

## 3. 目录结构

### 3.1 新结构

```
game/
├── main.go
│
├── pkg/                          # 外部可用 (agent / world import)
│   ├── network/                  #   NetConn, Listener, Dial, TCP, WS
│   │   ├── conn.go
│   │   ├── tcp.go
│   │   ├── ws.go
│   │   └── dial.go
│   ├── codec/                    #   消息编解码
│   │   ├── packet.go             #     PacketMsg
│   │   └── tunnel.go             #     TunnelMsg
│   ├── logger/
│   │   └── logger.go
│   ├── mysql/
│   │   └── mysql.go
│   ├── redis/
│   │   └── redis.go
│   └── uid/
│       └── uid.go
│
├── internal/                     # 仅 game 内部可见
│   ├── gameserver/               #   Server 单例 + 生命周期
│   │   ├── server.go
│   │   ├── config.go
│   │   ├── agent.go              #     handleAgentConn
│   │   └── message.go            #     handleMsg
│   ├── actor/                    #   Actor 模型
│   │   ├── base.go
│   │   ├── manager.go
│   │   └── player.go
│   ├── dispatch/                 #   消息分发
│   │   └── pool.go
│   ├── route/                    #   路由
│   │   ├── router.go
│   │   ├── factory.go
│   │   └── register.go
│   └── controller/               #   控制器
│       ├── base.go
│       └── handler.go
│
├── rpc/
│   └── server.go                   # 原 engine/grpc.go (避免与 google.golang.org/grpc 冲突)
├── api/proto/pt/                 # protobuf (不变)
├── client/                       # 测试客户端 (不变)
├── configs/                      # Excel 工具 (不变)
├── config/                       # YAML 配置 (不变)
├── docs/                         # 文档
└── test/                         # 测试
```

### 3.2 命名规则

- 包名名词单数：`actor` 非 `actors`
- 文件名去前缀：`actor_base.go` → `base.go`
- `internal/` 隔离内部实现
- 按职责聚合：一个目录一个明确职责

### 3.3 文件映射

| 旧路径 | 新路径 |
|--------|--------|
| `engine/server.go` | 拆入 `internal/gameserver/server.go` + `agent.go` |
| `engine/config.go` | `internal/gameserver/config.go` |
| `engine/handle_msg.go` | `internal/gameserver/message.go` |
| `engine/worker.go` | `internal/dispatch/pool.go` |
| `engine/actor_base.go` | `internal/actor/base.go` |
| `engine/actor_manager.go` | `internal/actor/manager.go` |
| `engine/actor_player.go` | `internal/actor/player.go` |
| `engine/grpc.go` | `rpc/server.go` |
| `pkg/netconn.go` + `wsconn.go` + `conn_dial.go` | `pkg/network/conn.go` + `ws.go` + `tcp.go` + `dial.go` |
| `pkg/msg.go` | `pkg/codec/packet.go` |
| `pkg/logger.go` | `pkg/logger/logger.go` |
| `pkg/mysql.go` | `pkg/mysql/mysql.go` |
| `pkg/redis.go` | `pkg/redis/redis.go` |
| `pkg/uuid.go` | `pkg/uid/uid.go` |
| `libs/packet_msg.go` | `pkg/codec/packet.go` (合并) |
| `tunnel/tunnel.go` | `pkg/codec/tunnel.go` |
| `routes/routes.go` | `internal/route/router.go` |
| `routes/protobuf_factory.go` | `internal/route/factory.go` |
| `register/register_route.go` | `internal/route/register.go` |
| `controller/base_controller.go` | `internal/controller/base.go` |
| `controller/all_controller.go` | `internal/controller/handler.go` |

### 3.4 删除的目录

`engine/` `libs/` `tunnel/` `routes/` `register/` `controller/` `pkg/`

## 4. Import 路径变更

### 4.1 外部 (agent / world)

| 旧 | 新 |
|----|-----|
| `simple_game/game/pkg` | `simple_game/game/pkg/network` + `.../pkg/logger` 等 |
| `simple_game/game/libs` | `simple_game/game/pkg/codec` |
| `simple_game/game/tunnel` | `simple_game/game/pkg/codec` |

### 4.2 内部 (game)

| 旧 | 新 |
|----|-----|
| `simple_game/game/engine` | `simple_game/game/internal/gameserver` |
| `simple_game/game/pkg` | `simple_game/game/pkg/network` 等 |
| `simple_game/game/libs` | `simple_game/game/pkg/codec` |
| `simple_game/game/tunnel` | `simple_game/game/pkg/codec` |
| `simple_game/game/routes` | `simple_game/game/internal/route` |
| `simple_game/game/register` | `simple_game/game/internal/route` |
| `simple_game/game/controller` | `simple_game/game/internal/controller` |

## 5. main.go 变化

```go
// 旧
func main() {
    cfg, _ := engine.LoadGameConfig(...)
    pkg.StartLog()
    routes.Init()
    register.RegisteredRoute()
    // ...
    go engine.StartGRPC()
    go engine.Start(addr, token)
}

// 新
func main() {
    srv := gameserver.NewServer(nil)
    if err := srv.Start("config/config.yaml"); err != nil {
        os.Exit(1)
    }
}
```

## 6. 不变量

- Actor 模型接口（IActor / ActorBase / AManager）不变
- 消息格式（PacketMsg / TunnelMsg）不变
- agent / world 只更新 import 路径，逻辑不变
- protobuf 生成代码路径不变
