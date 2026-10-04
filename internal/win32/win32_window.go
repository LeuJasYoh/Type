//go:build windows && (amd64 || arm64)

// ─── 窗口: 尺寸与 DPI/置顶/图标/前台窗口探测 ──────────

package win32

import (
	"math"
	"os"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/LeuJasYoh/type/internal/typing"
)

const (
	wmSetIcon     = 0x0080
	iconSmall     = 0
	iconBig       = 1
	imageIcon     = 1
	lrDefaultSize = 0x0040
)

// MonitorFromWindow 的标志: 拿不到窗口所在显示器时退回主显示器
const monitorDefaultToPrimary = 1

// ─── 窗口尺寸与 DPI ───────────────────────────────────

// ScaledForDPI 把以 96 DPI 为基准的逻辑尺寸换算成窗口所在显示器的物理像素。
// 进程的 DPI 感知由 manifest 声明, 声明之后窗口坐标一律按物理像素解释: 不换算
// 的话固定尺寸窗口在 125% 显示器上会小两成。见 docs/invariants.md「其它不变量」
func ScaledForDPI(hwnd uintptr, w, h int) (int, int) {
	dpi, _, _ := procGetDpiForWindow.Call(hwnd)
	if dpi < 96 {
		dpi = 96 // 调用失败(0)或异常值: 按 100% 处理
	}
	return scaledByDPI(w, h, int(dpi))
}

// scaledByDPI 逻辑尺寸 → 物理像素的纯换算(不碰窗口, 可单测)。
//
// 取整必须四舍五入: 整数除法截断在非整数倍缩放下会稳定地少一个物理像素(1024 在
// 125% 下算出 1023), 而客户区尺寸同时决定 WebView2 的渲染表面大小 —— 表面比
// 客户区小 1 像素, Chromium 的输出就要被重采样, 正是"文字发虚"的来源之一
func scaledByDPI(w, h, dpi int) (int, int) {
	if dpi < 96 {
		dpi = 96
	}
	return roundDiv(w*dpi, 96), roundDiv(h*dpi, 96)
}

// roundDiv 四舍五入的整数除法: 逻辑尺寸 → 物理像素的换算要用它。
// 截断会让误差单向累积(599 @120dpi: 舍入 749, 截断 748); 负值走对称分支 ——
// 折回方向与负值分支一并留着, 换算规则本是双向的
func roundDiv(v, div int) int {
	if v < 0 {
		return -((-v + div/2) / div)
	}
	return (v + div/2) / div
}

// dpiForWindow 读窗口当前所在显示器的 DPI(取不到时按 96)。
// 它不一定是 96 的整数倍: Windows 允许 100、110、125 这类自定义缩放,
// 非整数倍正是取整误差最容易露头的地方
func dpiForWindow(hwnd uintptr) int {
	dpi, _, _ := procGetDpiForWindow.Call(hwnd)
	if dpi < 96 {
		return 96
	}
	return int(dpi)
}

// clientPhysicalSize 读客户区的物理像素尺寸, 即 WebView2 渲染表面的实际大小
func clientPhysicalSize(hwnd uintptr) (w, h int, ok bool) {
	var r rect
	if ret, _, _ := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ret == 0 {
		return 0, 0, false
	}
	return int(r.Right - r.Left), int(r.Bottom - r.Top), true
}

// ─── 按显示器定窗口尺寸(启动时一次) ──────────────────

// 窗口尺寸上下限(逻辑像素, 96 DPI 基准)。
// 以**高度**为主: 纵向固定成本约 420px(标题栏/输入框/选项行/按钮行/状态栏/目标条),
// 只有输入框能吸收余量; 实测 600 高时余量 146px、全给输入框就头重脚轻, 540 高时
// 余量约 70px、输入框约 300px 正好吃完, 宽度再由高度按 6:5 推出。
// 下限 480 是保守取值、不是"刚好放下"的临界值, 别按更紧数字往下调: 见 docs/invariants.md「其它不变量」
const (
	MinWindowW = 540
	MinWindowH = 480
	maxWindowW = 648
	maxWindowH = 540
)

