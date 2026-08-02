package engine

import (
	"context"
	"fmt"
	"net"
	pt2 "simple_game/game/api/protos/pt"
	"simple_game/game/pkg"

	"google.golang.org/grpc"
)

// server gRPC服务器结构体，实现了Hello服务接口
type server struct {
	pt2.UnimplementedHelloServer
}

// Say 实现Hello服务的Say方法，处理客户端的Say请求
func (s *server) Say(ctx context.Context, req *pt2.SayRequest) (*pt2.SayResponse, error) {
	fmt.Println("request:", req.Msg)
	return &pt2.SayResponse{Msg: "Hello " + req.Msg}, nil
}

// StartGRPC 启动gRPC服务器
func StartGRPC() {
	// 创建TCP监听器
	listen, err := net.Listen("tcp", ":9901")
	if err != nil {
		fmt.Printf("failed to listen: %v", err)
		return
	}

	// 创建gRPC服务器实例
	s := grpc.NewServer()
	// 注册Hello服务
	pt2.RegisterHelloServer(s, &server{})
	defer func() {
		s.Stop()
		_ = listen.Close()
	}()

	pkg.INFO("grpc server start port: ", 9901)
	// 启动gRPC服务
	err = s.Serve(listen)
	if err != nil {
		pkg.ERROR("failed to serve", err)
		return
	}
}
