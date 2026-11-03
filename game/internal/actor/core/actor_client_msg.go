package core

import (
	"simple_game/game/internal/route"
	"simple_game/game/pkg/logger"
	"simple_game/game/pkg/protocol"
)

// HandleClientMsg 客户端消息的通用处理入口：
// 将消息信封与当前 actor 组装为类型化 HandlerContext 交给路由分发，
// 成功且有响应时通过 actor 的 WriteMessage 能力回包；没有该能力的 actor
// 仅记录日志并丢弃响应。
//
// 新增 actor 无需实现 Handler：通过 AManager 工厂/EnsureRunning 启动后，
// 客户端消息会自动走本入口（ActorBase.clientHandler 委托到本函数）。
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
