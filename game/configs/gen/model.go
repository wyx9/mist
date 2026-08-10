package main

import "strings"

// Field 一列字段定义。
type Field struct {
	Col     int    // Excel 列下标（0 起）
	RawName string // 原始字段名（第 2 行，如 q_id）
	GoName  string // Go 字段名（如 ID）
	GoType  string // Go 类型（如 int32）
	Marker  string // 字段标记（第 4 行）
	Comment string // 字段说明（第 5 行）
}

// IsServer 该列是否标记包含 server（大小写不敏感）。
func (f *Field) IsServer() bool {
	return strings.Contains(strings.ToLower(f.Marker), "server")
}

// Table 一张 sheet（一张配置表）的完整定义。
type Table struct {
	Name       string     // sheet 名，即表名（如 q_shop_goods）
	Comment    string     // 表注释（第 1 行）
	StructName string     // 生成的结构体名（如 ShopGoods）
	Fields     []Field    // 全部列（含非 server 列）
	Rows       [][]string // 数据行，与 Fields 对齐
}

// KeyField 主键列：第一列（解析时已强制校验）。
func (t *Table) KeyField() Field {
	return t.Fields[0]
}

// KeyGoType 主键 Go 类型（int32/int64）。
func (t *Table) KeyGoType() string {
	return t.KeyField().GoType
}

// ServerFields 只返回标记含 server 的列。
func (t *Table) ServerFields() []Field {
	var out []Field
	for _, f := range t.Fields {
		if f.IsServer() {
			out = append(out, f)
		}
	}
	return out
}
