# 内部跨进程 RPC 实现说明

本文梳理 game 多进程间的内部 RPC 机制，以及内部消息注册解耦的设计与使用方式。

## 1. 背景与目标

- **多进程**：game 可以启动多个进程，玩家分布在不同节点。
- **跨进程调用**：节点间通过 RPC 调用，链路为 `rpc → 统一 worker → AManager → actor handler`，与本地调用一致。
- **调用透明**：`ctx.Cast/Call` 先查本地 AManager（`Find`），未命中查位置表：本节点/可认领则本地复活，他节点则经 gateway 路由到 owner 节点，调用方不感知节点。
- **注册解耦**：内部消息 handler 仿 controller 用泛型注册，新增内部消息不改 Actor 文件。

## 2. 设计决策

| 决策点 | 结论 |
| --- | --- |
| 路由 Agent | 复用 gateway 作为路由 Agent：game↔gateway 长连接升级为 RPC 传输，gateway 解析 actor 位置并转发 |
| 消息编码 | protobuf（`.proto` 定义 + 生成，统一进 `pt` 包） |
| 调用 API | 透明路由：`ctx.Cast/Call` 本地 `Find` 优先、未命中走远程路由（本地不创建） |
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
                    Redis：game:servers / actor:loc:{uid} / actor:init:{uid} / player:{uid}
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
  int64   UID = 3;         // 目标 actor uid（全局身份；跨进程路由/owner 判定依据）
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

- `AManager.Cast/Call`：本地 `Find` 命中走本地；未命中查位置表（`IsOwner`）——本节点/可认领本地复活，他节点**委托 `remoteRouter`**（跨进程），**绝不本地创建他节点 actor**。
- 新增 `CallLocal/SendLocal(to, category, ...)`：**不带远程回退**，供远端 RPC 服务端调用，防止环路；`category` 为兜底类别（本地无记录时按需创建/复活）。
- `actor_loop.dispatch`：内部消息先走 `rpc.Dispatch`（注册表），未注册回退到 `actor.HandleMessage`（框架生命周期消息 `PersistTick/ConnDetached/ConnAttached`）。
- `Routable` 标记接口：可路由 actor（`Player`）创建时原子认领 `actor:loc:{uid}`；`Room`（每节点常驻）不实现。

### 4.3 gateway（路由 Agent）

- 新增 `TunnelRPC` 隧道帧（`codec.TunnelRPC = 7`，payload 为 `pt.RpcFrame`）。
- `RPC_REQUEST`：`actor:loc:{target}` 解析节点 → 转发；失败回错误响应。
- `RPC_RESPONSE`：按 `origin_node` 回程转发。
- 节点失联时 `SweepActorLoc` 清理该节点的位置记录。gateway 不解析业务 payload，保持无状态。

### 4.4 位置表（`pkg/registry`）

- `actor:loc:{uid} → addr`：可路由 actor 的 **owner 节点登记**。创建时**原子认领**（Lua：不存在或已属于本节点才写入，已被他节点占有时返回 `ErrActorNotOwner`），**关闭时保留**（供复活路由），`DeleteActor`/节点失联（`SweepActorLoc`）时清除。
- 辅助函数：`ClaimActorLoc`（原子认领）/ `SetActorLoc`（强制接管，握手用）/ `UnregisterActorLoc` / `ResolveActorLoc` / `SweepActorLoc`。
- `actor:init:{uid} → category`：初始化记录（长期保留），进程重启/跨节点后据此按需复活。

### 4.5 actor 位置与 owner 边界

每个可路由 actor 有且只有一个 **owner 节点**（`actor:loc:{uid}` 的登记方），各方法职责：

| 方法 | 职责 | 边界约束 |
| --- | --- | --- |
| `Find(uid)` | 只查本地运行实例（运行中/休眠中） | 不启动、不访问远程 |
| `EnsureRunning(uid, cat)` | 本地/跨节点 RPC 按需启动统一入口 | **只允许 owner 节点调用**；创建路径原子认领，非 owner 返回 `ErrActorNotOwner` 且不残留实例 |
| `SendLocal / CallLocal` | 远端 RPC 服务端投递（`rpc.Serve` 调用） | 请求已到达 owner 节点，允许按需启动并投递 |
| `Cast / Call` | 业务跨 actor 通信 | 本地 `Find` 未命中 → 查位置表：位置归本节点/可认领 → 本地复活；位置归他节点 → 远程路由（绝不本地创建他节点 actor） |
| `EnsureDaemon(uid, cat)` | 常驻 actor（如 `RoomUID=-1`） | 跳过 uid 合法性校验，仅本地常驻、不参与路由 |

