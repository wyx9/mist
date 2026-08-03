package codec

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
)

// MaxMessageSize 单帧最大长度（1MB），超过此大小的帧将被拒绝以防止 OOM 攻击。
const MaxMessageSize = 1 << 20

// SendData 以 4 字节大端长度头 + 数据的形式写入一条完整 TCP 帧。
func SendData(w io.Writer, data []byte) error {
	length := uint32(len(data))
	if length > MaxMessageSize {
		return fmt.Errorf("message too large: %d bytes (max: %d)", length, MaxMessageSize)
	}

	// 计算消息长度并将其转换成4个字节的二进制数据
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, length)

	// 发送消息头部和消息内容
	_, err := w.Write(append(header, data...))
	return err
}

// RecvFrame 从持久 bufio.Reader 读取一条完整 TCP 帧。
// ReadFull 会自然处理粘包（一次 Read 含多条帧）与半包（等待数据补齐）。
func RecvFrame(r *bufio.Reader) ([]byte, error) {
	// 先读取4个字节的消息头部，获取消息长度
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header)

	// 防止分配过大内存导致 OOM
	if length > MaxMessageSize {
		return nil, fmt.Errorf("message too large: %d bytes (max: %d)", length, MaxMessageSize)
	}

	// 根据消息长度读取对应的消息内容
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	return data, nil
}
