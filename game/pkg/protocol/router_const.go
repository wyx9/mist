package protocol

// 业务命令号，0 保留。
// 请求命令号与响应命令号成对定义：响应 = 请求 + 1。
const (
	_ uint16 = iota // 0 保留
	CmdLogin
	CmdLoginRsp

	CmdHeart
	CmdHeartRsp // 4

	CmdSendChat
	CmdSendChatRsp
)

// CmdError 系统保留错误响应命令号：路由层统一出错时下发，
// body 为 pt.ErrorRes；不参与业务注册与 ReqRsp 绑定。
const (
	CmdError uint16 = 0xFFFF
)

// 通用错误码，配合 pt.ErrorRes.Code 使用。
const (
	CodeOK          int32 = 0 // 成功
	CodeServerError int32 = 1 // 服务端处理出错（handler error / panic）
	CodeBadRequest  int32 = 2 // 请求体解析失败
)

// ReqRsp 请求→响应命令号绑定表（只读，勿修改）。
// 不符合“响应 = 请求 + 1”默认规则的协议在此显式登记。
var ReqRsp = map[uint16]uint16{
	CmdLogin:    CmdLoginRsp,
	CmdHeart:    CmdHeartRsp,
	CmdSendChat: CmdSendChatRsp,
}

// RspCmd 返回请求对应的响应命令号；未绑定时默认 = 请求命令号 + 1。
func RspCmd(reqCmd uint16) uint16 {
	if rsp, ok := ReqRsp[reqCmd]; ok {
		return rsp
	}
	return reqCmd + 1
}
