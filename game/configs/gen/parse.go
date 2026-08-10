package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"
)

// 表头行约定：1 注释 / 2 字段名 / 3 类型 / 4 标记 / 5 字段说明，数据从第 6 行开始。
const (
	rowComment = 0
	rowName    = 1
	rowType    = 2
	rowMarker  = 3
	rowDesc    = 4
	firstData  = 5
)

// parseExcels 解析目录下全部 xlsx（跳过 Excel 临时锁文件 ~$），返回所有 sheet 的表定义。
func parseExcels(dir string) ([]*Table, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.xlsx"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var all []*Table
	for _, file := range files {
		if strings.HasPrefix(filepath.Base(file), "~$") {
			continue
		}
		tables, err := parseFile(file)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(file), err)
		}
		all = append(all, tables...)
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("no valid xlsx sheets found in %s", dir)
	}
	return all, nil
}

func parseFile(path string) ([]*Table, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var tables []*Table
	for _, sheet := range f.GetSheetList() {
		t, err := parseSheet(f, sheet)
		if err != nil {
			return nil, fmt.Errorf("sheet %s: %w", sheet, err)
		}
		if t != nil {
			tables = append(tables, t)
		}
	}
	return tables, nil
}

func parseSheet(f *excelize.File, sheet string) (*Table, error) {
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if len(rows) < firstData {
		return nil, fmt.Errorf("header incomplete: need %d rows, got %d", firstData, len(rows))
	}

	t := &Table{Name: sheet, StructName: structName(sheet)}
	if len(rows[rowComment]) > 0 {
		t.Comment = strings.TrimSpace(rows[rowComment][0])
	}
	maxCols := 0
	for _, r := range rows[:firstData] {
		if len(r) > maxCols {
			maxCols = len(r)
		}
	}

	seen := make(map[string]int)
	for col := 0; col < maxCols; col++ {
		rawName := strings.TrimSpace(cell(rows, rowName, col))
		if rawName == "" {
			continue
		}
		fld := Field{
			Col:     col,
			RawName: rawName,
			Marker:  strings.TrimSpace(cell(rows, rowMarker, col)),
			Comment: strings.TrimSpace(cell(rows, rowDesc, col)),
		}
		fld.GoName = fieldName(rawName)
		if fld.GoName == "" {
			return nil, fmt.Errorf("column %d: field %q maps to empty go name", col+1, rawName)
		}
		if prev, dup := seen[fld.GoName]; dup {
			return nil, fmt.Errorf("column %d: field %q duplicates go name of column %d", col+1, rawName, prev+1)
		}
		seen[fld.GoName] = col
		if fld.IsServer() {
			gt, ok := goType(cell(rows, rowType, col))
			if !ok {
				return nil, fmt.Errorf("column %d %q: unsupported type %q", col+1, rawName, cell(rows, rowType, col))
			}
			fld.GoType = gt
		}
		t.Fields = append(t.Fields, fld)
	}

	if len(t.Fields) == 0 {
		return nil, fmt.Errorf("no fields defined")
	}
	key := t.Fields[0]
	if key.Col != 0 || !key.IsServer() {
		return nil, fmt.Errorf("first column %q must be the server key field", key.RawName)
	}
	if key.GoType != "int32" && key.GoType != "int64" {
		return nil, fmt.Errorf("first column key type must be int/long, got %q", key.GoType)
	}

	// 数据行：第 6 行起，跳过全空行
	for i := firstData; i < len(rows); i++ {
		rec := make([]string, len(t.Fields))
		empty := true
		for j, fld := range t.Fields {
			v := strings.TrimSpace(cell(rows, i, fld.Col))
			rec[j] = v
			if v != "" {
				empty = false
			}
		}
		if empty {
			continue
		}
		t.Rows = append(t.Rows, rec)
	}

	// 主键去重
	seenID := make(map[string]int)
	for idx, rec := range t.Rows {
		id := rec[0]
		if prev, dup := seenID[id]; dup {
			return nil, fmt.Errorf("row %d: duplicate key %q (first at row %d)", firstData+idx+1, id, firstData+prev+1)
		}
		seenID[id] = idx
	}
	return t, nil
}

func cell(rows [][]string, r, c int) string {
	if r >= len(rows) || c >= len(rows[r]) {
		return ""
	}
	return rows[r][c]
}
