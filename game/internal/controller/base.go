package controller

import (
	engine "simple_game/game/engine"
)

type BaseController struct {
}

func (this BaseController) Ctx(ctx interface{}) *engine.Player {
	return ctx.(*engine.Player)
}
