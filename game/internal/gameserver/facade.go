// internal/gameserver/facade.go
// Actor 与消息门面：对 Actor 管理器的薄转发。
package gameserver

import (
	"time"

	core "simple_game/game/internal/actor/core"
	"simple_game/game/pkg/protocol"
)

// Deliver 投递客户端消息到 Actor（目标为 uid）。
func (s *Server) Deliver(uid int64, msg *protocol.Msg) error {
	return s.ActorManner.Deliver(uid, msg)
}

// Cast 向 Actor 异步投递内部消息（目标为 uid）。
func (s *Server) Cast(uid int64, from string, msg interface{}) error {
	return s.ActorManner.Cast(uid, from, msg)
}

// Call 同步调用 Actor（内部 RPC，目标为 uid）。
func (s *Server) Call(uid int64, from string, msg interface{}, timeout time.Duration) (interface{}, error) {
	return s.ActorManner.Call(uid, from, msg, timeout)
}

// RegisterActor 注册 Actor 到管理器。
func (s *Server) RegisterActor(a *core.ActorBase) error {
	return s.ActorManner.Add(a)
}
