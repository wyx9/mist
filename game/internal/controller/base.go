package controller

type BaseController struct {
}

func (this BaseController) Ctx(ctx interface{}) interface{} {
	return ctx
}
