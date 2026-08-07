// Package tool 提供通用的容错转换、时间操作与安全并发工具。
package tool

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ToInt64 将任意值安全转换为 int64，失败或溢出时返回默认值 def。
// 支持全部整数/浮点类型、string 与 []byte；string/[]byte 会先去除首尾空白再解析。
func ToInt64(v any, def int64) int64 {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int8:
		return int64(t)
	case int16:
		return int64(t)
	case int32:
		return int64(t)
	case int64:
		return t
	case uint:
		return int64(t)
	case uint8:
		return int64(t)
	case uint16:
		return int64(t)
	case uint32:
		return int64(t)
	case uint64:
		if t > math.MaxInt64 {
			return def
		}
		return int64(t)
	case uintptr:
		if uint64(t) > math.MaxInt64 {
			return def
		}
		return int64(t)
	case float32:
		return floatToInt64(float64(t), def)
	case float64:
		return floatToInt64(t, def)
	case string:
		return parseSigned(t, def)
	case []byte:
		return parseSigned(string(t), def)
	default:
		return def
	}
}

// ToInt 将任意值安全转换为 int，失败、溢出或超出 int 范围时返回默认值 def。
func ToInt(v any, def int) int {
	if i := ToInt64(v, int64(def)); int64(int(i)) == i {
		return int(i)
	}
	return def
}

// ToFloat64 将任意值安全转换为 float64，失败时返回默认值 def。
func ToFloat64(v any, def float64) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int8:
		return float64(t)
	case int16:
		return float64(t)
	case int32:
		return float64(t)
	case int64:
		return float64(t)
	case uint:
		return float64(t)
	case uint8:
		return float64(t)
	case uint16:
		return float64(t)
	case uint32:
		return float64(t)
	case uint64:
		return float64(t)
	case uintptr:
		return float64(t)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return def
		}
		return f
	case []byte:
		return ToFloat64(string(t), def)
	default:
		return def
	}
}

// ToString 将任意值转换为字符串：nil 返回空串，数字用 strconv 精确格式化，其余走 fmt.Sprint。
func ToString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int8:
		return strconv.FormatInt(int64(t), 10)
	case int16:
		return strconv.FormatInt(int64(t), 10)
	case int32:
		return strconv.FormatInt(int64(t), 10)
	case int64:
		return strconv.FormatInt(t, 10)
	case uint:
		return strconv.FormatUint(uint64(t), 10)
	case uint8:
		return strconv.FormatUint(uint64(t), 10)
	case uint16:
		return strconv.FormatUint(uint64(t), 10)
	case uint32:
		return strconv.FormatUint(uint64(t), 10)
	case uint64:
		return strconv.FormatUint(t, 10)
	case uintptr:
		return strconv.FormatUint(uint64(t), 10)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

// floatToInt64 将浮点数转换为 int64，超出范围时返回默认值。
func floatToInt64(f float64, def int64) int64 {
	if f >= float64(math.MaxInt64) || f < float64(math.MinInt64) {
		return def
	}
	return int64(f)
}

// parseSigned 将字符串按十进制解析为 int64，失败时返回默认值。
func parseSigned(s string, def int64) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return def
	}
	return n
}