// initialWindowSize 按显示器可用工作区(逻辑像素)算窗口尺寸(逻辑像素)。
// workW 刻意不用(宽度由高度推出); 只有工作区比下限还矮(小屏 / 横屏少见情形)
// 才据它收窄, 不让窗口高过屏幕
func initialWindowSize(workW, workH int) (w, h int) {
	_ = workW // 宽度由高度推出; 保留参数是为了将来要按工作区宽度兜底时有处可加
	h = workH * 49 / 100
	if h < MinWindowH {
		h = MinWindowH
	}
	if h > maxWindowH {
		h = maxWindowH
	}
	// 工作区比下限还矮: 按工作区收窄, 不让窗口高过屏幕(此时会低于 MinWindowH,
	// 这是"屏幕放不下"的现实, 不是配置错误)
	if workH > 0 && h > workH {
		h = workH
	}
	// 宽度必须由**收窄之后**的高度推, 否则比例会在这一步被破坏
	w = h * 6 / 5
	if w < MinWindowW {
		w = MinWindowW
	}
	if w > maxWindowW {
		w = maxWindowW
	}
	return w, h
}

// rect 屏幕/工作区矩形(物理像素)
type rect struct {
	Left, Top, Right, Bottom int32
}

// MONITORINFO 只用到 rcWork; cbSize 必须按结构体尺寸填(含尾部 dwFlags 的填充)
type monitorInfo struct {
	cbSize    uint32
	rcMonitor rect
	rcWork    rect
	dwFlags   uint32
}

// monitorWorkArea 取窗口所在显示器的可用工作区(物理像素), 已排除任务栏。
// 拿不到显示器信息时返回 false, 由调用方退回默认尺寸 —— 这条路径不该让程序
// 起不来, 也不该弹框: 它只是"尺寸算不准", 不是"界面建不出来"
func monitorWorkArea(hwnd uintptr) (rect, bool) {
	mon, _, _ := procMonitorFromWindow.Call(hwnd, monitorDefaultToPrimary)
	if mon == 0 {
		return rect{}, false
	}
	var mi monitorInfo
	mi.cbSize = uint32(unsafe.Sizeof(mi))
	if ret, _, _ := procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); ret == 0 {
		return rect{}, false
	}
	if mi.rcWork.Right <= mi.rcWork.Left || mi.rcWork.Bottom <= mi.rcWork.Top {
		return rect{}, false
	}
	return mi.rcWork, true
}

// WindowPlan 窗口的目标: 设计逻辑尺寸(内容缩放为 1 时的基准) + 客户区物理尺寸 + 位置
type WindowPlan struct {
	LogicalW, LogicalH int
	ClientW, ClientH   int
	X, Y               int
}

// PlanForDisplay 算出窗口的目标客户区尺寸与位置(全部物理像素), 并把**设计逻辑尺寸**
// 一并带出来: 内容缩放与窗口 DPI 缩放不等时, 要拿它乘真实比值重算一次客户区
// (见 WindowClientForScale)。
// ok=false 表示取不到显示器信息: 调用方退回默认尺寸, 不让"尺寸算不准"升级成
// "界面起不来"; hwnd 需已存在(DPI 与工作区都从它所在的显示器取)
func PlanForDisplay(hwnd uintptr) (WindowPlan, bool) {
	work, haveWork := monitorWorkArea(hwnd)
	if !haveWork {
		return WindowPlan{}, false
	}
	dpi := dpiForWindow(hwnd)
	// 工作区是物理像素, 先折回逻辑像素再算尺寸, 最后统一换算回物理像素:
	// 全程只经过一次四舍五入, 不会出现两次取整叠加的漂移
	workW := int(work.Right-work.Left) * 96 / dpi
	workH := int(work.Bottom-work.Top) * 96 / dpi
	lw, lh := initialWindowSize(workW, workH)
	cw, ch := scaledByDPI(lw, lh, dpi)

	plan := WindowPlan{
		LogicalW: lw, LogicalH: lh,
		ClientW: cw, ClientH: ch,
		X: int(work.Left) + (int(work.Right-work.Left)-cw)/2,
		Y: int(work.Top) + (int(work.Bottom-work.Top)-ch)/2,
	}
	if plan.X < int(work.Left) {
		plan.X = int(work.Left)
	}
	if plan.Y < int(work.Top) {
		plan.Y = int(work.Top)
	}
	return plan, true
}

