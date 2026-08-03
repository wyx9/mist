// game/tunnel/tunnel.go
// TunnelMsg 握手阶段专用消息，用于 Agent↔Game 之间的握手结果转发。
// 业务消息已改为二进制帧（见 pkg/protocol），不再经过 JSON。
package codec

import (
	"encoding/json"
)

// TunnelMsg 握手阶段带路由标识的消息结构。
type TunnelMsg struct {
	Name     string `json:"name"`
	Data     []byte `json:"data"`
	TunnelID string `json:"tunnel_id"`
}

// PackTunnelRaw 将握手结果和 tunnelID 打包为 TunnelMsg JSON。
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
