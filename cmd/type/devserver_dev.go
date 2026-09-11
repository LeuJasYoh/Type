//go:build windows && (amd64 || arm64) && dev

package main

import "os"

// devServerURL 开发构建(-tags dev)的页面地址: TYPE_DEV_URL 环境变量优先
// (可指定任意端口), 其次 -dev 参数(默认 5173); 均未设置时退回嵌入页面
func devServerURL() string {
	if u := os.Getenv("TYPE_DEV_URL"); u != "" {
		return u
	}
	for _, arg := range os.Args[1:] {
		if arg == "-dev" {
			return "http://localhost:5173"
		}
	}
	return ""
}
