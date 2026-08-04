// Hand-maintained to match protoc-gen-go output (no .proto source / protoc in repo yet).
// source: errorres.proto
package pt

import (
	reflect "reflect"
	sync "sync"

	"google.golang.org/protobuf/proto"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// ErrorRes 路由层统一错误响应体：Code 为通用错误码，Msg 为失败原因。
type ErrorRes struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Code int32  `protobuf:"varint,1,opt,name=Code,proto3" json:"Code,omitempty"`
	Msg  string `protobuf:"bytes,2,opt,name=Msg,proto3" json:"Msg,omitempty"`
}

func (x *ErrorRes) Reset() {
	*x = ErrorRes{}
	if protoimpl.UnsafeEnabled {
		mi := &file_errorres_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ErrorRes) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ErrorRes) ProtoMessage() {}

func (x *ErrorRes) ProtoReflect() protoreflect.Message {
	mi := &file_errorres_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ErrorRes.ProtoReflect.Descriptor instead.
func (*ErrorRes) Descriptor() ([]byte, []int) {
	return file_errorres_proto_rawDescGZIP(), []int{0}
}

func (x *ErrorRes) GetCode() int32 {
	if x != nil {
		return x.Code
	}
	return 0
}

func (x *ErrorRes) GetMsg() string {
	if x != nil {
		return x.Msg
	}
	return ""
}

var File_errorres_proto protoreflect.FileDescriptor

// file_errorres_proto_rawDesc 在包初始化时用 descriptorpb 构造，
// 与 protoc 生成的 user.proto 等 rawDesc 保持相同（未压缩）形态。
var file_errorres_proto_rawDesc = func() []byte {
	fd := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("errorres.proto"),
		Package: proto.String("pt"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("ErrorRes"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:     proto.String("Code"),
						JsonName: proto.String("Code"),
						Number:   proto.Int32(1),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
					},
					{
						Name:     proto.String("Msg"),
						JsonName: proto.String("Msg"),
						Number:   proto.Int32(2),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
					},
				},
			},
		},
	}
	b, err := proto.Marshal(fd)
	if err != nil {
		panic(err)
	}
	return b
}()

var (
	file_errorres_proto_rawDescOnce sync.Once
	file_errorres_proto_rawDescData = file_errorres_proto_rawDesc
)

func file_errorres_proto_rawDescGZIP() []byte {
	file_errorres_proto_rawDescOnce.Do(func() {
		file_errorres_proto_rawDescData = protoimpl.X.CompressGZIP(file_errorres_proto_rawDescData)
	})
	return file_errorres_proto_rawDescData
}

var file_errorres_proto_msgTypes = make([]protoimpl.MessageInfo, 1)
var file_errorres_proto_goTypes = []interface{}{
	(*ErrorRes)(nil), // 0: pt.ErrorRes
}
var file_errorres_proto_depIdxs = []int32{}

func init() { file_errorres_proto_init() }

func file_errorres_proto_init() {
	if File_errorres_proto != nil {
		return
	}
	if protoimpl.UnsafeEnabled {
		file_errorres_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ErrorRes); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_errorres_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   1,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_errorres_proto_goTypes,
		DependencyIndexes: file_errorres_proto_depIdxs,
		MessageInfos:      file_errorres_proto_msgTypes,
	}.Build()
	File_errorres_proto = out.File
	file_errorres_proto_rawDesc = nil
	file_errorres_proto_goTypes = nil
	file_errorres_proto_depIdxs = nil
}
