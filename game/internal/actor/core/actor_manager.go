package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"simple_game/game/internal/rpc"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/protocol"
	"simple_game/game/pkg/timer"
)

// categoryIndex 单个类别的索引结构。
type categoryIndex struct {
	actors sync.Map // uidKey(string) → *ActorBase
}

// AManager Actor 管理器：全局单例，负责所有 Actor 的存储、查找、消息投递与生命周期状态机。
type AManager struct {
	actorMap    sync.Map // uidKey → *ActorBase 主存储（运行中/休眠中）
	knownActors sync.Map // uidKey → category 已初始化过（关闭后仍保留，供按需复活）
	categories  sync.Map // categoryName(string) → *categoryIndex
	factories   sync.Map // categoryName(string) → ActorFactory
	mu          sync.RWMutex
	createMu    sync.Mutex        // 串行化「工厂创建 + 启动」，避免同 uid 并发重复创建
	sched       *timer.Scheduler  // actor 内部内存定时调度器（惰性创建）
	faultHandler func(name string) // actor 故障回调（handler 超时等），由 gameserver 设置
	remoteRouter rpc.RemoteRouter   // 跨进程路由（本地未命中时委托），由 gameserver 注入
	locationReg  LocationRegistry  // 可路由 actor 位置注册，由 gameserver 注入
	initReg      InitRegistry      // actor 初始化记录（uid→category，Redis 持久化），由 gameserver 注入
	sweeperCtx   context.Context
	sweeperStop  context.CancelFunc
	sweeperRun   bool
}

// SetFaultHandler 注册 actor 故障回调（如 handler 超时），用于踢掉故障玩家的连接。
func (am *AManager) SetFaultHandler(fn func(name string)) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.faultHandler = fn
}

// notifyFault 通知故障回调（不阻塞，读取回调快照后调用）。
func (am *AManager) notifyFault(name string) {
	am.mu.RLock()
	fn := am.faultHandler
	am.mu.RUnlock()
	if fn != nil {
		fn(name)
	}
}

// SetRemoteRouter 注册跨进程路由（本地未命中时委托给 gateway 转发）。
func (am *AManager) SetRemoteRouter(r rpc.RemoteRouter) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.remoteRouter = r
}

// remoteRouterSnapshot 读取跨进程路由引用快照。
func (am *AManager) remoteRouterSnapshot() rpc.RemoteRouter {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.remoteRouter
}

// SetLocationRegistry 注册可路由 actor 的位置注册实现。
func (am *AManager) SetLocationRegistry(r LocationRegistry) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.locationReg = r
}

// locationRegSnapshot 读取位置注册实现引用快照。
func (am *AManager) locationRegSnapshot() LocationRegistry {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.locationReg
}

// SetInitRegistry 注册 actor 初始化记录实现（Redis uid→category）。
func (am *AManager) SetInitRegistry(r InitRegistry) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.initReg = r
}

// initRegSnapshot 读取初始化记录实现引用快照。
func (am *AManager) initRegSnapshot() InitRegistry {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.initReg
}

// RegisterFactory 注册某类 actor 的工厂（按需启动时使用）。
func (am *AManager) RegisterFactory(category string, f ActorFactory) error {
	if category == "" {
		return errors.New("empty actor category")
	}
	if _, ok := am.factories.Load(category); ok {
		return fmt.Errorf("actor factory already registered: %s", category)
	}
	am.factories.Store(category, f)
	return nil
}

// Start 启动状态机 sweeper：空闲休眠标记与超时关闭。幂等。
func (am *AManager) Start() {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.sweeperRun {
		return
	}
	am.sweeperCtx, am.sweeperStop = context.WithCancel(context.Background())
	am.sweeperRun = true
	if am.sched == nil {
		am.sched = timer.NewScheduler()
	}
	go am.sweepLoop()
}

// Stop 停止状态机 sweeper。幂等。
func (am *AManager) Stop() {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.sweeperRun {
		am.sweeperStop()
		am.sweeperRun = false
	}
	if am.sched != nil {
		am.sched.Stop()
		am.sched = nil
	}
}

