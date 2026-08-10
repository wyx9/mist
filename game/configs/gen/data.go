package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// writeTableData 生成一张表的 JSON 数据文件（数组，键为原始字段名）。
func writeTableData(outDir string, t *Table) error {
	list := make([]map[string]interface{}, 0, len(t.Rows))
	for i, rec := range t.Rows {
		row := make(map[string]interface{}, len(t.Fields))
		for j, fld := range t.Fields {
			if !fld.IsServer() {
				continue
			}
			ctx := fmt.Sprintf("%s row %d column %d", t.Name, firstData+i+1, fld.Col+1)
			v, err := convertValue(rec[j], fld.GoType, ctx)
			if err != nil {
				return err
			}
			row[fld.RawName] = v
		}
		list = append(list, row)
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(outDir, "data", t.Name+".json")
	return os.WriteFile(path, raw, 0o644)
}

// convertValue 按 Go 类型把单元格字符串转成 JSON 值；空串返回零值。
func convertValue(raw, goType, ctx string) (interface{}, error) {
	if raw == "" {
		switch goType {
		case "int32":
			return int32(0), nil
		case "int64":
			return int64(0), nil
		case "float64":
			return float64(0), nil
		case "bool":
			return false, nil
		default:
			return "", nil
		}
	}
	switch goType {
	case "int32":
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid int %q", ctx, raw)
		}
		return int32(n), nil
	case "int64":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid long %q", ctx, raw)
		}
		return n, nil
	case "float64":
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid float %q", ctx, raw)
		}
		return f, nil
	case "bool":
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid bool %q", ctx, raw)
		}
		return b, nil
	default:
		return raw, nil
	}
}