// ─── 内容缩放(WebView2 的 rasterization scale)校正 ───
// 尺寸等式只有一条: **CSS 视口 = 客户区物理像素 / 内容缩放**。内容缩放由 WebView2
// 自己定(官方口径是"显示器缩放 × 用户文本大小", 还叠着页面缩放), 不等于窗口 DPI
// 缩放; 两者不等时视口会被压小、控件互相覆盖, 实测症状与处置见
// docs/invariants.md「其它不变量」, 所以启动后按页面报回的真实比值重设客户区

// maxContentScale 内容缩放的上界。Windows 的显示缩放最大 500%、"文本大小"最大 225%，
// 两个都拉满也到不了 16 倍；而且那种量级下窗口本来就会因为装不进工作区被拒。
// 这条上界存在的理由不是覆盖正常配置，而是不让天文数字走进 Win32 的 INT32 矩形：
// 实测 scale=1e15 时窗口矩形的 int32 截断会让"装不进工作区"的判定读到垃圾值而放行
const maxContentScale = 16

// scaledClientSize 设计逻辑尺寸 × 内容缩放 → 客户区物理像素(四舍五入)。
// 比值非法(≤0 / NaN / ±Inf / 超过 maxContentScale)或算出的尺寸超出 INT32 时返回 (0,0),
// 调用方按"不校正"处理 —— 这是个全函数, 不会把溢出值交给调用方
func scaledClientSize(lw, lh int, scale float64) (int, int) {
	if math.IsNaN(scale) || math.IsInf(scale, 0) || scale <= 0 || scale > maxContentScale {
		return 0, 0
	}
	fw := math.Round(float64(lw) * scale)
	fh := math.Round(float64(lh) * scale)
	// 判定放在 float 上, 不先转 int: 越界的 float→int 转换结果由实现决定
	if fw < 1 || fh < 1 || fw > math.MaxInt32 || fh > math.MaxInt32 {
		return 0, 0
	}
	return int(fw), int(fh)
}

// WindowClientForScale 按内容缩放算出窗口的目标客户区尺寸与居中位置(物理像素)。
// ok=false 有三种情形: 比值非法、算出的尺寸非正、或按它放大的整窗(含边框)装不进
// 工作区。此时调用方保持原尺寸 —— 宁可让界面滚动, 也不把窗口摆到屏幕外
func WindowClientForScale(hwnd uintptr, lw, lh int, scale float64) (cw, ch, x, y int, ok bool) {
	cw, ch = scaledClientSize(lw, lh, scale)
	if cw <= 0 || ch <= 0 {
		return 0, 0, 0, 0, false
	}
	work, haveWork := monitorWorkArea(hwnd)
	if !haveWork {
		return 0, 0, 0, 0, false
	}
	workW := int(work.Right - work.Left)
	workH := int(work.Bottom - work.Top)
	ww, wh := windowRectForClient(hwnd, cw, ch)
	if ww > workW || wh > workH {
		return 0, 0, 0, 0, false
	}
	x = int(work.Left) + (workW-cw)/2
	y = int(work.Top) + (workH-ch)/2
	return cw, ch, x, y, true
}

// windowRectForClient 把客户区尺寸(cw×ch, 物理像素)反推成窗口矩形的宽高。
// 必须用 AdjustWindowRectExForDpi: 非 DPI 版在"系统 DPI 与显示器 DPI 不同"的机器
// 上按错的那个算边框(客户区差十几个物理像素); 拿不到该入口(极老系统)才退回非 DPI 版
func windowRectForClient(hwnd uintptr, cw, ch int) (w, h int) {
	r := rect{Right: int32(cw), Bottom: int32(ch)}
	ok := false
	if err := procAdjustWindowRectExForDpi.Find(); err == nil {
		// 参数顺序是 (lpRect, dwStyle, bMenu, dwExStyle, dpi) —— dpi 是第 5 个。
		// 只传 4 个的话 dpi 会取到寄存器里的残留值, 边框被算成几百像素宽, 窗口
		// 直接涨成两倍多(实测 600×400 的请求摆出 1540×941 的客户区)
		ret, _, _ := procAdjustWindowRectExForDpi.Call(
			uintptr(unsafe.Pointer(&r)), wsOverlappedWindow, 0, 0, uintptr(dpiForWindow(hwnd)))
		ok = ret != 0
	}
	if !ok {
		if ret, _, _ := procAdjustWindowRect.Call(uintptr(unsafe.Pointer(&r)), wsOverlappedWindow, 0); ret == 0 {
			return cw, ch // 反推失败(理论上不会): 至少尺寸不错
		}
	}
	return int(r.Right - r.Left), int(r.Bottom - r.Top)
}

