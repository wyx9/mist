package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"simple_game/game/internal/rpc"
	"simple_game/game/pkg/actortype"
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
	actorMap     sync.Map // uidKey → *ActorBase 主存储（运行中/休眠中）
	knownActors  sync.Map // uidKey → actortype.ActorType（已初始化过，关闭后仍保留，供按需复活）
	categories   sync.Map // actortype.ActorType → *categoryIndex
	factories    sync.Map // actortype.ActorType → ActorFactory
	mu           sync.RWMutex
	createMu     sync.Mutex        // 串行化「工厂创建 + 启动」，避免同 uid 并发重复创建
	sched        *timer.Scheduler  // actor 内部内存定时调度器（惰性创建）
	faultHandler func(uid int64) // actor 故障回调（handler 超时等），由 gameserver 设置
	remoteRouter rpc.RemoteRouter  // 跨进程路由（本地未命中时委托），由 gameserver 注入
	sweeperCtx   context.Context
	sweeperStop  context.CancelFunc
	sweeperRun   bool
	addr         string
}

// SetFaultHandler 注册 actor 故障回调（如 handler 超时），用于踢掉故障玩家的连接。
func (am *AManager) SetFaultHandler(fn func(uid int64)) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.faultHandler = fn
}

// notifyFault 通知故障回调（不阻塞，读取回调快照后调用）。
func (am *AManager) notifyFault(uid int64) {
	am.mu.RLock()
	fn := am.faultHandler
	am.mu.RUnlock()
	if fn != nil {
		fn(uid)
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

// SetAddr 设置本节点注册地址，供位置注册/认领使用；须在启动任何 actor 前设置。
func (am *AManager) SetAddr(addr string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.addr = addr
}

// addrSnapshot 读取本节点注册地址快照。
func (am *AManager) addrSnapshot() string {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.addr
}

// RegisterFactory 注册某类 actor 的工厂（按需启动时使用）。
func (am *AManager) RegisterFactory(actorType actortype.ActorType, f ActorFactory) error {
	if actorType == "" {
		return errors.New("empty actor type")
	}
	if _, ok := am.factories.Load(actorType); ok {
		return fmt.Errorf("actor factory already registered: %s", actorType)
	}
	am.factories.Store(actorType, f)
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

// sweepActor 单 actor 状态推进：运行空闲超时 → 休眠；休眠超时且未附加 → 关闭。
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

// RegisterCategory 注册 Actor 类型。传入原型实例，通过 Type() 获取类型标识。
func (am *AManager) RegisterCategory(prototype IActor) {
	am.mu.Lock()
	defer am.mu.Unlock()
	actorType := prototype.Type()
	if actorType == "" {
		logger.ERROR("RegisterCategory: empty type from prototype")
		return
	}
	if _, ok := am.categories.Load(actorType); ok {
		logger.ERROR("category already registered:", actorType)
		return
	}
	am.categories.Store(actorType, &categoryIndex{})
}

// addToCategories 将 actor 按 Type() 写入对应类型索引。
func (am *AManager) addToCategories(ab *ActorBase) {
	actorType := ab.Type()
	if actorType == "" {
		return
	}
	value, ok := am.categories.Load(actorType)
	if !ok {
		return
	}
	ci := value.(*categoryIndex)
	ci.actors.Store(uidKey(ab.id), ab)
}

// removeFromCategories 将 actor 从对应类型索引中删除。
func (am *AManager) removeFromCategories(ab *ActorBase) {
	actorType := ab.Type()
	if actorType == "" {
		return
	}
	value, ok := am.categories.Load(actorType)
	if !ok {
		return
	}
	ci := value.(*categoryIndex)
	ci.actors.Delete(uidKey(ab.id))
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
	am.actorMap.Store(uidKey(ab.id), ab)
	am.addToCategories(ab)
	// 注册节点：仅可路由 actor 参与位置表，Room 等本地常驻 actor 跳过。
	if _, ok := ab.iActor.(Routable); ok {
		if err := am.RegisterAddr(ab.id); err != nil {
			// 位置认领失败（已被他节点占有）：回滚刚创建的实例，避免残留双实例
			am.remove(ab.id)
			if serr := ab.Shutdown(CloseTimeout); serr != nil && !errors.Is(serr, ErrActorClose) {
				logger.ERROR("rollback actor shutdown failed:", serr, "uid:", ab.id)
			}
			return err
		}
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

// unregisterLocation 若 actor 可路由，删除位置记录（彻底删除时用）。

// unregisterLocation 若 actor 可路由，删除位置记录（彻底删除时用）。
func (am *AManager) unregisterLocation(ab *ActorBase) {
	if _, ok := ab.iActor.(Routable); !ok {
		return
	}
	if err := am.UnregisterAddr(ab.id); err != nil {
		logger.ERROR("unregister actor location failed:", err, "uid:", ab.id)
	}
}

// isOrphan 判断本地 actor 是否已成孤儿：可路由且位置已不属于本节点。
// 位置查询失败时视为非孤儿（不误关），等待其他路径兜底。
func (am *AManager) isOrphan(ab *ActorBase) bool {
	if _, ok := ab.iActor.(Routable); !ok {
		return false
	}
	owner, err := am.IsOwner(ab.id)
	if err != nil {
		logger.ERROR("check actor ownership failed:", err, "uid:", ab.id)
		return false
	}
	return !owner
}

// CloseActor 关闭指定 Actor：停止并记入 knownActors（保留位置表，供 RPC 按需复活）。
func (am *AManager) CloseActor(uid int64, timeout time.Duration) error {
	ab := am.Find(uid)
	if ab == nil {
		return ErrActorNotFound
	}
	if cat := ab.Type(); cat != "" {
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
	am.knownActors.Delete(uidKey(uid))
	if err := am.UnregisterInit(uid); err != nil {
		logger.ERROR("unregister actor init failed:", err, "uid:", uid)
	}
	ab := am.Find(uid)
	if ab == nil {
		if err := am.UnregisterAddr(uid); err != nil {
			logger.ERROR("unregister actor location failed:", err, "uid:", uid)
		}
		return nil
	}
	am.unregisterLocation(ab)
	return am.CloseActor(uid, timeout)
}

// EnsureRunning 确保 uid 的 actor 已运行（本地/跨节点 RPC 按需启动统一入口）。
// 仅 owner 节点可创建：位置认领失败返回 ErrActorNotOwner 且不残留实例；actorType 为兜底类型。
func (am *AManager) EnsureRunning(uid int64, actorType actortype.ActorType) (*ActorBase, error) {
	if uid <= 0 {
		return nil, fmt.Errorf("invalid actor uid: %d", uid)
	}
	return am.ensureRunning(uid, actorType)
}

// EnsureDaemon 启动常驻 actor（保留 uid，如 RoomUID）：跳过 uid 合法性校验。
// 仅用于本地常驻、不参与路由的 actor（如每节点一个的 Room），不经过 RPC/路由路径。
func (am *AManager) EnsureDaemon(uid int64, actorType actortype.ActorType) (*ActorBase, error) {
	return am.ensureRunning(uid, actorType)
}

func (am *AManager) ensureRunning(uid int64, actorType actortype.ActorType) (*ActorBase, error) {
	key := uidKey(uid)
	if v, ok := am.actorMap.Load(key); ok {
		ab := v.(*ActorBase)
		if ab.State() == StateSleeping {
			ab.wake()
		}
		return ab, nil
	}
	cat, err := am.actorCategory(uid, actorType)
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

// actorCategory 获取 uid 的 actor 类型：内存 knownActors → Redis actor:init → 兜底参数。
// 只在 EnsureRunning（唯一启动入口）内部调用，避免重复查询。
func (am *AManager) actorCategory(uid int64, fallback actortype.ActorType) (actortype.ActorType, error) {
	key := uidKey(uid)
	if v, ok := am.knownActors.Load(key); ok {
		return v.(actortype.ActorType), nil
	}
	cat, found, err := am.GetActorCategory(uid)
	if err != nil {
		return "", fmt.Errorf("query actor category failed: %w", err)
	}
	if found {
		return cat, nil
	}
	return fallback, nil
}

// startByFactory 按类型工厂创建 actor 并启动。
func (am *AManager) startByFactory(uid int64, actorType actortype.ActorType) (*ActorBase, error) {
	fv, ok := am.factories.Load(actorType)
	if !ok {
		return nil, fmt.Errorf("actor factory not registered: %s", actorType)
	}
	actor, err := fv.(ActorFactory)(uid)
	if err != nil {
		return nil, err
	}
	return am.startNew(uid, actorType, actor)
}

// startNew 创建并启动 actor：记录初始化（内存 + Redis）、注入定时器句柄、注册到管理器。
func (am *AManager) startNew(uid int64, actorType actortype.ActorType, actor IActor) (*ActorBase, error) {
	key := uidKey(uid)
	am.knownActors.Store(key, actorType)
	if err := am.RegisterInit(uid, actorType); err != nil {
		logger.ERROR("register actor init failed:", err, "uid:", uid)
	}
	// 定时器句柄须在 NewActorBase（触发 Start）之前注入，保证 Start 内也能排定时器。
	if p, ok := actor.(SetTimerHandle); ok {
		p.SetTimerHandle(am.newTimerHandle(uid))
	}
	ab := NewActorBase(uid, actor)
	if ab == nil {
		return nil, fmt.Errorf("create actor base failed: %s", key)
	}
	ab.clientHandler = func(ctx *ActorContext, msg interface{}) {
		HandleClientMsg(actor, ctx, msg)
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

// RangeByType 遍历指定类型的所有 Actor。回调返回 false 时停止遍历。
func (am *AManager) RangeByType(actorType actortype.ActorType, fn func(*ActorBase) bool) {
	value, ok := am.categories.Load(actorType)
	if !ok {
		return
	}
	ci := value.(*categoryIndex)
	ci.actors.Range(func(key, value interface{}) bool {
		ab, ok := value.(*ActorBase)
		if !ok {
			return true
		}
		return fn(ab)
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
// 客户端连接所在节点即 owner，未命中时按需启动（EnsureRunning）。
func (am *AManager) Deliver(uid int64, msg *protocol.Msg) error {
	ab, err := am.EnsureRunning(uid, "")
	if err != nil {
		return err
	}
	return am.deliver(ab, &Envelope{From: "server", Kind: MsgClient, Payload: msg})
}

// Cast 异步投递：本地命中直接投递，否则交 remoteRouter（gateway）跨节点寻址。
func (am *AManager) Cast(to int64, from string, msg interface{}) error {
	if ab := am.Find(to); ab != nil {
		return am.deliver(ab, &Envelope{From: from, Kind: MsgSend, Payload: msg})
	}
	if r := am.remoteRouterSnapshot(); r != nil {
		return r.Cast(to, from, msg)
	}
	return ErrActorNotFound
}

// SendLocal 本地异步投递（无远程回退，rpc.Serve 入口）：允许按需启动/复活，
// 非 owner 节点调用返回 ErrActorNotOwner。actorType 仅作兜底类型。
func (am *AManager) SendLocal(to int64, actorType actortype.ActorType, from string, msg interface{}) error {
	ab, err := am.EnsureRunning(to, actorType)
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

// Call 同步调用：本地命中直接调用，否则交 remoteRouter（gateway）跨节点寻址。
func (am *AManager) Call(to int64, from string, msg interface{}, timeout time.Duration) (interface{}, error) {
	if ab := am.Find(to); ab != nil {
		return am.callLocal(ab, to, from, msg, timeout)
	}
	if r := am.remoteRouterSnapshot(); r != nil {
		return r.Call(to, from, msg, timeout)
	}
	return nil, ErrActorNotFound
}

// CallLocal 本地 RPC（无远程回退，rpc.Serve 入口）：允许按需启动/复活，
// 非 owner 节点调用返回 ErrActorNotOwner。actorType 仅作兜底类型。
func (am *AManager) CallLocal(to int64, actorType actortype.ActorType, from string, msg interface{}, timeout time.Duration) (interface{}, error) {
	ab, err := am.EnsureRunning(to, actorType)
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

// deliver 投递信封：业务消息先刷新活动时间并唤醒休眠 actor；入队时监听 stop 防关闭竞态。
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
