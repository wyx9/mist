package codec

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
)

// MaxMessageSize 单帧最大长度（1MB），超过此大小的帧将被拒绝以防止 OOM 攻击。
const MaxMessageSize = 1 << 20

// SendData 写入一条完整 TCP 帧：4 字节大端长度头 + 数据。
func SendData(w io.Writer, data []byte) error {
	length := uint32(len(data))
	if length > MaxMessageSize {
		return fmt.Errorf("message too large: %d bytes (max: %d)", length, MaxMessageSize)
	}

	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, length)

	_, err := w.Write(append(header, data...))
	return err
}

// RecvFrame 从 bufio.Reader 读取一条完整帧；ReadFull 天然处理粘包/半包。
func RecvFrame(r *bufio.Reader) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header)

	if length > MaxMessageSize {
		return nil, fmt.Errorf("message too large: %d bytes (max: %d)", length, MaxMessageSize)
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	return data, nil
}