// SetWindowClientRect 把窗口摆到 (x, y), 并让**客户区**尺寸正好是 cw×ch(物理像素)。
// 不能把 cw/ch 直接交给 SetWindowPos: 它收的是含标题栏与边框的窗口矩形, 而客户区才是
// WebView2 渲染表面的大小; 少了换算客户区会矮一个标题栏 —— 底部被切, 表面仍与客户区
// 一致, 所以不发虚、只会静默少一截。边框反推走 DPI 版并读回复核(差 1px 以上再摆一次),
// 读回才是判据: 见 docs/invariants.md「其它不变量」
func SetWindowClientRect(hwnd uintptr, x, y, cw, ch int) {
	for attempt := 0; ; attempt++ {
		ww, wh := windowRectForClient(hwnd, cw, ch)
		procSetWindowPos.Call(
			hwnd, 0,
			toUint32(x), toUint32(y),
			uintptr(uint32(ww)), uintptr(uint32(wh)),
			swpNoZOrder|swpNoActivate|swpFrameChanged,
		)
		pw, ph, ok := clientPhysicalSize(hwnd)
		if !ok || (pw == cw && ph == ch) || attempt >= 1 {
			return
		}
		// 按差额补一次(两轮封顶): 只有边框推算与实际不一致时才会走到这里
		cw += cw - pw
		ch += ch - ph
	}
}

// wsOverlappedWindow 是 WS_OVERLAPPEDWINDOW: 库建窗时用的正是这个样式组合
// (webview.go 里以 0xCF0000 传给 CreateWindowExW), 反推窗口尺寸要与之一致
const wsOverlappedWindow = 0x00CF0000

// WS_THICKFRAME 是可拖拽的边框, WS_MAXIMIZEBOX 是最大化按钮(同样让尺寸可变)。
// 库的 SetSize(HintFixed) 会清掉这两个位, 但"库会清"与"产物里真的不可拖大"是
// 两件事(样式位可能被别处改回、边框可能没随样式刷新): 判据见
// docs/invariants.md「其它不变量」
const (
	wsThickFrame  = 0x00040000
	wsMaximizeBox = 0x00010000
)

// htBottomRight 是 WM_NCHITTEST 的 HTBOTTOMRIGHT: 只有它说明右下角是可拖拽的
// 缩放边框(HTCLIENT=1 是客户区, HTBORDER=18 是不可缩放的细边框, 两者都不可拖大)
const htBottomRight = 17

// toUint32 把一个坐标/尺寸值按 32 位传给 Win32。
// 直接 uintptr(v) 在 v 为负时会符号扩展成 0xFFFFFFFFxxxx (64 位下),
// 系统按 int32 读取时结果虽同, 但按 uintptr 读取(如 SWP 的坐标参数在某些
// 封装里)就会得到一个天文数字。显式截断成 32 位, 让行为不依赖调用约定
func toUint32(v int) uintptr {
	return uintptr(uint32(int32(v)))
}

// ─── 窗口置顶 ─────────────────────────────────────────

// windowStyle / hitTestBottomRight 是"窗口到底能不能拖大"的取证手段:
// 判据不能只看源码, 样式位要读回, 还要真的问系统一次命中测试; 且用例必须 A/B 两段
// (先证明取证手段认得出可拖大, 再清位证明认不出, 缺阳性对照则断言恒真) ——
// 见 docs/invariants.md「其它不变量」
func windowStyle(hwnd uintptr) uint32 {
	style, _, _ := procGetWindowLongPtrW.Call(hwnd, ^uintptr(15)) // GWL_STYLE = -16
	return uint32(style)
}

