// game/engine/worker.go
package dispatch

import (
	"hash/fnv"
	"runtime"

	"simple_game/game/pkg/logger"
)

// WorkerPool 消息分发工作池。将 Gateway 过来的请求按 tunnelID 哈希
// 分配到固定 worker goroutine，保证同一玩家的消息有序处理。
type WorkerPool struct {
	workers []chan func()
	closed  chan struct{}
}

// NewWorkerPool 创建 worker 池。n 为 worker 数量，n<=0 时取 CPU 核心数。
func NewWorkerPool(n int) *WorkerPool {
	if n <= 0 {
		n = runtime.NumCPU()
	}
	wp := &WorkerPool{
		workers: make([]chan func(), n),
		closed:  make(chan struct{}),
	}
	for i := 0; i < n; i++ {
		ch := make(chan func(), 1024)
		wp.workers[i] = ch
		go wp.workerLoop(i, ch)
	}
	logger.INFO("WorkerPool started with", n, "workers")
	return wp
}

// workerLoop 单个 worker 的主循环，顺序执行投递到它 channel 的任务。
func (wp *WorkerPool) workerLoop(id int, ch <-chan func()) {
	for {
		select {
		case task, ok := <-ch:
			if !ok {
				return
			}
			task()
		case <-wp.closed:
			return
		}
	}
}

// Submit 按 tunnelID 哈希将任务投递到固定 worker。同一 tunnelID 的消息
// 始终由同一 worker 处理，保证有序。
func (wp *WorkerPool) Submit(tunnelID string, task func()) {
	idx := wp.hash(tunnelID) % uint32(len(wp.workers))
	select {
	case wp.workers[idx] <- task:
	default:
		logger.ERROR("WorkerPool: worker", idx, "queue full, dropping task for", tunnelID)
	}
}

// hash 计算 tunnelID 的 FNV-1a 哈希值。
func (wp *WorkerPool) hash(tunnelID string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(tunnelID))
	return h.Sum32()
}

// Shutdown 关闭所有 worker。
func (wp *WorkerPool) Shutdown() {
	close(wp.closed)
	for _, ch := range wp.workers {
		close(ch)
		// 排空 channel 中残留的任务，避免 goroutine 泄漏
		for range ch {
		}
	}
	logger.INFO("WorkerPool shutdown")
}

// GlobalPool 全局工作池实例。
var GlobalPool *WorkerPool

// InitWorkerPool 初始化全局工作池。n<=0 时取 CPU 核心数。
func InitWorkerPool(n int) {
	GlobalPool = NewWorkerPool(n)
}
