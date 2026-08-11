// Package timer 定时器系统：actor 内部内存定时器（scheduler.go）与 Redis 全局持久化定时器（global.go）。
package timer

import (
	"context"
	"sync"
	"time"
)

// Task 一个已注册定时任务的句柄；Stop 后不再触发。
type Task struct {
	owner  string
	cancel context.CancelFunc
	s      *Scheduler
}

// Stop 取消该任务并从注册表中移除；幂等。
func (t *Task) Stop() {
	t.cancel()
	t.s.unregister(t)
}

// Scheduler 内存定时调度器：按 owner 登记任务，支持一次/周期与按 owner 批量取消。
// 每个任务一个协程 + time.NewTimer；fn 在任务协程中执行，由调用方保证线程安全。
// 纯内存，进程重启即消失。
type Scheduler struct {
	mu    sync.Mutex
	tasks map[string]map[*Task]struct{} // owner -> 任务集合
}

// NewScheduler 创建调度器。
func NewScheduler() *Scheduler {
	return &Scheduler{tasks: make(map[string]map[*Task]struct{})}
}

// After 注册一次性任务：d 后执行 fn。
func (s *Scheduler) After(owner string, d time.Duration, fn func()) *Task {
	return s.add(owner, d, fn, false)
}

// Every 注册周期任务：每隔 d 执行一次 fn，直到 Stop/CancelAll。
func (s *Scheduler) Every(owner string, d time.Duration, fn func()) *Task {
	return s.add(owner, d, fn, true)
}

func (s *Scheduler) add(owner string, d time.Duration, fn func(), periodic bool) *Task {
	if d <= 0 {
		d = time.Millisecond
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &Task{owner: owner, cancel: cancel, s: s}
	s.register(t)
	go func() {
		tm := time.NewTimer(d)
		defer tm.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tm.C:
				fn()
				if !periodic {
					return
				}
				tm.Reset(d)
			}
		}
	}()
	return t
}

func (s *Scheduler) register(t *Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.tasks[t.owner]
	if !ok {
		m = make(map[*Task]struct{})
		s.tasks[t.owner] = m
	}
	m[t] = struct{}{}
}

func (s *Scheduler) unregister(t *Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.tasks[t.owner]; ok {
		delete(m, t)
		if len(m) == 0 {
			delete(s.tasks, t.owner)
		}
	}
}

// CancelAll 取消某 owner 的全部定时任务（如 actor 关闭时清理）。
func (s *Scheduler) CancelAll(owner string) {
	s.mu.Lock()
	m := s.tasks[owner]
	delete(s.tasks, owner)
	s.mu.Unlock()
	for t := range m {
		t.cancel()
	}
}

// Stop 停止全部定时任务。
func (s *Scheduler) Stop() {
	s.mu.Lock()
	all := s.tasks
	s.tasks = make(map[string]map[*Task]struct{})
	s.mu.Unlock()
	for _, m := range all {
		for t := range m {
			t.cancel()
		}
	}
}
