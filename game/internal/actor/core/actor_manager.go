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
	"simple_game/game/pkg/uid"
)

// categoryIndex 单个类别的索引结构。
type categoryIndex struct {
	actors sync.Map // key(string) → *ActorBase
}

// AManager Actor 管理器：全局单例，负责所有 Actor 的存储、查找、消息投递与生命周期状态机。
type AManager struct {
	actorMap     sync.Map // name → *ActorBase 主存储
	categories   sync.Map // categoryName(string) → *categoryIndex
	mu           sync.RWMutex
	sched        *timer.Scheduler  // actor 内部内存定时调度器（惰性创建）
	faultHandler func(name string) // actor 故障回调（handler 超时等），由 gameserver 设置
	remoteRouter rpc.RemoteRouter  // 跨进程路由（本地未命中时委托），由 gameserver 注入
	locationReg  LocationRegistry  // 可路由 actor 位置注册，由 gameserver 注入
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
//	  - Sleeping 且未附加连接且休眠超时 → 真正关闭（落盘 + 移除）。
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
		if err := am.CloseActor(ab.name, CloseTimeout); err != nil && !errors.Is(err, ErrActorClose) {
			logger.ERROR("idle close actor error:", err, "name:", ab.name)
			return
		}
		logger.INFO("actor closed by idle timeout, name:", ab.name)
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
	am.registerLocation(ab)
	return nil
}

// Remove 从管理器中移除 Actor。从主存储和所有类别索引中删除。
func (am *AManager) Remove(name string) {
	value, ok := am.actorMap.Load(name)
	if !ok {
		return
	}
	ab, ok := value.(*ActorBase)
	if !ok {
		return
	}
	am.actorMap.Delete(name)
	am.removeFromCategories(ab)
	am.unregisterLocation(ab)
}

// registerLocation 若 actor 可路由且已注入位置注册实现，则写入位置表。
func (am *AManager) registerLocation(ab *ActorBase) {
	if _, ok := ab.iActor.(Routable); !ok {
		return
	}
	am.mu.RLock()
	reg := am.locationReg
	am.mu.RUnlock()
	if reg == nil {
		return
	}
	if err := reg.Register(ab.name); err != nil {
		logger.ERROR("register actor location failed:", err, "actor:", ab.name)
	}
}

// unregisterLocation 若 actor 可路由且已注入位置注册实现，则删除位置记录。
func (am *AManager) unregisterLocation(ab *ActorBase) {
	if _, ok := ab.iActor.(Routable); !ok {
		return
	}
	am.mu.RLock()
	reg := am.locationReg
	am.mu.RUnlock()
	if reg == nil {
		return
	}
	if err := reg.Unregister(ab.name); err != nil {
		logger.ERROR("unregister actor location failed:", err, "actor:", ab.name)
	}
}

// CloseActor 关闭指定 Actor 并从管理器中移除，Actor 生命周期统一由管理器负责。
func (am *AManager) CloseActor(name string, timeout time.Duration) error {
	ab := am.Find(name)
	if ab == nil {
		return ErrActorNotFound
	}
	err := ab.Shutdown(timeout)
	// 无论关闭结果如何都从注册表移除，避免残留已关闭的 Actor。
	am.Remove(name)
	am.cancelActorTimers(name)
	if err != nil && !errors.Is(err, ErrActorClose) {
		logger.ERROR("close actor error:", err, "name:", name)
	}
	return err
}

// timerHandle 绑定单个 actor 的定时器句柄：到期后向该 actor 自己投递消息。
type timerHandle struct {
	am   *AManager
	name string
}

// After 注册一次性定时消息：d 后投递 msg 到当前 actor 的 HandleMessage。
func (h *timerHandle) After(d time.Duration, msg interface{}) *timer.Task {
	return h.am.scheduler().After(h.name, d, func() {
		h.deliver(msg)
	})
}

// Every 注册周期定时消息：每隔 d 投递一次 msg，直到 Cancel。
func (h *timerHandle) Every(d time.Duration, msg interface{}) *timer.Task {
	return h.am.scheduler().Every(h.name, d, func() {
		h.deliver(msg)
	})
}

// Cancel 取消一个定时任务。
func (h *timerHandle) Cancel(task *timer.Task) {
	task.Stop()
}

