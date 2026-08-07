package iface

type ChatModelIFace interface {
	SendMessage(uid int64, name, msg string) error
}
