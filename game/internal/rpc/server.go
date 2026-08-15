package rpc

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
)

// LocalCaller 本地投递能力：由 core.AManager 实现（CallLocal/SendLocal，不带远程回退）。
type LocalCaller interface {
	CallLocal(to, from string, msg interface{}, timeout time.Duration) (interface{}, error)
	SendLocal(to, from string, msg interface{}) error
}

// Serve 处理一个到达本节点的 RpcFrame（接收侧入口，须在统一 worker 内调用）。
// 按 method 反查 handler、反序列化请求、本地投递到目标 actor；
// IsCall=true 时通过 reply 回写 RPC_RESPONSE。
func Serve(local LocalCaller, frame *pt.RpcFrame, reply func(*pt.RpcFrame) error) error {
	e, err := lookup(frame.Method)
	if err != nil {
		return err
	}
	req := e.newRequest()
	if err := proto.Unmarshal(frame.Payload, req); err != nil {
		return fmt.Errorf("rpc: unmarshal request: %w", err)
	}

	if !frame.IsCall {
		if err := local.SendLocal(frame.Target, frame.OriginActor, req); err != nil {
			return fmt.Errorf("rpc: send local: %w", err)
		}
		return nil
	}

	timeout := time.Duration(frame.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	resp := &pt.RpcFrame{
		Kind:        pt.RpcKind_RPC_RESPONSE,
		ReqId:       frame.ReqId,
		OriginNode:  frame.OriginNode,
		OriginActor: frame.Target,
	}
	res, err := local.CallLocal(frame.Target, frame.OriginActor, req, timeout)
	if err != nil {
		resp.Error = err.Error()
	} else if res != nil {
		pm, ok := res.(proto.Message)
		if !ok {
			resp.Error = fmt.Sprintf("rpc: reply type %T is not proto.Message", res)
		} else if data, merr := proto.Marshal(pm); merr != nil {
			resp.Error = merr.Error()
		} else {
			resp.Payload = data
		}
	}
	return reply(resp)
}