// hitTestBottomRight 在**窗口矩形**(含边框)右下角内侧 2px 处做一次 WM_NCHITTEST,
// 返回命中码: htBottomRight(17) 表示缩放边框, 其余(HTCLIENT=1 / HTBORDER=18)不可
// 拖大, 失败返回 -1。采样点必须按窗口矩形算: 按客户区取点落在客户区里面, 任何带
// 边框的窗口都只回 HTCLIENT, 断言于是对**可拖大**的窗口同样成立(2026-10 探针实测:
// 客户区取点 1, 窗口矩形取点 17)
func hitTestBottomRight(hwnd uintptr) int {
	var r rect
	if ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ret == 0 {
		return -1
	}
	// WM_NCHITTEST 的 lParam 是屏幕坐标打包成的 POINTS: 低 16 位 x, 高 16 位 y
	lp := int32(r.Bottom-2)<<16 | (r.Right - 2)
	ret, _, _ := procSendMessageW.Call(hwnd, wmNCHitTest, 0, uintptr(uint32(lp)))
	return int(int32(uint32(ret)))
}

// WM_NCHITTEST: 问系统"这个坐标点命中了窗口的哪个部位", 用来验证右下角
// 到底是不是缩放边框(见 hitTestBottomRight)
const wmNCHitTest = 0x0084

const (
	hwndTopmost     = ^uintptr(0) // -1
	hwndNotTopmost  = ^uintptr(1) // -2
	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020
	swpShowWindow   = 0x0040
)

func SetTopmost(hwnd uintptr, topmost bool) bool {
	insertAfter := hwndNotTopmost
	if topmost {
		insertAfter = hwndTopmost
	}
	ret, _, _ := procSetWindowPos.Call(
		hwnd, insertAfter, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpShowWindow|swpNoActivate,
	)
	return ret != 0
}

// ─── 窗口图标（从 exe 自身提取，设置标题栏/任务栏）─────

// SHFILEINFO 用于 SHGetFileInfoW
type shFileInfo struct {
	hIcon         uintptr
	iIcon         int32
	dwAttributes  uint32
	szDisplayName [260]uint16
	szTypeName    [80]uint16
}

const (
	shgfiIcon      = 0x100
	shgfiLargeIcon = 0x000
	shgfiSmallIcon = 0x001
)

// loadAppIcon 从当前 exe 提取大图标和小图标句柄
func loadAppIcon() (hLarge, hSmall uintptr) {
	exe, _ := os.Executable()
	exeW, _ := syscall.UTF16PtrFromString(exe)

	var fiLarge, fiSmall shFileInfo
	infoSize := unsafe.Sizeof(shFileInfo{})

	// 大图标（任务栏）
	procSHGetFileInfoW.Call(
		uintptr(unsafe.Pointer(exeW)),
		0,
		uintptr(unsafe.Pointer(&fiLarge)),
		infoSize,
		shgfiIcon|shgfiLargeIcon,
	)

	// 小图标（标题栏）
	procSHGetFileInfoW.Call(
		uintptr(unsafe.Pointer(exeW)),
		0,
		uintptr(unsafe.Pointer(&fiSmall)),
		infoSize,
		shgfiIcon|shgfiSmallIcon,
	)

	return fiLarge.hIcon, fiSmall.hIcon
}

// applyWindowIcon 应用图标到窗口。
// 所有权约定: WM_SETICON 设置的句柄归窗口所有, 替换/销毁时由系统释放, 因此
// 仅可对同一句柄执行一次, 不可复用; 类图标(SetClassLongPtr)不转移所有权,
// 句柄由我们持有至进程结束, 可幂等重设。标题栏与任务栏在窗口未设图标时
// 会回退到类图标, 故重试只需重设类图标并强制重绘
func applyWindowIcon(hwnd, hLarge, hSmall uintptr, withSetIcon bool) {
	if withSetIcon {
		if hLarge != 0 {
			procPostMessageW.Call(hwnd, wmSetIcon, iconBig, hLarge)
		}
		if hSmall != 0 {
			procPostMessageW.Call(hwnd, wmSetIcon, iconSmall, hSmall)
		}
	}

	// 改窗口类图标。索引是 WNDCLASSEX 内部字段的负字节偏移, 即文档值
	// GCLP_HICON = -14 / GCLP_HICONSM = -34 —— 这里以按位取反表达负数
	// (^x 是取反不是取负): ^uintptr(13) = -14, ^uintptr(33) = -34。
	// 值写错时 SetClassLongPtr 会以 ERROR_INVALID_INDEX 静默返回 0(无人采信),
	// 图标重设整体空转; win32_test.go 有真建窗口读回的实测用例钉住这两个值
	const GCLP_HICON = ^uintptr(13)
	const GCLP_HICONSM = ^uintptr(33)
	if hLarge != 0 {
		procSetClassLongPtrW.Call(hwnd, GCLP_HICON, hLarge)
	}
	if hSmall != 0 {
		procSetClassLongPtrW.Call(hwnd, GCLP_HICONSM, hSmall)
	}

	// 强制重绘标题栏
	procRedrawWindow.Call(hwnd, 0, 0, 0x0001|0x0100)
}

