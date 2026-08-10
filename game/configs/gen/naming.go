package main

import "strings"

// stripTablePrefix 去掉常见的表/字段前缀 q_，得到干净的命名源串。
func stripTablePrefix(s string) string {
	if strings.HasPrefix(s, "q_") {
		return s[2:]
	}
	return s
}

// acronyms 常见缩略词整词大写。
var acronyms = map[string]string{
	"id":  "ID",
	"uid": "UID",
	"ip":  "IP",
	"url": "URL",
}

// toCamel 把 snake_case 转大驼峰：按非字母数字拆分，每段首字母大写，缩略词整词大写。
func toCamel(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	})
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		low := strings.ToLower(p)
		if a, ok := acronyms[low]; ok {
			b.WriteString(a)
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return b.String()
}

// structName 表名 → 结构体名：去 q_ 前缀后转大驼峰。
func structName(sheet string) string {
	return toCamel(stripTablePrefix(sheet))
}

// fieldName 字段名 → Go 字段名：去 q_ 前缀后转大驼峰。
func fieldName(raw string) string {
	return toCamel(stripTablePrefix(raw))
}

// unexport 把大驼峰转小驼峰，用于内部类型名。
func unexport(name string) string {
	if name == "" {
		return name
	}
	return strings.ToLower(name[:1]) + name[1:]
}