func (am *AManager) sweepLoop() {
	ticker := time.NewTicker(SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-am.sweeperCtx.Done():
			return
		case <-ticker.C:
			am.sweepOnce()
		}
	}
}

func (am *AManager) sweepOnce() {
	am.actorMap.Range(func(key, value interface{}) bool {
		if ab, ok := value.(*ActorBase); ok {
			am.sweepActor(ab)
		}
		return true
	})
}

//		sweepActor 单 actor 状态推进：
//	  - Running 且空闲超时 → Sleeping（仅状态标签，在线永不因空闲关闭）；
//	  - Sleeping 且未附加连接且休眠超时 → 真正关闭（落盘 + 记 knownActors，位置保留供复活）。
func (am *AManager) sweepActor(ab *ActorBase) {
	if ab.State() == StateClosed || ab.idleTimeout <= 0 {
		return
	}
	now := time.Now()
	switch ab.State() {
	case StateRunning:
		if now.Sub(time.Unix(0, ab.lastActiveAt.Load())) >= ab.idleTimeout {
			ab.markSleeping()
		}
	case StateSleeping:
		if ab.attached.Load() {
			return // 在线永不因空闲关闭
		}
		if now.Sub(time.Unix(0, ab.sleepAt.Load())) < ab.idleTimeout {
			return
		}
		// 关闭前再次确认仍是休眠且未附加，避免与唤醒竞态
		if ab.State() != StateSleeping || ab.attached.Load() {
			return
		}
		if err := am.CloseActor(ab.id, CloseTimeout); err != nil && !errors.Is(err, ErrActorClose) {
			logger.ERROR("idle close actor error:", err, "uid:", ab.id)
			return
		}
		logger.INFO("actor closed by idle timeout, uid:", ab.id)
	}
}

// RegisterCategory 注册 Actor 类别。传入原型实例，通过 Category() 获取类别名。
func (am *AManager) RegisterCategory(prototype IActor) {
	am.mu.Lock()
	defer am.mu.Unlock()
	category := prototype.Category()
	if category == "" {
		logger.ERROR("RegisterCategory: empty category from prototype")
		return
	}
	if _, ok := am.categories.Load(category); ok {
		logger.ERROR("category already registered:", category)
		return
	}
	am.categories.Store(category, &categoryIndex{})
}

// addToCategories 将 actor 按 Category() 写入对应类别索引。
func (am *AManager) addToCategories(ab *ActorBase) {
	category := ab.iActor.Category()
	if category == "" {
		return
	}
	value, ok := am.categories.Load(category)
	if !ok {
		return
	}
	ci := value.(*categoryIndex)
	ci.actors.Store(ab.name, ab)
}

// removeFromCategories 将 actor 从对应类别索引中删除。
func (am *AManager) removeFromCategories(ab *ActorBase) {
	category := ab.iActor.Category()
	if category == "" {
		return
	}
	value, ok := am.categories.Load(category)
	if !ok {
		return
	}
	ci := value.(*categoryIndex)
	ci.actors.Delete(ab.name)
}

// Add 添加 Actor 到管理器：缓存空闲超时并刷新活动时间，自动按已注册类别建立索引。
// 可路由 actor 创建时原子认领位置：已被他节点占有时回滚刚创建的实例并返回错误。
func (am *AManager) Add(ab *ActorBase) error {
	ab.manager = am // 注入管理器引用，供 handler 内部 RPC 使用
	ab.idleTimeout = DefaultIdleTimeout
	if p, ok := ab.iActor.(IdleTimeoutProvider); ok {
		if t := p.IdleTimeout(); t > 0 {
			ab.idleTimeout = t
		} else {
			ab.idleTimeout = 0 // 显式禁用自动关闭
		}
	}
	ab.touchActivity()
	am.actorMap.Store(ab.name, ab)
	am.addToCategories(ab)
	if err := am.registerLocation(ab); err != nil {
		// 位置认领失败（已被他节点占有）：回滚刚创建的实例，避免残留双实例
		am.remove(ab.id)
		if serr := ab.Shutdown(CloseTimeout); serr != nil && !errors.Is(serr, ErrActorClose) {
			logger.ERROR("rollback actor shutdown failed:", serr, "uid:", ab.id)
		}
		return err
	}
	return nil
}

