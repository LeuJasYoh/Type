//go:build windows && (amd64 || arm64)

// 仅 64 位: INPUT 结构体的手工填充(40 字节)仅匹配 64 位 ABI, 386 下
// SendInput 会静默注入乱码(见 internal/win32/win32_keyboard.go),
// 因此直接禁止 32 位编译
package main

import (
	"sync/atomic"

	"github.com/LeuJasYoh/type/internal/typing"
	"github.com/LeuJasYoh/type/internal/web"
	"github.com/LeuJasYoh/type/internal/win32"

	"github.com/jchv/go-webview2"
)

var version = "1.5.6"

var topmostFlag atomic.Bool // 窗口置顶开关(与输入任务无关, 归装配层)

// ─── 主程序 ───────────────────────────────────────────

func main() {
	// 单实例: 多开会让两个实例争抢剪贴板与键盘焦点, 先挡在门口
	if !win32.GuardSingleInstance() {
		return
	}

	// 界面由 WebView2 渲染: 运行时缺失时连窗口都建不出来, 而 GUI 子系统没有
	// 控制台, 库里的报错没人看得见。先预检再创建, 缺什么就说什么
	// (见 internal/win32/win32_webview2.go), 不让用户面对一片死寂
	if !win32.WebView2Available() {
		win32.PromptWebView2Unusable(win32.MsgWebView2Missing) // 提示后退出, 不返回
	}

	// Debug 直接决定库的两项设置: 默认右键菜单与 DevTools 是否可用。
	// 正式构建必须两者皆关 —— 发布版留 DevTools 没有意义, 而右键菜单里的
	// "重新加载"会让前端复位、与仍在跑的后端任务脱钩
	w := webview2.NewWithOptions(webview2.WebViewOptions{Debug: devMode()})
	// 判空必须在 defer 之前: New 同步失败时返回的是 nil 接口, 而 defer 语句
	// 求值 receiver 的那一刻就会 panic(已实测: 栈顶正落在那条 defer 上)
	if w == nil {
		win32.PromptWebView2Unusable(win32.MsgWebView2InitFailed) // 提示后退出, 不返回
	}
	defer w.Destroy()
	w.SetTitle("Type " + version)

	// 先取窗口句柄: 尺寸要按该窗口所在显示器的 DPI 换算成物理像素
	// (manifest 声明了 PerMonitorV2, 见 internal/win32 的 ScaledForDPI)
	hw := uintptr(w.Window())
	cw, ch := win32.ScaledForDPI(hw, 540, 450)
	w.SetSize(cw, ch, webview2.HintFixed)

	// 设置窗口图标（首次 + 延迟重试）
	win32.RetrySetIcon(hw)

	// 业务服务: 平台能力以接口注入, Win32 实现见 internal/win32;
	// 前台探测带上自身窗口句柄, 用于目标窗口的"非自身"判定
	svc := typing.NewTypingService(win32.Injector{}, win32.Clipboard{}, win32.Foreground{HWND: hw})

	// 绑定 Go 函数到 JS
	// (须在加载页面前完成: 绑定的注入脚本对随后创建的文档生效)
	w.Bind("startTyping", svc.Start)
	w.Bind("cancelTyping", svc.Cancel)

	w.Bind("toggleTopmost", func() (bool, error) {
		on := !topmostFlag.Load()
		topmostFlag.Store(on)
		if hw != 0 {
			win32.SetTopmost(hw, on)
		}
		return on, nil
	})

	// 前端轮询读取当前输入状态
	w.Bind("getTypingStatus", svc.Status)

	// 加载界面: 开发构建(-tags dev)指向 Vite dev server 支持 HMR,
	// 正式构建不含这段代码, 恒加载嵌入的自包含页面
	if url := devServerURL(); url != "" {
		w.Navigate(url)
	} else {
		w.SetHtml(web.IndexHTML)
	}

	w.Run()
}