func (h *timerHandle) deliver(msg interface{}) {
	if err := h.am.Cast(h.name, "timer", msg); err != nil {
		// actor 可能已关闭，属正常竞态，DEBUG 级别即可
		logger.DEBUG("actor timer deliver failed:", err, "actor:", h.name)
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
func (am *AManager) cancelActorTimers(name string) {
	am.mu.RLock()
	sched := am.sched
	am.mu.RUnlock()
	if sched != nil {
		sched.CancelAll(name)
	}
}

// Detach 连接断开：actor 保留进入休眠（重连宽限开始计时），并向 actor 投递 ConnDetached 清理连接引用。
func (am *AManager) Detach(name string) error {
	ab := am.Find(name)
	if ab == nil {
		return ErrActorNotFound
	}
	if !ab.markDetached() {
		return ErrActorClose
	}
	if err := am.SendSystem(name, "server", ConnDetached{}); err != nil {
		logger.WARNING("detach send ConnDetached failed:", err, "actor:", name)
	}
	return nil
}

// Attach 重连唤醒休眠 actor：置运行并注入新连接；actor 已关闭时返回 ErrActorClose。
func (am *AManager) Attach(name string, conn any) error {
	ab := am.Find(name)
	if ab == nil {
		return ErrActorNotFound
	}
	if err := am.SendSystem(name, "server", ConnAttached{Conn: conn}); err != nil {
		return err
	}
	if !ab.markAttached() {
		return ErrActorClose
	}
	return nil
}

// StartActor 创建并启动一个 Actor，注册到管理器。
// 客户端消息默认走 HandleClientMsg：按具体 actor 类型注入 clientHandler，
// 响应通过 actor 的 WriteMessage 能力（clientMsgWriter）自动回包。
// 注意：Go 方法不支持类型参数，因此这是包级泛型函数。
func StartActor[A IActor](am *AManager, name string, actor A) (*ActorBase, error) {
	// 框架级身份保证：每个 actor 都有全局唯一 uid。
	// 具体 actor 若实现 ActorID() 且返回非零（如 Player 使用账号 uid），则沿用；否则自动分配 uid。
	id := uid.NewUid()
	if p, ok := any(actor).(interface{ ActorID() int64 }); ok && p.ActorID() != 0 {
		id = p.ActorID()
	}
	// 定时器句柄须在 NewActorBase（触发 Start）之前注入，保证 Start 内也能排定时器。
	if p, ok := any(actor).(SetTimerHandle); ok {
		p.SetTimerHandle(am.newTimerHandle(name))
	}
	ab := NewActorBase(name, actor)
	if ab == nil {
		return nil, fmt.Errorf("create actor base failed: %s", name)
	}
	ab.SetID(id)
	actorRef := actor
	ab.clientHandler = func(ctx *ActorContext, msg interface{}) {
		HandleClientMsg(actorRef, ctx, msg)
	}
	if err := am.Add(ab); err != nil {
		return nil, err
	}
	return ab, nil
}

// newTimerHandle 创建绑定指定 actor 名的定时器句柄。
func (am *AManager) newTimerHandle(name string) TimerHandle {
	return &timerHandle{am: am, name: name}
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

// Find 根据名称查找 Actor 实例。
func (am *AManager) Find(name string) *ActorBase {
	value, ok := am.actorMap.Load(name)
	if !ok {
		return nil
	}
	actor, ok := value.(*ActorBase)
	if !ok {
		return nil
	}
	return actor
}

// FindByCategory 按类别和 key 查找 Actor 实例。
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
			logger.ERROR("shutdown actor error:", err, "name:", ab.name)
		}
		return true
	})
}

// Deliver 投递客户端消息（来自网络层）。msg 为已解析的消息信封。
func (am *AManager) Deliver(name string, msg *protocol.Msg) error {
	return am.deliver(name, &Envelope{From: "server", Kind: MsgClient, Payload: msg})
}

// Cast 向其他 actor 异步投递消息（业务消息，计入活动时间）。
// 本地未命中时经跨进程路由转发（透明跨进程）。
func (am *AManager) Cast(to, from string, msg interface{}) error {
	if ab := am.Find(to); ab != nil {
		return am.deliver(to, &Envelope{From: from, Kind: MsgSend, Payload: msg})
	}
	if r := am.remoteRouterSnapshot(); r != nil {
		return r.Cast(to, from, msg)
	}
	return ErrActorNotFound
}

// SendLocal 本地异步投递（不带远程回退），供远端 RPC 服务端（rpc.Serve）调用，
// 避免远端请求在本节点再次触发远端路由造成环路。
func (am *AManager) SendLocal(to, from string, msg interface{}) error {
	return am.deliver(to, &Envelope{From: from, Kind: MsgSend, Payload: msg})
}

// SendSystem 投递系统消息（PersistTick/连接生命周期），不刷新业务活动时间。
func (am *AManager) SendSystem(to, from string, msg interface{}) error {
	return am.deliver(to, &Envelope{From: from, Kind: MsgSend, Payload: msg, System: true})
}

// Call 向其他 actor 发起 RPC 并等待回复。
// 本地未命中时经跨进程路由转发（透明跨进程）。
func (am *AManager) Call(to, from string, msg interface{}, timeout time.Duration) (interface{}, error) {
	if to == from {
		return nil, ErrSelfCall
	}
	if ab := am.Find(to); ab != nil {
		return am.callLocal(ab, to, from, msg, timeout)
	}
	if r := am.remoteRouterSnapshot(); r != nil {
		return r.Call(to, from, msg, timeout)
	}
	return nil, ErrActorNotFound
}

// CallLocal 本地 RPC（不带远程回退），供远端 RPC 服务端（rpc.Serve）调用，
// 避免远端请求在本节点再次触发远端路由造成环路。
func (am *AManager) CallLocal(to, from string, msg interface{}, timeout time.Duration) (interface{}, error) {
	ab := am.Find(to)
	if ab == nil {
		return nil, ErrActorNotFound
	}
	return am.callLocal(ab, to, from, msg, timeout)
}

func (am *AManager) callLocal(ab *ActorBase, to, from string, msg interface{}, timeout time.Duration) (interface{}, error) {
	if ab.isClosed() {
		return nil, ErrActorClose
	}

	env := &Envelope{
		From:    from,
		Kind:    MsgCall,
		Payload: msg,
		Reply:   make(chan interface{}, 1),
	}
	if err := am.deliver(to, env); err != nil {
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
func (am *AManager) deliver(name string, env *Envelope) error {
	ab := am.Find(name)
	if ab == nil {
		return ErrActorNotFound
	}
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