// remove 从主存储与类别索引移除（不含位置表与 knownActors）。
func (am *AManager) remove(uid int64) {
	key := uidKey(uid)
	value, ok := am.actorMap.Load(key)
	if !ok {
		return
	}
	ab, ok := value.(*ActorBase)
	if !ok {
		return
	}
	am.actorMap.Delete(key)
	am.removeFromCategories(ab)
}

// registerLocation 若 actor 可路由且已注入位置注册实现，则原子认领位置。
// 认领失败（已被他节点占有）返回错误，由 Add 回滚刚创建的实例。
func (am *AManager) registerLocation(ab *ActorBase) error {
	if _, ok := ab.iActor.(Routable); !ok {
		return nil
	}
	reg := am.locationRegSnapshot()
	if reg == nil {
		return nil
	}
	return reg.Register(ab.id)
}

// unregisterLocation 若 actor 可路由且已注入位置注册实现，则删除位置记录（彻底删除时用）。
func (am *AManager) unregisterLocation(ab *ActorBase) {
	if _, ok := ab.iActor.(Routable); !ok {
		return
	}
	reg := am.locationRegSnapshot()
	if reg == nil {
		return
	}
	if err := reg.Unregister(ab.id); err != nil {
		logger.ERROR("unregister actor location failed:", err, "uid:", ab.id)
	}
}

// CloseActor 关闭指定 Actor：停止并记入 knownActors（保留位置表，供 RPC 按需复活）。
func (am *AManager) CloseActor(uid int64, timeout time.Duration) error {
	ab := am.Find(uid)
	if ab == nil {
		return ErrActorNotFound
	}
	if cat := ab.iActor.Category(); cat != "" {
		am.knownActors.Store(uidKey(uid), cat)
	}
	err := ab.Shutdown(timeout)
	am.remove(uid)
	am.cancelActorTimers(uidKey(uid))
	if err != nil && !errors.Is(err, ErrActorClose) {
		logger.ERROR("close actor error:", err, "uid:", uid)
	}
	return err
}

// DeleteActor 彻底删除 actor：关闭 + 清初始化记录（内存 + Redis）+ 清位置表。
func (am *AManager) DeleteActor(uid int64, timeout time.Duration) error {
	ab := am.Find(uid)
	am.knownActors.Delete(uidKey(uid))
	if reg := am.initRegSnapshot(); reg != nil {
		_ = reg.Unregister(uid)
	}
	if ab == nil {
		if reg := am.locationRegSnapshot(); reg != nil {
			_ = reg.Unregister(uid)
		}
		return nil
	}
	am.unregisterLocation(ab)
	return am.CloseActor(uid, timeout)
}

// EnsureRunning 确保 uid 对应 actor 已运行（本地/跨节点 RPC 按需启动的统一入口）。
// 只允许 owner 节点调用：创建路径经位置原子认领（Add → registerLocation），
// 已被他节点占有时返回 ErrActorNotOwner 且不残留实例。
// category 仅在所有记录（内存 knownActors / Redis actor:init）均未命中时使用。
func (am *AManager) EnsureRunning(uid int64, category string) (*ActorBase, error) {
	if uid <= 0 {
		return nil, fmt.Errorf("invalid actor uid: %d", uid)
	}
	return am.ensureRunning(uid, category)
}

// EnsureDaemon 启动常驻 actor（保留 uid，如 RoomUID）：跳过 uid 合法性校验。
// 仅用于本地常驻、不参与路由的 actor（如每节点一个的 Room），不经过 RPC/路由路径。
func (am *AManager) EnsureDaemon(uid int64, category string) (*ActorBase, error) {
	return am.ensureRunning(uid, category)
}

