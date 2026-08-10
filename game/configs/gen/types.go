package main

import "strings"

// typeMap Excel 类型 → Go 类型。
var typeMap = map[string]string{
	"int":     "int32",
	"int32":   "int32",
	"long":    "int64",
	"int64":   "int64",
	"float":   "float64",
	"float64": "float64",
	"string":  "string",
	"bool":    "bool",
}

// goType 归一化 Excel 类型名。
func goType(raw string) (string, bool) {
	t, ok := typeMap[strings.ToLower(strings.TrimSpace(raw))]
	return t, ok
}
