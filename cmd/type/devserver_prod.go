//go:build windows && (amd64 || arm64) && !dev

package main

// devServerURL 正式构建恒返回空串: 加载嵌入页面。
// 指向 dev server 的能力整体编译在 dev 构建标签之后(见 devserver_dev.go),
// 正式发布的 exe 不含该代码路径, 环境变量无法把界面引向任意外部地址
func devServerURL() string { return "" }

// devMode 正式构建恒为 false。它打开的是 WebView2 自身的调试能力:
// DevTools 与右键菜单(库把该值直接传给这两项设置)。发布版两者都不该有 ——
// 留着 DevTools 没有意义, 而右键菜单里的"重新加载"会让前端复位、
// 与仍在跑的后端任务脱钩
func devMode() bool { return false }
