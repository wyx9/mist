# 内部跨进程 RPC 实现说明

本文梳理 game 多进程间的内部 RPC 机制，以及内部消息注册解耦的设计与使用方式。

## 1. 背景与目标

- **多进程**：game 可以启动多个进程，玩家分布在不同节点。
- **跨进程调用**：节点间通过 RPC 调用，链路为 `rpc → 统一 worker → AManager → actor handler`，与本地调用一致。
- **调用透明**：`ctx.Call/Send` 先查本地 AManager，未命中自动经 gateway 路由到目标节点，调用方不感知节点。
- **注册解耦**：内部消息 handler 仿 controller 用泛型注册，新增内部消息不改 Actor 文件。

## 2. 设计决策

| 决策点 | 结论 |
| --- | --- |
| 路由 Agent | 复用 gateway 作为路由 Agent：game↔gateway 长连接升级为 RPC 传输，gateway 解析 actor 位置并转发 |
| 消息编码 | protobuf（`.proto` 定义 + 生成，统一进 `pt` 包） |
| 调用 API | 透明路由：`ctx.Call/Send` 本地优先、未命中走远程 |
| 消息注册 | 仿 `route.Register` 的泛型注册，业务内部消息 handler 迁到 controller 层 |

## 3. 总体拓扑

```
                 ┌──────────┐   HTTP /login   ┌──────────┐
  浏览器 ──WS──▶ │ gateway  │ ◀────────────── │  webapi  │
                 │ (路由Agent)│                 └──────────┘
                 └────┬─────┘
        game↔gateway 长连接（会话隧道 + 内部 RPC 帧）
              ┌───────┴───────┐
         ┌────▼────┐     ┌────▼────┐
         │  game A │     │  game B │
         │  :9900  │     │  :9910  │
         └─────────┘     └─────────┘
                └───────┬───────┘
                    Redis：game:servers / actor:loc:{name} / player:{name}
```

## 4. 核心组件

### 4.1 `game/internal/rpc` 包（框架核心，不依赖 core）

| 文件 | 职责 |
| --- | --- |
| `context.go` | `HandlerContext[A]` + `Comm`（跨 actor 通信接口） |
| `registry.go` | `Register`（Send）/ `RegisterCall`（Call）注册表，双索引：按 Go 类型本地分发 + 按 method 远端分发；`Dispatch` / `MethodOf` |
| `frame.go` | `RpcFrame` 信封编解码 |
| `client.go` | `RemoteRouter` + `Client`：marshal、reqID 关联、超时、pending 表、transport 断开 flush |
| `server.go` | `Serve`：method → unmarshal → `CallLocal/SendLocal` → 回写响应 |

RPC 信封（`rpc.proto` → `pt.RpcFrame`）：

```proto
enum RpcKind { RPC_REQUEST = 0; RPC_RESPONSE = 1; }
message RpcFrame {
  RpcKind Kind = 1;
  string  ReqId = 2;       // 关联 id（雪花）
  string  Target = 3;      // 目标 actor 名
  string  Method = 4;      // 方法名，路由到 handler
  string  OriginNode = 5;  // 来源节点（响应回程路由）
  string  OriginActor = 6;
  bytes   Payload = 7;     // 业务消息 protobuf
  string  Error = 8;
  bool    IsCall = 9;      // true=Call 需回包
  int64   TimeoutMs = 10;
}
```

### 4.2 `core`（actor）改动

- `AManager.Call/Send`：本地 `Find` 命中走本地；未命中委托 `remoteRouter`（跨进程）。
- 新增 `CallLocal/SendLocal`：**不带远程回退**，供远端 RPC 服务端调用，防止环路。
- `actor_loop.dispatch`：内部消息先走 `rpc.Dispatch`（注册表），未注册回退到 `actor.HandleMessage`（框架生命周期消息 `PersistTick/ConnDetached/ConnAttached`）。
- `Routable` 标记接口：实现后创建/关闭时向 `actor:loc` 注册/注销位置。`Player` 实现，`Room`（每节点常驻）不实现。

### 4.3 gateway（路由 Agent）

- 新增 `TunnelRPC` 隧道帧（`codec.TunnelRPC = 7`，payload 为 `pt.RpcFrame`）。
- `RPC_REQUEST`：`actor:loc:{target}` 解析节点 → 转发；失败回错误响应。
- `RPC_RESPONSE`：按 `origin_node` 回程转发。
- 节点失联时 `SweepActorLoc` 清理该节点的位置记录。gateway 不解析业务 payload，保持无状态。

### 4.4 位置表（`pkg/registry`）

- `actor:loc:{name} → addr`：可路由 actor 创建时写、关闭时删。
- `Register/Unregister/ResolveActorLoc/SweepActorLoc` 辅助函数。

