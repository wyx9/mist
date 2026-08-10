// game/configs/gen —— 配置表生成器
// 读取 configs/excels/*.xlsx，按 5 行表头约定解析，生成：
//  1. configs/*.gen.go             强类型结构体 + 运行时表（package configs）
//  2. configs/data/*.json          每张表的 JSON 数组数据（go:embed 内嵌）
//
// 可选 -push：把生成数据写入 Redis 并通过 pub/sub 通知热更。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func main() {
	root := findModuleRoot()
	if root == "" {
		fail("cannot locate game module root (go.mod not found)")
	}
	excelsDir := flag.String("excels", filepath.Join(root, "configs", "excels"), "excel 表格目录")
	outDir := flag.String("out", filepath.Join(root, "configs"), "生成产物目录（包目录）")
	push := flag.Bool("push", false, "生成后把数据推送到 Redis 并发布热更通知")
	flag.Parse()

	tables, err := parseExcels(*excelsDir)
	if err != nil {
		fail(err)
	}
	if err := generate(*outDir, tables); err != nil {
		fail(err)
	}
	if *push {
		if err := pushToRedis(root, tables); err != nil {
			fail(err)
		}
	}
	fmt.Printf("gen_configs: %d tables generated\n", len(tables))
}

func fail(v ...interface{}) {
	fmt.Fprintln(os.Stderr, append([]interface{}{"gen_configs:"}, v...)...)
	os.Exit(1)
}

// findModuleRoot 从源码文件位置向上查找 game 模块根（含 go.mod 的目录）。
func findModuleRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