func (am *AManager) ensureRunning(uid int64, category string) (*ActorBase, error) {
	key := uidKey(uid)
	if v, ok := am.actorMap.Load(key); ok {
		ab := v.(*ActorBase)
		if ab.State() == StateSleeping {
			ab.wake()
		}
		return ab, nil
	}
	cat, err := am.actorCategory(uid, category)
	if err != nil {
		return nil, err
	}
	if cat == "" {
		return nil, ErrActorNotFound
	}

	am.createMu.Lock()
	defer am.createMu.Unlock()
	// 双重检查：并发下可能已被其他协程创建
	if v, ok := am.actorMap.Load(key); ok {
		ab := v.(*ActorBase)
		if ab.State() == StateSleeping {
			ab.wake()
		}
		return ab, nil
	}
	return am.startByFactory(uid, cat)
}

// resolveLocal 解析本地 actor（category 为兜底类别），供 owner 节点路径使用
// （Deliver 客户端消息 / SendLocal / CallLocal / Cast/Call 的本地复活分支）。创建路径经位置原子认领，
// 已被他节点占有时由 Add 回滚并返回 ErrActorNotOwner。
// 判定「是否初始化过」：内存 knownActors → Redis actor:init → 兜底 category，
// 覆盖进程重启/跨节点「数据在 Redis、本进程内存无记录」的 actor。
func (am *AManager) resolveLocal(uid int64, category string) (*ActorBase, error) {
	if ab := am.Find(uid); ab != nil {
		if ab.State() == StateSleeping {
			ab.wake()
		}
		return ab, nil
	}
	cat, err := am.actorCategory(uid, category)
	if err != nil {
		return nil, err
	}
	if cat == "" {
		return nil, ErrActorNotFound
	}
	return am.EnsureRunning(uid, cat)
}

// actorCategory 获取 uid 的类别：内存 knownActors → Redis actor:init → 兜底参数。
func (am *AManager) actorCategory(uid int64, fallback string) (string, error) {
	key := uidKey(uid)
	if v, ok := am.knownActors.Load(key); ok {
		return v.(string), nil
	}
	if reg := am.initRegSnapshot(); reg != nil {
		cat, found, err := reg.Category(uid)
		if err != nil {
			return "", fmt.Errorf("query actor category failed: %w", err)
		}
		if found {
			return cat, nil
		}
	}
	return fallback, nil
}

// startByFactory 按类别工厂创建 actor 并启动。
func (am *AManager) startByFactory(uid int64, category string) (*ActorBase, error) {
	fv, ok := am.factories.Load(category)
	if !ok {
		return nil, fmt.Errorf("actor factory not registered: %s", category)
	}
	actor, err := fv.(ActorFactory)(uid)
	if err != nil {
		return nil, err
	}
	return am.startNew(uid, category, actor)
}

// startNew 创建并启动 actor：记录初始化（内存 + Redis）、注入定时器句柄、注册到管理器。
func (am *AManager) startNew(uid int64, category string, actor IActor) (*ActorBase, error) {
	key := uidKey(uid)
	am.knownActors.Store(key, category)
	if reg := am.initRegSnapshot(); reg != nil {
		if err := reg.Register(uid, category); err != nil {
			logger.ERROR("register actor init failed:", err, "uid:", uid)
		}
	}
	// 定时器句柄须在 NewActorBase（触发 Start）之前注入，保证 Start 内也能排定时器。
	if p, ok := actor.(SetTimerHandle); ok {
		p.SetTimerHandle(am.newTimerHandle(uid))
	}
	ab := NewActorBase(uid, actor)
	if ab == nil {
		return nil, fmt.Errorf("create actor base failed: %s", key)
	}
	actorRef := actor
	ab.clientHandler = func(ctx *ActorContext, msg interface{}) {
		HandleClientMsg(actorRef, ctx, msg)
	}
	if err := am.Add(ab); err != nil {
		return nil, err
	}
	return ab, nil
}

// timerHandle 绑定单个 actor 的定时器句柄：到期后向该 actor 自己投递消息。
type timerHandle struct {
	am  *AManager
	uid int64
	key string
}

