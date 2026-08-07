package tool

import (
	"runtime/debug"
	"sync"

	"simple_game/game/pkg/logger"
)

// Go 启动一个安全 goroutine：捕获 panic 并记录日志与堆栈，
// 保证单个 goroutine 崩溃不会影响整个进程。
func Go(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.ERROR("tool.Go panic:", r, "\n", string(debug.Stack()))
			}
		}()
		fn()
	}()
}

// Map 泛型并发安全的 map，内部使用 RWMutex 保护，适合多 goroutine 读写。
type Map[K comparable, V any] struct {
	mu sync.RWMutex
	m  map[K]V
}

// NewMap 创建一个空的并发 map。
func NewMap[K comparable, V any]() *Map[K, V] {
	return &Map[K, V]{m: make(map[K]V)}
}

// Load 返回 key 对应的值；key 不存在时 ok 为 false。
func (m *Map[K, V]) Load(key K) (V, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.m[key]
	return v, ok
}

// Store 设置 key 对应的值。
func (m *Map[K, V]) Store(key K, value V) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m[key] = value
}

// Delete 删除 key。
func (m *Map[K, V]) Delete(key K) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.m, key)
}

// LoadOrStore 返回 key 对应的值；key 不存在时写入 value 并返回 false。
func (m *Map[K, V]) LoadOrStore(key K, value V) (V, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.m[key]; ok {
		return v, true
	}
	m.m[key] = value
	return value, false
}

// Range 遍历 map，fn 返回 false 时停止遍历；fn 内不要调用本 map 的方法，避免锁重入死锁。
func (m *Map[K, V]) Range(fn func(key K, value V) bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for k, v := range m.m {
		if !fn(k, v) {
			break
		}
	}
}

// Len 返回元素数量。
func (m *Map[K, V]) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.m)
}

// Once 泛型只执行一次的延迟初始化：fn 成功后缓存结果，失败可重试。
type Once[T any] struct {
	mu   sync.Mutex
	done bool
	val  T
	err  error
}

// Do 执行 fn 并缓存结果；成功后再次调用直接返回缓存值，失败则下次调用重新执行。
func (o *Once[T]) Do(fn func() (T, error)) (T, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.done {
		return o.val, o.err
	}
	o.val, o.err = fn()
	if o.err == nil {
		o.done = true
	}
	return o.val, o.err
}