跨节点顶替（重新登录落到新节点）：握手路径先 `Claim`（覆盖写接管位置），再 `EnsureRunning`；旧节点在 `Detach` 时检测到「位置已不属于自己」即关闭本地孤儿实例，避免双实例。

## 5. 跨进程调用数据流（A 节点1 → B 节点2）

```
A handler: ctx.Call(B_uid, &pt.XXXReq{})
  → AManager.Call: 本地 Find(B_uid) 未命中 → 查位置表 IsOwner(B_uid)
      位置归本节点/无记录 → CallLocal 本地复活（不经 gateway）
      位置归他节点 ↓
  → rpc.Client.Call: marshal + 登记 pending[reqID]
  → A↔gateway 长连接写 TunnelRPC(RPC_REQUEST, UID=B_uid, method=..., origin_node=A)

gateway: ResolveActorLoc(B_uid) → 节点2（owner）→ 转发给 game B

game B: rpc.Serve → AManager.CallLocal(B_uid, category)
  → owner 节点按需启动/复活（EnsureRunning，位置原子认领）
  → B 的 actor 串行循环 → rpc.Dispatch → 注册的 handler → 返回 proto 响应
  → 回写 TunnelRPC(RPC_RESPONSE, origin_node=A)

gateway: 按 origin_node 回程给 game A → rpc.Client.OnResponse → pending 唤醒 → Call 返回
```

- 目标已关闭但位置仍归本节点：`Cast/Call` 查位置表后**直接本地复活**，不经过 gateway 往返。
- 目标已关闭（位置保留在他节点）：gateway 仍按 `actor:loc` 路由到 owner 节点，owner 节点 `SendLocal/CallLocal → EnsureRunning` 复活并重载数据后投递，调用方无感知。
- 目标 owner 节点失联（位置已被清扫、可认领）：任意节点 `Cast/Call` 查位置表无记录 → 本地认领复活（虚拟 actor 语义）。

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

- 客户端协议：`CmdPlayerInfo = 11`，`PlayerInfoReq{UUid}` / `PlayerInfoRes{UUid, Name, Online, LastLoginAt}`。
- 客户端 handler `PlayerInfoController` 内部执行 `ctx.Comm.Call(req.UUid, &pt.PlayerInfoReq{})`（透明跨进程：目标在本地则本地，在其它节点则经 gateway 路由到 owner）。
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
2. 异步 `Cast` 触发对端 handler；
3. 目标从未在本进程初始化过（内存无记录）时，接收节点按方法类别自动创建并回包（`actor:init` 复活场景）；
4. 未注册的消息类型在客户端侧 `MethodOf` 校验即失败（非超时）。

```powershell
cd game && go test ./internal/rpc/ -count=1
```

## 11. 边界与已知限制

- **单 gateway**：transport 挂载/清空假设单条 gateway 连接；gateway 快速重连存在极小概率 transport 竞态，多 gateway/HA 留待后续。
- **固定节点不校验存活**：`fixed` 指向的节点宕机时由 gateway 握手阶段回 `game unavailable`。
- **Room 大厅为每节点本地**：`Room` 不注册进 `actor:loc`，跨节点聚合大厅/全局房间留待后续。
- **跨节点顶替的数据冲刷时序**：旧节点孤儿在 `Detach` 时正常关闭（会落盘）。若新节点 `loadPlayer` 先于旧节点落盘完成，旧落盘可能短暂覆盖新数据，随后新 owner 的脏写会再覆盖回去；窗口极小（gateway 先踢旧会话再握手新节点）。如需严格数据交接可后续增加「孤儿关闭跳过落盘」钩子。
- **`Call` 回包为 `interface{}`**：调用方类型断言响应类型（与 `ctx.Call` 现状一致）。