// After 注册一次性定时消息：d 后投递 msg 到当前 actor 的 HandleMessage。
func (h *timerHandle) After(d time.Duration, msg interface{}) *timer.Task {
	return h.am.scheduler().After(h.key, d, func() {
		h.deliver(msg)
	})
}

// Every 注册周期定时消息：每隔 d 投递一次 msg，直到 Cancel。
func (h *timerHandle) Every(d time.Duration, msg interface{}) *timer.Task {
	return h.am.scheduler().Every(h.key, d, func() {
		h.deliver(msg)
	})
}

// Cancel 取消一个定时任务。
func (h *timerHandle) Cancel(task *timer.Task) {
	task.Stop()
}

func (h *timerHandle) deliver(msg interface{}) {
	if err := h.am.Cast(h.uid, "timer", msg); err != nil {
		// actor 可能已关闭，属正常竞态，DEBUG 级别即可
		logger.DEBUG("actor timer deliver failed:", err, "uid:", h.uid)
	}
}

// scheduler 惰性获取内存定时调度器。
func (am *AManager) scheduler() *timer.Scheduler {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.sched == nil {
		am.sched = timer.NewScheduler()
	}
	return am.sched
}

// cancelActorTimers 取消某 actor 的全部待执行定时器（关闭时调用）。
func (am *AManager) cancelActorTimers(key string) {
	am.mu.RLock()
	sched := am.sched
	am.mu.RUnlock()
	if sched != nil {
		sched.CancelAll(key)
	}
}

// newTimerHandle 创建绑定指定 uid 的定时器句柄。
func (am *AManager) newTimerHandle(uid int64) TimerHandle {
	return &timerHandle{am: am, uid: uid, key: uidKey(uid)}
}

// Detach 连接断开：actor 保留进入休眠（重连宽限开始计时），并向 actor 投递 ConnDetached 清理连接引用。
// 若位置已被其他节点接管（跨节点顶替的孤儿），直接关闭本地实例，不保留休眠。
func (am *AManager) Detach(uid int64) error {
	ab := am.Find(uid)
	if ab == nil {
		return ErrActorNotFound
	}
	if !ab.markDetached() {
		return ErrActorClose
	}
	if am.isOrphan(ab) {
		logger.INFO("actor location taken over by another node, close orphan, uid:", uid)
		return am.CloseActor(uid, CloseTimeout)
	}
	if err := am.SendSystem(uid, "server", ConnDetached{}); err != nil {
		logger.WARNING("detach send ConnDetached failed:", err, "uid:", uid)
	}
	return nil
}

// isOrphan 判断本地 actor 是否已成孤儿：可路由且位置已不属于本节点。
// 位置查询失败时视为非孤儿（不误关），等待其他路径兜底。
func (am *AManager) isOrphan(ab *ActorBase) bool {
	if _, ok := ab.iActor.(Routable); !ok {
		return false
	}
	reg := am.locationRegSnapshot()
	if reg == nil {
		return false
	}
	owner, err := reg.IsOwner(ab.id)
	if err != nil {
		logger.ERROR("check actor ownership failed:", err, "uid:", ab.id)
		return false
	}
	return !owner
}

// Attach 重连唤醒休眠 actor：置运行并注入新连接；actor 已关闭时返回 ErrActorClose。
func (am *AManager) Attach(uid int64, conn any) error {
	ab := am.Find(uid)
	if ab == nil {
		return ErrActorNotFound
	}
	if err := am.SendSystem(uid, "server", ConnAttached{Conn: conn}); err != nil {
		return err
	}
	if !ab.markAttached() {
		return ErrActorClose
	}
	return nil
}

// RangeByCategory 遍历指定类别的所有 Actor。回调返回 false 时停止遍历。
func (am *AManager) RangeByCategory(category string, fn func(name string, ab *ActorBase) bool) {
	value, ok := am.categories.Load(category)
	if !ok {
		return
	}
	ci := value.(*categoryIndex)
	ci.actors.Range(func(key, value interface{}) bool {
		ab, ok := value.(*ActorBase)
		if !ok {
			return true
		}
		return fn(key.(string), ab)
	})
}

