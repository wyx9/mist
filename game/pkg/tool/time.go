package tool

import "time"

// 常用时间布局常量，避免到处写 Go 布局魔数。
const (
	// LayoutDateTime 日期时间格式：2006-01-02 15:04:05。
	LayoutDateTime = "2006-01-02 15:04:05"
	// LayoutDate 日期格式：2006-01-02。
	LayoutDate = "2006-01-02"
)

// Now 返回当前 Unix 秒时间戳。
func Now() int64 { return time.Now().Unix() }

// NowMs 返回当前 Unix 毫秒时间戳。
func NowMs() int64 { return time.Now().UnixMilli() }

// Format 将秒时间戳按布局格式化为字符串。
func Format(ts int64, layout string) string {
	return time.Unix(ts, 0).Format(layout)
}

// Parse 将字符串按布局解析为本地时区的秒时间戳，解析失败返回 0。
func Parse(layout, value string) int64 {
	t, err := time.ParseInLocation(layout, value, time.Local)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// DayStart 返回时间戳所在自然日（本地时区）的零点秒时间戳。
func DayStart(ts int64) int64 {
	t := time.Unix(ts, 0)
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location()).Unix()
}

// RemainSeconds 返回距目标时间戳的剩余秒数，目标已过时返回 0。
func RemainSeconds(ts int64) int64 {
	if remain := ts - Now(); remain > 0 {
		return remain
	}
	return 0
}
