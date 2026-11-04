//go:build windows && (amd64 || arm64) && !dev

package main

import "github.com/egoist/mygo"

// devURL 正式构建恒返回空串: 加载嵌入页面。指向 dev server 的能力整体编译在 dev
// 构建标签之后, 正式发布的 exe 不含该代码路径, 环境变量无法把界面引向任意外部地址。
func devURL() string { return "" }

// devTools 正式构建关闭 DevTools 与 WebView2 自带的重载、右键菜单加速键。
func devTools() mygo.DevTools { return mygo.DevToolsDisabled }
