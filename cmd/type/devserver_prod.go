//go:build windows && (amd64 || arm64) && !dev

package main

// devServerURL 正式构建恒返回空串: 加载嵌入页面。指向 dev server 的能力整体编译在 dev
// 构建标签之后, 正式发布的 exe 不含该代码路径, 环境变量无法把界面引向任意外部地址。
// 见 docs/architecture.md「布局与单一来源」
func devServerURL() string { return "" }

// devMode 正式构建恒为 false: 发布版不开 WebView2 的 DevTools 与右键菜单。
// 见 docs/architecture.md「布局与单一来源」
func devMode() bool { return false }