## 5. 跨进程调用数据流（A 节点1 → B 节点2）

```
A handler: ctx.Call("B", &pt.XXXReq{})
  → AManager.Call: 本地 Find("B") 未命中
  → rpc.Client.Call: marshal + 登记 pending[reqID]
  → A↔gateway 长连接写 TunnelRPC(RPC_REQUEST, target=B, method=..., origin_node=A)

gateway: actor:loc:B → 节点2 → 转发给 game B

game B: 统一 worker pool → rpc.Serve → AManager.CallLocal("B")
  → B 的 actor 串行循环 → rpc.Dispatch → 注册的 handler → 返回 proto 响应
  → 回写 TunnelRPC(RPC_RESPONSE, origin_node=A)

gateway: 按 origin_node 回程给 game A → rpc.Client.OnResponse → pending 唤醒 → Call 返回
```

## 6. 如何新增内部消息

只改 proto + controller 一处注册，**不改 Actor 文件**：

1. 定义消息（`.proto`，重新生成 `pt/*.pb.go`）。
2. 在 `controller` 写 handler：

```go
// 异步 Send
func JoinHandler(ctx *rpc.HandlerContext[*actor.Room], req *pt.RoomPlayerJoin) error { ... }

// 同步 Call
func GetPlayersHandler(ctx *rpc.HandlerContext[*actor.Room], req *pt.RoomGetPlayersReq) (*pt.RoomGetPlayersRes, error) { ... }
```

3. 在 `controller/rpc_register.go` 的 `RegisterRpc()` 注册：

```go
rpc.Register[*actor.Room, *pt.RoomPlayerJoin]("room.join", JoinHandler)
rpc.RegisterCall[*actor.Room, *pt.RoomGetPlayersReq, *pt.RoomGetPlayersRes]("room.get_players", GetPlayersHandler)
```

4. 调用方透明调用（本地/远程无差别）：

```go
ctx.Comm.Send("room", &pt.RoomPlayerJoin{UID: 1, Name: "x"})
res, err := ctx.Comm.Call("room", &pt.RoomGetPlayersReq{}, 5*time.Second)
```

## 7. 跨节点查询玩家信息（示例）

- 客户端协议：`CmdPlayerInfo = 11`，`PlayerInfoReq{Name}` / `PlayerInfoRes{UUid, Name, Online, LastLoginAt}`。
- 客户端 handler `PlayerInfoController` 内部执行 `ctx.Comm.Call(target, &pt.PlayerInfoReq{})`。
- 服务端 handler `PlayerInfoInternalHandler`（注册 `player.get_info`）返回目标玩家自己的信息快照。

## 8. 多实例启动

game 支持 flag 覆盖（留空时行为与原逻辑一致）：

```powershell
cd game && go build -o game.exe .
.\game.exe -port=9900 -register=127.0.0.1:9900 -machineid=1
.\game.exe -port=9910 -register=127.0.0.1:9910 -machineid=2
```

| flag | 说明 |
| --- | --- |
| `-config` | 配置文件路径（空则自动探测） |
| `-port` | 覆盖 `Listen.Port` |
| `-register` | 覆盖 `RegisterAddr`（须唯一、gateway 可连通） |
| `-machineid` | 雪花机器 id（同机多实例必须不同，默认 `-1` 用本机 IP 推导） |

## 9. webapi 节点分配策略

`webapi/config/config.yaml`：

```yaml
GameSelect:
  Strategy: "hash"    # fixed（指定节点）/ hash（按玩家名哈希，默认）
  FixedNode: ""       # fixed 时指定目标 game 地址
```

- `fixed`：所有玩家固定路由到 `FixedNode`。
- `hash`：先 `sort.Strings` 稳定排序存活节点，再按 `FNV-1a(name) % len` 选择，保证同一玩家跨登录落同一节点。

## 10. 测试

`game/internal/rpc/rpc_integration_test.go`：自建 TCP 接收侧 + 真实 `rpc.Client`，验证：

1. 同步 `Call` 的完整收发与类型化响应反序列化；
2. 异步 `Send` 触发对端 handler；
3. 目标 actor 不存在时错误跨进程回传（非超时）。

```powershell
cd game && go test ./internal/rpc/ -count=1
```

## 11. 边界与已知限制

- **单 gateway**：transport 挂载/清空假设单条 gateway 连接；gateway 快速重连存在极小概率 transport 竞态，多 gateway/HA 留待后续。
- **固定节点不校验存活**：`fixed` 指向的节点宕机时由 gateway 握手阶段回 `game unavailable`。
- **Room 大厅为每节点本地**：`Room` 不注册进 `actor:loc`，跨节点聚合大厅/全局房间留待后续。
- **`Call` 回包为 `interface{}`**：调用方类型断言响应类型（与 `ctx.Call` 现状一致）。
