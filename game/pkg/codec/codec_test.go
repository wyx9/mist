package codec

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

// buildFrame 构造一条 [4B 长度头][data] 的 TCP 帧。
func buildFrame(data []byte) []byte {
	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame, uint32(len(data)))
	copy(frame[4:], data)
	return frame
}

func TestSendRecvRoundtrip(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte{0x00, 0x01, 0x02, 0x03}
	if err := SendData(&buf, payload); err != nil {
		t.Fatalf("send: %v", err)
	}

	r := bufio.NewReader(&buf)
	data, err := RecvFrame(r)
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("unexpected data: %v", data)
	}
}

// TestRecvFrameSticky 一次写入两帧，验证连续读出（粘包场景）。
func TestRecvFrameSticky(t *testing.T) {
	var buf bytes.Buffer
	f1 := buildFrame([]byte{0xaa})
	f2 := buildFrame([]byte{0xbb, 0xcc})
	buf.Write(append(append([]byte{}, f1...), f2...))

	r := bufio.NewReader(&buf)
	d1, err := RecvFrame(r)
	if err != nil {
		t.Fatalf("recv first: %v", err)
	}
	d2, err := RecvFrame(r)
	if err != nil {
		t.Fatalf("recv second: %v", err)
	}
	if !bytes.Equal(d1, []byte{0xaa}) || !bytes.Equal(d2, []byte{0xbb, 0xcc}) {
		t.Fatalf("unexpected frames: %v, %v", d1, d2)
	}
}

// TestRecvFrameHalfPacket 帧被拆成多段写入（半包场景），RecvFrame 应等待补齐。
func TestRecvFrameHalfPacket(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	frame := buildFrame([]byte{0x01, 0x02, 0x03})

	go func() {
		_, _ = client.Write(frame[:2])
		_, _ = client.Write(frame[2:4])
		_, _ = client.Write(frame[4:])
		_ = client.Close()
	}()

	r := bufio.NewReader(server)
	data, err := RecvFrame(r)
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	if !bytes.Equal(data, []byte{0x01, 0x02, 0x03}) {
		t.Fatalf("unexpected data: %v", data)
	}
}

func TestRecvFrameTooLarge(t *testing.T) {
	var buf bytes.Buffer
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, MaxMessageSize+1)
	buf.Write(header)

	r := bufio.NewReader(&buf)
	if _, err := RecvFrame(r); err == nil {
		t.Fatal("expected error for oversized frame")
	}
}

func TestSendDataTooLarge(t *testing.T) {
	var buf bytes.Buffer
	if err := SendData(&buf, make([]byte, MaxMessageSize+1)); err == nil {
		t.Fatal("expected error for oversized send")
	}
}