// iconRetryDelays 图标延迟重试的三档间隔: 句柄加载一次即可, 但 WebView2
// 窗口创建早期图标可能未生效, 按这三档补设类图标并重绘
var iconRetryDelays = []time.Duration{
	500 * time.Millisecond,
	1500 * time.Millisecond,
	3000 * time.Millisecond,
}

// RetrySetIcon 设置窗口图标: 句柄只加载一次, 常驻至进程结束;
// 延迟重试弥补 WebView2 窗口创建早期图标未生效的情况
func RetrySetIcon(hwnd uintptr) {
	hLarge, hSmall := loadAppIcon()
	applyWindowIcon(hwnd, hLarge, hSmall, true)
	go func() {
		for _, d := range iconRetryDelays {
			time.Sleep(d)
			applyWindowIcon(hwnd, hLarge, hSmall, false)
		}
	}()
}

// GUITHREADINFO / RECT 用于 GetGUIThreadInfo 定位焦点窗口
type winRect struct {
	Left, Top, Right, Bottom int32
}

type guiThreadInfo struct {
	cbSize        uint32
	flags         uint32
	hwndActive    uintptr
	hwndFocus     uintptr
	hwndCapture   uintptr
	hwndMenuOwner uintptr
	hwndMoveSize  uintptr
	hwndCaret     uintptr
	rcCaret       winRect
}

// focusedHWND 返回当前实际持有键盘焦点的窗口(前台顶层窗口通常只是容器, 直接向它
// 发消息会被丢弃)。拿不到焦点子窗口时返回 0, 不拿容器窗口顶替: 顶层窗口会丢
// WM_CHAR 而 SendMessageTimeout 照样返回成功, "一个字都没进去"会被报成注入成功,
// 调用方据此退化为按键注入。见 docs/invariants.md「文本直投（v1.5.0，WM_CHAR 文本层注入）」
func focusedHWND() uintptr {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return 0
	}
	tid, _, _ := procGetWindowThreadProcessId.Call(hwnd, 0)
	if tid == 0 {
		return 0
	}
	var gti guiThreadInfo
	gti.cbSize = uint32(unsafe.Sizeof(gti))
	if ret, _, _ := procGetGUIThreadInfo.Call(tid, uintptr(unsafe.Pointer(&gti))); ret != 0 {
		return gti.hwndFocus
	}
	return 0
}

// Foreground Foreground 接口的 Win32 实现。
// HWND 为本程序主窗口句柄, 用于识别"前台是否是自己"
type Foreground struct{ HWND uintptr }

// Sample 读取当前顶层前台窗口: 句柄 + 标题 + 是否为本程序自身。
// 三要素取自同一次 GetForegroundWindow: 分多次读会在间隙发生切换时得到互相矛盾的
// 组合(标题与锁定窗口不符、非自身判定失效); 标题走 GetWindowTextW 的缓存文本,
// 系统保证不因目标进程无响应而阻塞, 所以每拍都读标题是安全的。
// 见 docs/invariants.md「焦点锁定与漂移防护（v1.5.3）」
func (f Foreground) Sample() typing.ForegroundSample {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return typing.ForegroundSample{} // 无前台窗口: 标识 0, 标题空, 非自身
	}
	return typing.ForegroundSample{
		ID:    typing.TargetID(hwnd),
		Title: windowTitle(hwnd),
		Self:  hwnd == f.HWND,
	}
}

// windowTitle 读取指定顶层窗口的标题(目标窗口预览与终态展示用)
func windowTitle(hwnd uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(unsafe.SliceData(buf))), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return string(utf16.Decode(buf[:n]))
}
