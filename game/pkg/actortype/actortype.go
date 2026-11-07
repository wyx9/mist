// Package actortype 定义 Actor 类型标识（如 "player"、"room"）。
// 放在 pkg 层供 core（框架）、rpc（注册/路由）与业务 actor 层共享：
// core 依赖 rpc，若类型定义在 core 会形成 import 环。
package actortype

// ActorType Actor 类型标识，取值由业务层定义（如 "player"、"room"）。
type ActorType string
