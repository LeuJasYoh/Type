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

var version = "1.6.0"

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
	// "重新加载"会让前端复位、与仍在跑的后端任务脱钩。
	// 建窗失败有两条来路(同步返回 nil 与库内部 panic), 都收在 createWebView2 里
	w := createWebView2()
	// 判空必须在 defer 之前: 失败时 New 返回的是 nil 接口, 而 defer 语句
	// 求值 receiver 的那一刻就会 panic(已实测: 栈顶正落在那条 defer 上)
	if w == nil {
		win32.PromptWebView2Unusable(win32.MsgWebView2InitFailed) // 提示后退出, 不返回
	}
	defer w.Destroy()
	w.SetTitle("Type " + version)

	// 先取窗口句柄: 尺寸要按该窗口所在显示器的 DPI 与工作区换算
	// (manifest 声明了 PerMonitorV2, 见 internal/win32 的 ScaledForDPI)
	hw := uintptr(w.Window())

	// 尺寸与位置只在启动时算一次, 之后固定:
	//   ① 按窗口所在显示器的工作区定尺寸(内置 540×480 ~ 720×600 的上下限,
	//      见 win32.InitialWindowSize), 4K 上不会缩成一张邮票;
	//   ② 位置在该工作区内居中。
	// 刻意不做的两件事, 别顺手加回来:
	//   - 不响应 WM_DPICHANGED(把窗口拖到缩放比例不同的显示器上重新适配)。
	//     用户明确不要这个; 而且窗口侧改尺寸与内容侧 Chromium 改缩放若不同步,
	//     就会变成"渲染缩放 ≠ 显示器缩放", 那才是真正的位图拉伸发虚;
	//   - 不放宽窗口样式。HintFixed 会去掉 WS_THICKFRAME|WS_MAXIMIZEBOX,
	//     窗口尺寸固定、右下角拖不动(internal/win32 的 TestWindowSizeIsFixed
	//     读回样式位与命中测试钉着这条)。HintFixed 省不掉, 那是"不可缩放"的
	//     唯一来源 —— 只调 SetWindowClientRect 的话窗口仍是可拖大的
	if cw, ch, wx, wy, ok := win32.WindowSizeForDisplay(hw); ok {
		// 尺寸与位置一次到位(AdjustWindowRect 反推窗口矩形), 之后 SetSize
		// 会用同一个尺寸再摆一次位置不动: 实测客户区仍等于请求值
		win32.SetWindowClientRect(hw, wx, wy, cw, ch)
		w.SetSize(cw, ch, webview2.HintFixed)
	} else {
		// 取不到显示器信息: 退回默认尺寸, 位置交给系统
		cw, ch := win32.ScaledForDPI(hw, win32.MinWindowW, win32.MinWindowH)
		w.SetSize(cw, ch, webview2.HintFixed)
	}

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

	// 读回当前置顶状态。窗口的置顶不随页面重载复位, 而按钮每次都从"未置顶"起:
	// 不读回来的话, 重载后(Ctrl+R、渲染进程崩溃后自动重载)按钮显示"置顶"而窗口
	// 仍钉在最上层, 用户点一下反而是把置顶取消, 按钮与窗口正好相反
	w.Bind("getTopmost", func() (bool, error) { return topmostFlag.Load(), nil })

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

// createWebView2 建 WebView2; 建不出来时返回 nil, 由调用方给出"界面起不来"的提示。
//
// 除了 New 同步返回 nil 这条明路, 还有一条暗路: 控制器是在
// CreateCoreWebView2Controller 的回调里异步创建的, 失败时 go-webview2 先用
// int64(res) < 0 判 HRESULT —— 而 HRESULT 错误码是负的 32 位值, 零扩展进 uintptr
// 之后 int64() 反而是正数, 这个判断永不成立, 该打印的
// "Creating controller failed with %08x" 从不出现, 它接着对 nil 控制器解引用,
// panic 从 NewWithOptions 里冒出来(帧都在库内, 但整条链都在 main 这个 goroutine 上,
// 所以这里能接住)。不接住的话用户看到的是"双击之后窗口一闪就没了" —— GUI 子系统
// 没有控制台, panic 文本一个字都到不了他眼前, 而这正是两条启动提示要避免的情形。
// 实测触发条件: 宿主进程完整性级别偏低时(例如 exe 所在目录被沙箱类工具打上
// Low 完整性标签)WebView2 拒绝创建控制器。
func createWebView2() (w webview2.WebView) {
	defer func() {
		if recover() != nil {
			w = nil
		}
	}()
	return webview2.NewWithOptions(webview2.WebViewOptions{Debug: devMode()})
}
