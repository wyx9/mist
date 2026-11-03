package rpc

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	"simple_game/game/api/protos/pt"
	"simple_game/game/pkg/tool"
)

// LocalCaller 本地投递能力：由 core.AManager 实现（CallLocal/SendLocal，不带远程回退）。
// category 为消息归属的 actor 类别：目标未运行且本地无记录时据此按需创建/复活。
type LocalCaller interface {
	CallLocal(to int64, category string, from string, msg interface{}, timeout time.Duration) (interface{}, error)
	SendLocal(to int64, category string, from string, msg interface{}) error
}

// Serve 处理一个到达本节点的 RpcFrame（接收侧入口）。
// 按消息名（frame.Method）反查 handler（含 category）、反序列化请求、本地投递到目标 actor；
// 目标未运行/本地无记录时用 category 按需创建（Start 内从 Redis 重载数据）；
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
		if err := local.SendLocal(frame.UID, e.category, frame.OriginActor, req); err != nil {
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
		OriginActor: tool.ToString(frame.UID),
	}
	res, err := local.CallLocal(frame.UID, e.category, frame.OriginActor, req, timeout)
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
