package codec

import (
	"encoding/binary"
	"errors"
)

// gateway <-> game 长连接内部隧道帧：在现有 4B 长度分帧之上承载多路复用隧道。
// 格式：[1B type][2B tunnelID len][tunnelID][payload]
const (
	TunnelHandshake    byte = 1 // gateway->game 建隧道，payload 为 JSON 身份 {player_name,uid,version}
	TunnelHandshakeAck byte = 2 // game->gateway 握手回执，payload 为 JSON {ok,error?}
	TunnelData         byte = 3 // 双向业务数据，payload 为业务帧字节
	TunnelKick         byte = 4 // 任一侧通知关闭某隧道
	TunnelPing         byte = 5 // gateway<->game 保活
	TunnelPong         byte = 6
	TunnelRPC          byte = 7 // game<->game 内部 RPC 信封（经 gateway 路由，payload 为 pt.RpcFrame）
)

// PackTunnelFrame 打包隧道帧。
func PackTunnelFrame(typ byte, tunnelID string, data []byte) []byte {
	tid := []byte(tunnelID)
	out := make([]byte, 3+len(tid)+len(data))
	out[0] = typ
	binary.BigEndian.PutUint16(out[1:3], uint16(len(tid)))
	copy(out[3:], tid)
	copy(out[3+len(tid):], data)
	return out
}

// UnpackTunnelFrame 解析隧道帧。
func UnpackTunnelFrame(frame []byte) (typ byte, tunnelID string, data []byte, err error) {
	if len(frame) < 3 {
		return 0, "", nil, errors.New("tunnel frame too short")
	}
	typ = frame[0]
	tidLen := int(binary.BigEndian.Uint16(frame[1:3]))
	if 3+tidLen > len(frame) {
		return 0, "", nil, errors.New("tunnel frame truncated")
	}
	return typ, string(frame[3 : 3+tidLen]), frame[3+tidLen:], nil
}
