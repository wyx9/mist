package protocol

// 业务命令号，0 保留。
// 请求命令号与响应命令号成对定义：响应 = 请求 + 1。
const (
	CmdLogin    uint16 = 1
	CmdLoginRsp        = CmdLogin + 1 // 2
	CmdHeart    uint16 = 3
	CmdHeartRsp        = CmdHeart + 1 // 4
)

// ReqRsp 请求→响应命令号绑定表（只读，勿修改）。
// 不符合“响应 = 请求 + 1”默认规则的协议在此显式登记。
var ReqRsp = map[uint16]uint16{
	CmdLogin: CmdLoginRsp,
	CmdHeart: CmdHeartRsp,
}

// RspCmd 返回请求对应的响应命令号；未绑定时默认 = 请求命令号 + 1。
func RspCmd(reqCmd uint16) uint16 {
	if rsp, ok := ReqRsp[reqCmd]; ok {
		return rsp
	}
	return reqCmd + 1
}
