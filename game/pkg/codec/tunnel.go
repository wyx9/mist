// game/tunnel/tunnel.go
// TunnelMsg 在 PacketMsg 外层包裹路由标识，用于 Agent↔Game 之间的消息转发。
package tunnel

import (
	"encoding/json"
	"simple_game/game/libs"
)

// TunnelMsg 带路由标识的消息结构。
// 比 PacketMsg 多一个 TunnelID，Agent 通过该字段做 client↔game 双向映射。
type TunnelMsg struct {
	Name     string `json:"name"`
	Data     []byte `json:"data"`
	TunnelID string `json:"tunnel_id"`
}

// PackTunnel 将 PacketMsg 和 tunnelID 打包为 JSON 字节。
// 如果 msg 已经是 *libs.PacketMsg，直接内联字段；否则 Name 设为空。
func PackTunnel(msg *libs.PacketMsg, tunnelID string) []byte {
	tm := &TunnelMsg{
		Name:     msg.Name,
		Data:     msg.Data,
		TunnelID: tunnelID,
	}
	b, _ := json.Marshal(tm)
	return b
}

// PackTunnelRaw 将原始消息字节和 tunnelID 直接打包为 TunnelMsg JSON。
// name 用于设置消息类型标识，data 是原始 protobuf 二进制。
func PackTunnelRaw(name string, data []byte, tunnelID string) []byte {
	tm := &TunnelMsg{
		Name:     name,
		Data:     data,
		TunnelID: tunnelID,
	}
	b, _ := json.Marshal(tm)
	return b
}

// UnpackTunnel 从 JSON 字节解析出 TunnelMsg。
func UnpackTunnel(data []byte) *TunnelMsg {
	tm := &TunnelMsg{}
	if err := json.Unmarshal(data, tm); err != nil {
		return nil
	}
	return tm
}
