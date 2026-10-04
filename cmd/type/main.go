//go:build windows && (amd64 || arm64)

// 仅 64 位: INPUT 结构体的手工填充(40 字节)仅匹配 64 位 ABI, 386 下
// SendInput 会静默注入乱码(见 internal/win32/win32_keyboard.go),
// 因此直接禁止 32 位编译
package main

import (
	"sync"
	"sync/atomic"

	"github.com/LeuJasYoh/type/internal/typing"
	"github.com/LeuJasYoh/type/internal/web"
	"github.com/LeuJasYoh/type/internal/win32"

	"github.com/jchv/go-webview2"
)

var version = "1.6.2"

// maxViewportFixes 内容缩放最多校正几次。正常只有一次(页面报回来的第一个比值),
// 留第二次是给"创建初期读到一个错的比值、随后自行修正"的兜底; 再往上就不跟了,
// 免得某个一直在变的读数把窗口摆来摆去
const maxViewportFixes = 2

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

	// Debug 打开 WebView2 的默认右键菜单与 DevTools; 正式构建两者皆关 —— 发布版留
	// DevTools 没有意义, 而右键菜单里的"重新加载"会让前端复位、与仍在跑的后端任务脱钩。
	// 见 docs/architecture.md「布局与单一来源」
	w := createWebView2()
	// 判空必须在 defer 之前: 失败时 New 返回 nil 接口, defer 求值 receiver 就 panic
	// 见 docs/invariants.md「其它不变量」
	if w == nil {
		win32.PromptWebView2Unusable(win32.MsgWebView2InitFailed) // 提示后退出, 不返回
	}
	defer w.Destroy()
	w.SetTitle("Type " + version)

	// 先取窗口句柄: 尺寸要按该窗口所在显示器的 DPI 与工作区换算
	// (manifest 声明了 PerMonitorV2, 见 internal/win32 的 ScaledForDPI)
	hw := uintptr(w.Window())

	// 尺寸与位置只在启动时算一次, 之后固定: 按窗口所在显示器的工作区定尺寸(内置
	// 540×480 ~ 648×540 的上下限, 见 internal/win32 的 initialWindowSize), 4K 上不会
	// 缩成一张邮票; 位置在该工作区内居中

	// 刻意不做的两件事, 别顺手加回来:
	//   ① 不响应 WM_DPICHANGED(把窗口拖到缩放比例不同的显示器上重新适配)。用户明确
	//      不要; 窗口侧改尺寸与内容侧 Chromium 改缩放不同步, 就会变成"渲染缩放 ≠
	//      显示器缩放", 那才是真正的位图拉伸发虚;
	//   ② 不放宽窗口样式。HintFixed 去掉 WS_THICKFRAME|WS_MAXIMIZEBOX 是"不可拖大"的
	//      唯一来源(internal/win32 的 TestWindowSizeIsFixed 读回样式位与命中测试钉着),
	//      只调 SetWindowClientRect 的话窗口仍可拖大

	// 顺序也是刻意的: 先让库 SetSize 拿到 HintFixed 并设一次 bounds, 再用
	// SetWindowClientRect 把客户区精确摆到目标 —— 反过来库会用非 DPI 版的边框再摆一次,
	// 把客户区带回偏差(见 internal/win32 的 windowRectForClient)

	// lw/lh 是设计逻辑尺寸(内容缩放为 1 时的基准), 留给下面的内容缩放校正:
	// 客户区 = 设计逻辑尺寸 × 真实内容缩放, CSS 视口才等于设计尺寸
	lw, lh := win32.MinWindowW, win32.MinWindowH
	if plan, ok := win32.PlanForDisplay(hw); ok {
		lw, lh = plan.LogicalW, plan.LogicalH
		w.SetSize(plan.ClientW, plan.ClientH, webview2.HintFixed)
		win32.SetWindowClientRect(hw, plan.X, plan.Y, plan.ClientW, plan.ClientH)
	} else {
		// 取不到显示器信息: 退回默认尺寸, 位置交给系统
		cw, ch := win32.ScaledForDPI(hw, lw, lh)
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

	// CSS 视口 = 客户区物理像素 ÷ 内容缩放; 内容缩放由 WebView2 自己定、不等于窗口 DPI 缩放,
	// 实测 125% 那台机器上内容按约 1.75 倍渲染、视口缩到 411×343, 输入框(195px 下限)压住选项行。
	// 见 docs/invariants.md「其它不变量」
	var (
		vpMu      sync.Mutex
		vpSeen    float64
		vpApplied int
	)
	w.Bind("reportViewport", func(dpr float64) error {
		vpMu.Lock()
		defer vpMu.Unlock()
		// 同一个比值只处理一次; 值变了(创建初期可能先报一个错的)允许再校正一次
		if vpApplied >= maxViewportFixes || dpr == vpSeen {
			return nil
		}
		cw, ch, x, y, ok := win32.WindowClientForScale(hw, lw, lh, dpr)
		if !ok {
			// 比值非法, 或按它算出的窗口装不进工作区: 保持原尺寸, 由前端滚动兜底。
			// 这里刻意不记账(不写 vpSeen): 同一个值以后可能就装得下了(工作区变了),
			// 记账会把那次机会吃掉
			return nil
		}
		vpSeen = dpr
		vpApplied++
		w.SetSize(cw, ch, webview2.HintFixed)
		win32.SetWindowClientRect(hw, x, y, cw, ch)
		return nil
	})

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
// **recover 不能删**: 控制器异步创建失败时 go-webview2 的 HRESULT 判断永不成立、接着
// 解引用 nil panic; GUI 子系统没有控制台, 不接住就是"双击之后窗口一闪就没了"。
// 见 docs/invariants.md「其它不变量」
func createWebView2() (w webview2.WebView) {
	defer func() {
		if recover() != nil {
			w = nil
		}
	}()
	return webview2.NewWithOptions(webview2.WebViewOptions{Debug: devMode()})
}
