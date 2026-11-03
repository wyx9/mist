package protocol

// 通用错误码，配合 pt.ErrorRes.Code 使用。
// 业务路由已改为 Protobuf 消息名（反射）驱动，不再维护命令号枚举与请求/响应配对表。
const (
	CodeOK          int32 = 0 // 成功
	CodeServerError int32 = 1 // 服务端处理出错（handler error / panic）
	CodeBadRequest  int32 = 2 // 请求体解析失败
)