// Find 根据 uid 查找运行中/休眠中的 Actor 实例。
func (am *AManager) Find(uid int64) *ActorBase {
	value, ok := am.actorMap.Load(uidKey(uid))
	if !ok {
		return nil
	}
	actor, ok := value.(*ActorBase)
	if !ok {
		return nil
	}
	return actor
}

// FindByCategory 按类别和 key（uidKey 字符串）查找 Actor 实例。
func (am *AManager) FindByCategory(category, key string) *ActorBase {
	value, ok := am.categories.Load(category)
	if !ok {
		return nil
	}
	ci := value.(*categoryIndex)
	actorVal, ok := ci.actors.Load(key)
	if !ok {
		return nil
	}
	ab, ok := actorVal.(*ActorBase)
	if !ok {
		return nil
	}
	return ab
}

// Range 遍历所有 Actor。回调返回 false 时停止遍历。
func (am *AManager) Range(fn func(*ActorBase) bool) {
	am.actorMap.Range(func(key, value interface{}) bool {
		ab, ok := value.(*ActorBase)
		if !ok {
			return true
		}
		return fn(ab)
	})
}

// Count 返回当前 Actor 总数。
func (am *AManager) Count() int {
	n := 0
	am.actorMap.Range(func(key, value interface{}) bool {
		n++
		return true
	})
	return n
}

// ShutdownActors 遍历关闭所有 Actor。
func (am *AManager) ShutdownActors(timeout time.Duration) {
	am.Range(func(ab *ActorBase) bool {
		if err := ab.Shutdown(timeout); err != nil {
			logger.ERROR("shutdown actor error:", err, "uid:", ab.id)
		}
		return true
	})
}

// Deliver 投递客户端消息（来自网络层）。msg 为已解析的消息信封。
func (am *AManager) Deliver(uid int64, msg *protocol.Msg) error {
	ab, err := am.resolveLocal(uid, "")
	if err != nil {
		return err
	}
	return am.deliver(ab, &Envelope{From: "server", Kind: MsgClient, Payload: msg})
}

// Cast 向其他 actor 异步投递消息（业务消息，计入活动时间）。
// 原则：本地 Find 命中直接投递（不创建、不访问远程）；未命中时查位置表（Redis actor:loc）——
//   - 位置归属本节点或无记录（可认领）→ 本地按需复活（不经过 gateway 往返）；
//   - 位置归属他节点 → 走远程路由，gateway 按位置转发到 owner 节点；
//   - 本地复活时位置刚被接管（ErrActorNotOwner）→ 回退远程路由（gateway 按最新位置转发）。
func (am *AManager) Cast(to int64, from string, msg interface{}) error {
	if ab := am.Find(to); ab != nil {
		return am.deliver(ab, &Envelope{From: from, Kind: MsgSend, Payload: msg})
	}
	if am.shouldReviveLocal(to) {
		if err := am.SendLocal(to, "", from, msg); err == nil || !errors.Is(err, ErrActorNotOwner) {
			return err
		}
	}
	if r := am.remoteRouterSnapshot(); r != nil {
		return r.Cast(to, from, msg)
	}
	return ErrActorNotFound
}

// SendLocal 本地异步投递（不带远程回退），供远端 RPC 服务端（rpc.Serve）调用。
// 请求已到达 owner 节点，允许按需启动并投递；创建路径经位置原子认领，
// 非 owner 节点调用将返回 ErrActorNotOwner（不会残留实例）。
// category 为兜底类别：本地无记录时据此按需创建/复活，避免依赖进程内存判断。
func (am *AManager) SendLocal(to int64, category string, from string, msg interface{}) error {
	ab, err := am.resolveLocal(to, category)
	if err != nil {
		return err
	}
	return am.deliver(ab, &Envelope{From: from, Kind: MsgSend, Payload: msg})
}

