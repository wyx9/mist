package core

import "time"

// ActorConfig Actor配置结构体，用于配置Actor的运行参数
type ActorConfig struct {
	QueueSize      int           // 消息队列大小，决定可以缓存的消息数量
	SendTimeout    time.Duration // 发送超时时间，消息发送的最大等待时间
	HandlerTimeout time.Duration // 处理超时时间，消息处理的最大执行时间
}

// DefaultConfig 默认配置，当没有提供自定义配置时使用
var DefaultConfig = ActorConfig{
	QueueSize:      1024,             // 默认队列大小1024
	SendTimeout:    5 * time.Second,  // 默认发送超时5秒
	HandlerTimeout: 30 * time.Second, // 默认处理超时30秒（超时视为 actor 故障）
}
