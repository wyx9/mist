# simple_game — Claude 项目规则

## 服务器框架设计参考

修改 game/ 下服务器框架逻辑（actor、dispatch、gameserver、route、网络层）时，必须参考以下游戏服务器框架的设计原则：

### 核心参考框架
1. **Skynet** (cloudwu) — Actor 模型 + 两级队列调度 + Worker Pool
2. **Leaf** (Go) — 模块化 + chanrpc + goroutine 管理
3. **Pitaya** (Go) — 分布式微服务 + 服务发现

### 设计原则 (提炼自 Skynet)

**Actor 模型**
- 每个 Actor 独立运行，通过消息通信，不共享内存
- Actor 内部串行处理消息，无需加锁
- "Let it crash" — Actor 崩溃不应拖垮整个服务
- 项目已对齐：IActor / ActorBase / AManager 体系符合此模型

**两级队列调度 (Skynet 核心)**
- 全局队列只存放"非空"的 Actor 队列指针
- Worker 线程从全局队列取 Actor，消费消息后若队列非空则放回全局队列末尾
- 避免 Worker 轮询空队列，避免忙 Actor 饿死闲 Actor
- 项目当前状态：WorkerPool 按 tunnelID 哈希分配，缺乏全局公平调度

**权重消费**
- Worker 线程按权重消费消息：weight=0 全消费，weight=1 消费一半，weight=-1 只消费 1 条
- 防止长队列 Actor 独占 Worker 导致其他 Actor 饥饿
- 项目当前状态：无权重机制，每个 task 消费一条消息后回池

**一人一 Actor**
- 每个玩家对应一个独立 Actor（Player Actor）
- 项目已对齐：Player Actor = `actor.StartNewPlayerActor(tunnelID, conn)`

**服务注册模式**
- Actor 按消息类型注册 handler（类似 `skynet.dispatch`）
- 消息到达时由框架查表路由到对应 handler
- 项目已对齐：route.Init() + route.RegisteredRoute() + controller 体系

**定时器服务**
- 独立的定时器线程/goroutine，不阻塞 worker
- 项目当前状态：定时器系统已删除（原有实现有 bug），待重新设计

**监控/钩子系统**
- Actor 生命周期钩子：Start / Stop / Crash
- 消息处理超时告警
- 项目当前状态：ActorBase 有 Start/Stop，缺 Crash 钩子和监控

### 改动检查清单

每次修改服务器框架代码前，确认：
1. 是否符合 Actor 串行处理原则？（单 Actor 内无并发）
2. 调度公平性？（忙 Actor 不饿死闲 Actor）
3. 是否有崩溃隔离？（单 Actor panic 不影响其他）
4. 消息路由是否解耦？（不硬编码消息类型到框架层）
5. 是否引入共享状态？（Actor 间不共享可变数据）

### 禁止的修改
- 在 Actor 间引入共享可变状态（违背消息传递原则）
- 在消息处理路径中做同步阻塞 I/O
- 让网络层感知业务消息内容
- 在 agent 中做业务逻辑（agent 只做转发）
