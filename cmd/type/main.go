//go:build windows && (amd64 || arm64)

// INPUT 结构体的手工填充(40 字节)仅匹配 64 位 ABI; 386 下真实布局为 28 字节,
// SendInput 会静默注入乱码, 因此直接禁止 32 位编译
package main

import (
	"os"
	"sync/atomic"

	"Type/internal/web"

	"github.com/webview/webview_go"
)

var version = "1.3.4"

var topmostFlag atomic.Bool // 窗口置顶开关(与输入任务无关, 归装配层)

// ─── 主程序 ───────────────────────────────────────────

func main() {
	w := webview.New(true)
	defer w.Destroy()
	w.SetTitle("Type " + version)
	w.SetSize(540, 450, webview.HintFixed)

	// 设置窗口图标（首次 + 延迟重试）
	hw := uintptr(w.Window())
	retrySetIcon(hw)

	// 业务服务: 平台能力以接口注入, Win32 实现见 win32_*.go
	svc := newTypingService(win32Injector{}, win32Clipboard{}, win32Foreground{})

	// 绑定 Go 函数到 JS
	// (须在加载页面前完成: 绑定的注入脚本对随后创建的文档生效)
	w.Bind("startTyping", svc.Start)
	w.Bind("cancelTyping", svc.Cancel)

	w.Bind("toggleTopmost", func() (bool, error) {
		on := !topmostFlag.Load()
		topmostFlag.Store(on)
		if hw != 0 {
			setTopmost(hw, on)
		}
		return on, nil
	})

	// 前端轮询读取当前输入状态
	w.Bind("getTypingStatus", svc.Status)

	// 加载界面: 开发模式指向 Vite dev server (支持 HMR, 需先 npm run dev),
	// 默认加载嵌入的自包含页面
	if url := devServerURL(); url != "" {
		w.Navigate(url)
	} else {
		w.SetHtml(web.IndexHTML)
	}

	w.Run()
}

// devServerURL 开发模式页面地址: TYPE_DEV_URL 环境变量优先 (可指定任意端口),
// 其次 -dev 参数 (默认 5173); 均未设置时返回空串, 即生产模式
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
