package core

import (
	"simple_game/game/internal/route"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/protocol"
)

// HandleClientMsg 客户端消息入口：组装 HandlerContext 交给 route 分发，有响应则回包。
// 所有 actor 经工厂/EnsureRunning 启动后自动走这里，无需额外实现。
func HandleClientMsg[A any](actor A, actx *ActorContext, msg interface{}) {
	m, ok := msg.(*protocol.Msg)
	if !ok {
		return
	}
	rsp, err := route.Route(&route.HandlerContext[A]{
		Actor: actor,
		Msg:   m,
		Comm:  actx,
	}, m)
	if err != nil {
		logger.ERROR("route error:", m.Name, err)
		return
	}
	logger.DEBUG("req msg :", m.Name, "msg id:", m.MsgID, "uid:", m.UID)
	if rsp == nil {
		return
	}
	out, err := protocol.EncodeMsg(rsp)
	if err != nil {
		logger.ERROR("encode response error:", err)
		return
	}
	if w, ok := any(actor).(route.ClientWriter); ok {
		if err := w.WriteMessage(out); err != nil {
			logger.ERROR("write response error:", err, "msg:", m.Name)
		}
		return
	}
	logger.WARNING("actor has no WriteMessage, response dropped, msg:", m.Name)
}