// SendSystem 投递系统消息（PersistTick/连接生命周期），不刷新业务活动时间。
// 只投递本地已存在的实例（Find），系统消息不创建/复活 actor。
func (am *AManager) SendSystem(to int64, from string, msg interface{}) error {
	ab := am.Find(to)
	if ab == nil {
		return ErrActorNotFound
	}
	return am.deliver(ab, &Envelope{From: from, Kind: MsgSend, Payload: msg, System: true})
}

// Call 向其他 actor 发起 RPC 并等待回复。
// 原则：本地 Find 命中直接本地调用（不创建、不访问远程）；未命中时查位置表（Redis actor:loc）——
//   - 位置归属本节点或无记录（可认领）→ 本地按需复活（不经过 gateway 往返）；
//   - 位置归属他节点 → 走远程路由，gateway 按位置转发到 owner 节点；
//   - 本地复活时位置刚被接管（ErrActorNotOwner）→ 回退远程路由（gateway 按最新位置转发）。
func (am *AManager) Call(to int64, from string, msg interface{}, timeout time.Duration) (interface{}, error) {
	if ab := am.Find(to); ab != nil {
		return am.callLocal(ab, to, from, msg, timeout)
	}
	if am.shouldReviveLocal(to) {
		if res, err := am.CallLocal(to, "", from, msg, timeout); err == nil || !errors.Is(err, ErrActorNotOwner) {
			return res, err
		}
	}
	if r := am.remoteRouterSnapshot(); r != nil {
		return r.Call(to, from, msg, timeout)
	}
	return nil, ErrActorNotFound
}

// shouldReviveLocal 本地 Find 未命中时是否应在本地复活：
// 位置归属本节点或无记录（可认领）→ true；位置归属他节点、未注入位置表或查询失败 → false
// （查询失败保守走远程路由，由 gateway 再次解析）。
func (am *AManager) shouldReviveLocal(uid int64) bool {
	reg := am.locationRegSnapshot()
	if reg == nil {
		return false
	}
	owner, err := reg.IsOwner(uid)
	if err != nil {
		return false
	}
	return owner
}

// CallLocal 本地 RPC（不带远程回退），供远端 RPC 服务端（rpc.Serve）调用。
// 请求已到达 owner 节点，允许按需启动并投递；创建路径经位置原子认领，
// 非 owner 节点调用将返回 ErrActorNotOwner（不会残留实例）。
// category 为兜底类别：本地无记录时据此按需创建/复活，避免依赖进程内存判断。
func (am *AManager) CallLocal(to int64, category string, from string, msg interface{}, timeout time.Duration) (interface{}, error) {
	ab, err := am.resolveLocal(to, category)
	if err != nil {
		return nil, err
	}
	return am.callLocal(ab, to, from, msg, timeout)
}

func (am *AManager) callLocal(ab *ActorBase, to int64, from string, msg interface{}, timeout time.Duration) (interface{}, error) {
	if ab.isClosed() {
		return nil, ErrActorClose
	}

	env := &Envelope{
		From:    from,
		Kind:    MsgCall,
		Payload: msg,
		Reply:   make(chan interface{}, 1),
	}
	if err := am.deliver(ab, env); err != nil {
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case resp := <-env.Reply:
		if ce, ok := resp.(*CallError); ok {
			return nil, ce
		}
		return resp, nil
	case <-timer.C:
		return nil, ErrSendTimeout
	}
}

// deliver 投递信封到目标 actor 队列。
// 业务消息（非 System）先刷新活动时间（唤醒休眠 actor）再入队；
// 入队时监听 stop 通道，避免与关闭竞态导致阻塞或 panic。
func (am *AManager) deliver(ab *ActorBase, env *Envelope) error {
	if ab.isClosed() {
		return ErrActorClose
	}
	if !env.System {
		ab.touchActivity()
	}
	timer := time.NewTimer(SendTimeout)
	defer timer.Stop()
	select {
	case ab.request <- env:
		return nil
	case <-ab.stop:
		return ErrActorClose
	case <-timer.C:
		return ErrSendTimeout
	}
}
