//go:build windows && (amd64 || arm64)

// ─── 窗口: 尺寸与 DPI/置顶/图标/前台窗口探测 ──────────

package win32

import (
	"os"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/LeuJasYoh/type/internal/typing"
)

const (
	WM_SETICON     = 0x0080
	ICON_SMALL     = 0
	ICON_BIG       = 1
	IMAGE_ICON     = 1
	LR_DEFAULTSIZE = 0x0040
)

// MonitorFromWindow 的标志: 拿不到窗口所在显示器时退回主显示器
const MONITOR_DEFAULTTOPRIMARY = 1

// ─── 窗口尺寸与 DPI ───────────────────────────────────

// ScaledForDPI 把以 96 DPI 为基准的逻辑尺寸换算成窗口所在显示器的物理像素。
// 进程的 DPI 感知由 tools/mkres 生成的 manifest 声明(PerMonitorV2), 声明之后
// 窗口坐标一律按物理像素解释: 不换算的话, 在 125% 缩放的显示器上固定尺寸的
// 窗口会比预期小两成(旧的 webview 库在内部替调用方做了同一件事)
func ScaledForDPI(hwnd uintptr, w, h int) (int, int) {
	dpi, _, _ := procGetDpiForWindow.Call(hwnd)
	if dpi < 96 {
		dpi = 96 // 调用失败(0)或异常值: 按 100% 处理
	}
	return scaledByDPI(w, h, int(dpi))
}

// scaledByDPI 逻辑尺寸 → 物理像素的纯换算(不碰窗口, 可单测)。
//
// 取整必须四舍五入, 不能用整数除法的截断: 截断在非整数倍缩放下会稳定地少
// 一个物理像素(1024 在 125% 下算出 1023), 而客户区尺寸同时决定 WebView2 的
// 渲染表面大小 —— 表面比客户区小 1 像素, Chromium 的输出就要被重采样,
// 这正是"文字发虚"的来源之一。四舍五入把误差压到 ±0.5 物理像素
func scaledByDPI(w, h, dpi int) (int, int) {
	if dpi < 96 {
		dpi = 96
	}
	return roundDiv(w*dpi, 96), roundDiv(h*dpi, 96)
}

// roundDiv 四舍五入的整数除法。两个方向都要用它: 逻辑→物理用 dpi 乘,
// 物理→逻辑用 dpi 除, 截断会让误差单向累积(实测 100 DPI 下 720 逻辑 →
// 750 物理, 再截断折回就变成 720, 看着对; 但 599 这类值会稳定地少 1)
func roundDiv(v, div int) int {
	if v < 0 {
		return -((-v + div/2) / div)
	}
	return (v + div/2) / div
}

// DPIForWindow 读窗口当前所在显示器的 DPI(取不到时按 96)。
// 注意它不一定是 96 的整数倍: Windows 允许 100、110、125 这类自定义缩放,
// 本机实测就是 100 —— 非整数倍正是取整误差最容易露头的地方
func DPIForWindow(hwnd uintptr) int {
	dpi, _, _ := procGetDpiForWindow.Call(hwnd)
	if dpi < 96 {
		return 96
	}
	return int(dpi)
}

// ClientPhysicalSize 读客户区的物理像素尺寸, 即 WebView2 渲染表面的实际大小
func ClientPhysicalSize(hwnd uintptr) (w, h int, ok bool) {
	var r Rect
	if ret, _, _ := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ret == 0 {
		return 0, 0, false
	}
	return int(r.Right - r.Left), int(r.Bottom - r.Top), true
}

// ─── 按显示器定窗口尺寸(启动时一次) ──────────────────

// 窗口尺寸上下限(逻辑像素, 96 DPI 基准)。
//
// 为什么以**高度**为主而不是宽度: 本界面的纵向固定成本很高(标题栏 34 + 输入框
// 195 + 选项行 26 + 按钮行 36 + 状态栏 32 + 目标条 26 + 若干 gap ≈ 420px),
// 只有输入框能吸收余量。实测高到 600 时余量有 146px, 全给输入框会让它比按钮行
// 高九倍、头重脚轻; 收到 540 后余量约 70px, 输入框长到约 300px 正好吃完, 界面
// 不留空白也不失衡。宽度由高度按 6:5 推出来。
//
// 为什么有下限: 540 是文档化的默认宽度。高度下限 480 的来历见下──原先按"450 高
// 时目标行 + 两行状态文案会被裁"定的, 而实测(2026-10, layout-probe 逐像素量)
// 最长的那条终态文案在 540 宽下只占一行(实测 446px < 可用 470px), 状态栏恒为
// 单行 37.5px; 两行文案要到 496 宽才出现(那已在官方尺寸范围之外)。所以 480 是
// 留有余量的保守取值, 不是"刚好放下"的临界值, 别再按更紧的数字往下调
const (
	MinWindowW = 540
	MinWindowH = 480
	MaxWindowW = 648
	MaxWindowH = 540
)

// InitialWindowSize 按显示器可用工作区(逻辑像素)算窗口尺寸(逻辑像素)。
// workH 用不上时忽略(恒返回下限), 只有工作区比下限还矮(小屏 / 横屏少见情形)
// 才据它收窄, 不让窗口高过屏幕
func InitialWindowSize(workW, workH int) (w, h int) {
	_ = workW // 宽度由高度推出; 保留参数是为了将来要按工作区宽度兜底时有处可加
	h = workH * 49 / 100
	if h < MinWindowH {
		h = MinWindowH
	}
	if h > MaxWindowH {
		h = MaxWindowH
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
	if w > MaxWindowW {
		w = MaxWindowW
	}
	return w, h
}

// Rect 屏幕/工作区矩形(物理像素)
type Rect struct {
	Left, Top, Right, Bottom int32
}

// MONITORINFO 只用到 rcWork; cbSize 必须按结构体尺寸填(含尾部 dwFlags 的填充)
type MONITORINFO struct {
	cbSize    uint32
	rcMonitor Rect
	rcWork    Rect
	dwFlags   uint32
}

// MonitorWorkArea 取窗口所在显示器的可用工作区(物理像素), 已排除任务栏。
// 拿不到显示器信息时返回 false, 由调用方退回默认尺寸 —— 这条路径不该让程序
// 起不来, 也不该弹框: 它只是"尺寸算不准", 不是"界面建不出来"
func MonitorWorkArea(hwnd uintptr) (Rect, bool) {
	mon, _, _ := procMonitorFromWindow.Call(hwnd, MONITOR_DEFAULTTOPRIMARY)
	if mon == 0 {
		return Rect{}, false
	}
	var mi MONITORINFO
	mi.cbSize = uint32(unsafe.Sizeof(mi))
	if ret, _, _ := procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); ret == 0 {
		return Rect{}, false
	}
	if mi.rcWork.Right <= mi.rcWork.Left || mi.rcWork.Bottom <= mi.rcWork.Top {
		return Rect{}, false
	}
	return mi.rcWork, true
}

// WindowSizeForDisplay 算出窗口的目标客户区尺寸与位置(全部物理像素)。
// ok 为 false 表示取不到显示器信息: 此时返回默认尺寸, 位置交给系统决定
// (调用方用 SetSize 即可), 不让"尺寸算不准"升级成"界面起不来"。
// hwnd 需要已存在: DPI 与工作区都从它所在的显示器取
func WindowSizeForDisplay(hwnd uintptr) (w, h, x, y int, ok bool) {
	work, haveWork := MonitorWorkArea(hwnd)
	if !haveWork {
		cw, ch := ScaledForDPI(hwnd, MinWindowW, MinWindowH)
		return cw, ch, 0, 0, false
	}
	dpi, _, _ := procGetDpiForWindow.Call(hwnd)
	if dpi < 96 {
		dpi = 96
	}
	// 工作区是物理像素, 先折回逻辑像素再算尺寸, 最后统一换算回物理像素:
	// 全程只经过一次四舍五入, 不会出现两次取整叠加的漂移
	workW := int(work.Right-work.Left) * 96 / int(dpi)
	workH := int(work.Bottom-work.Top) * 96 / int(dpi)
	lw, lh := InitialWindowSize(workW, workH)
	cw, ch := scaledByDPI(lw, lh, int(dpi))

	x = int(work.Left) + (int(work.Right-work.Left)-cw)/2
	y = int(work.Top) + (int(work.Bottom-work.Top)-ch)/2
	if x < int(work.Left) {
		x = int(work.Left)
	}
	if y < int(work.Top) {
		y = int(work.Top)
	}
	return cw, ch, x, y, true
}

// SetWindowClientRect 把窗口摆到 (x, y), 并让**客户区**尺寸正好是 cw×ch
// (全部物理像素)。
//
// 为什么不能直接把 cw/ch 交给 SetWindowPos: 它收的是窗口矩形(含标题栏与
// 边框), 而客户区才是 WebView2 渲染表面的大小。少了这一步换算, 客户区会比
// 目标矮一个标题栏 —— 界面底部被切一条, 而表面与客户区仍然一致, 所以不会
// 发虚, 只会静默少一截, 更不容易发现
func SetWindowClientRect(hwnd uintptr, x, y, cw, ch int) {
	r := Rect{Right: int32(cw), Bottom: int32(ch)}
	// AdjustWindowRect 按普通重叠窗口的框架把客户区尺寸反推成窗口尺寸
	if ret, _, _ := procAdjustWindowRect.Call(uintptr(unsafe.Pointer(&r)), wsOverlappedWindow, 0); ret == 0 {
		// 反推失败(理论上不会): 退回按客户区尺寸摆放, 至少尺寸不错
		r = Rect{Right: int32(cw), Bottom: int32(ch)}
	}
	procSetWindowPos.Call(
		hwnd, 0,
		toUint32(x), toUint32(y),
		uintptr(uint32(r.Right-r.Left)), uintptr(uint32(r.Bottom-r.Top)),
		SWP_NOZORDER|SWP_NOACTIVATE|SWP_FRAMECHANGED,
	)
}

// wsOverlappedWindow 是 WS_OVERLAPPEDWINDOW: 库建窗时用的正是这个样式组合
// (webview.go 里以 0xCF0000 传给 CreateWindowExW), 反推窗口尺寸要与之一致
const wsOverlappedWindow = 0x00CF0000

// toUint32 把一个坐标/尺寸值按 32 位传给 Win32。
// 直接 uintptr(v) 在 v 为负时会符号扩展成 0xFFFFFFFFxxxx (64 位下),
// 系统按 int32 读取时结果虽同, 但按 uintptr 读取(如 SWP 的坐标参数在某些
// 封装里)就会得到一个天文数字。显式截断成 32 位, 让行为不依赖调用约定
func toUint32(v int) uintptr {
	return uintptr(uint32(int32(v)))
}

// ClientLogicalSize 读回窗口客户区的逻辑尺寸(把物理像素按当前 DPI 折回 96 DPI
// 基准)。存在的理由只有一个: 客户区同时决定 WebView2 的渲染表面大小, 表面与
// 客户区一旦不匹配(哪怕差 1 像素), Chromium 的输出就会被重采样成"发虚"。
// 所以"请求的逻辑尺寸"与"读回的客户区逻辑尺寸"必须相等, 这是可校验的事实
func ClientLogicalSize(hwnd uintptr) (w, h int, ok bool) {
	pw, ph, hasRect := ClientPhysicalSize(hwnd)
	if !hasRect {
		return 0, 0, false
	}
	dpi := DPIForWindow(hwnd)
	// 与 scaledByDPI 用同一个 roundDiv: 折回方向若改用截断, 在 100 DPI 这类
	// 非整数倍缩放下会凭空少 1 像素, 看上去像"窗口尺寸算错了"
	return roundDiv(pw*96, dpi), roundDiv(ph*96, dpi), true
}

// ─── 窗口置顶 ─────────────────────────────────────────

// WindowStyle / HitTestBottomRight 是"窗口到底能不能拖大"的取证手段。
// 库的 SetSize(HintFixed) 声称移除 WS_THICKFRAME|WS_MAXIMIZEBOX, 但实测样式位
// 仍在(见 main.go 的注释): 于是判据不能只看源码, 得读回样式位并真的问一次
// 命中测试 —— 只有 HTBOTTOMRIGHT 才说明右下角是个可拖拽的边框
func WindowStyle(hwnd uintptr) uint32 {
	style, _, _ := procGetWindowLongPtrW.Call(hwnd, ^uintptr(15)) // GWL_STYLE = -16
	return uint32(style)
}

// HitTestBottomRight 在窗口右下角 2px 处做一次 WM_NCHITTEST, 返回命中码:
// HTBOTTOMRIGHT(17) 表示那里是缩放边框, HTCLIENT(1) 表示是客户区(不可拖大)
func HitTestBottomRight(hwnd uintptr) int {
	var r Rect
	if ret, _, _ := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ret == 0 {
		return -1
	}
	// 客户区右下角转成窗口坐标(含边框), 再往内收 2px 落在边框带上
	var origin struct{ x, y int32 }
	if ret, _, _ := procClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&origin))); ret == 0 {
		return -1
	}
	lp := int32(origin.y+int32(r.Bottom)-2)<<16 | (origin.x + r.Right - 2)
	ret, _, _ := procSendMessageW.Call(hwnd, WM_NCHITTEST, 0, uintptr(uint32(lp)))
	return int(int32(uint32(ret)))
}

// WM_NCHITTEST: 问系统"这个坐标点命中了窗口的哪个部位", 用来验证右下角
// 到底是不是缩放边框(见 HitTestBottomRight)
const WM_NCHITTEST = 0x0084

const (
	HWND_TOPMOST     = ^uintptr(0) // -1
	HWND_NOTOPMOST   = ^uintptr(1) // -2
	SWP_NOSIZE       = 0x0001
	SWP_NOMOVE       = 0x0002
	SWP_NOZORDER     = 0x0004
	SWP_NOACTIVATE   = 0x0010
	SWP_FRAMECHANGED = 0x0020
	SWP_SHOWWINDOW   = 0x0040
)

func SetTopmost(hwnd uintptr, topmost bool) bool {
	insertAfter := HWND_NOTOPMOST
	if topmost {
		insertAfter = HWND_TOPMOST
	}
	ret, _, _ := procSetWindowPos.Call(
		hwnd, insertAfter, 0, 0, 0, 0,
		SWP_NOMOVE|SWP_NOSIZE|SWP_SHOWWINDOW|SWP_NOACTIVATE,
	)
	return ret != 0
}

// ─── 窗口图标（从 exe 自身提取，设置标题栏/任务栏）─────

// SHFILEINFO 用于 SHGetFileInfoW
type SHFILEINFO struct {
	hIcon         uintptr
	iIcon         int32
	dwAttributes  uint32
	szDisplayName [260]uint16
	szTypeName    [80]uint16
}

const (
	SHGFI_ICON      = 0x100
	SHGFI_LARGEICON = 0x000
	SHGFI_SMALLICON = 0x001
)

// loadAppIcon 从当前 exe 提取大图标和小图标句柄
func loadAppIcon() (hLarge, hSmall uintptr) {
	exe, _ := os.Executable()
	exeW, _ := syscall.UTF16PtrFromString(exe)

	var fiLarge, fiSmall SHFILEINFO
	infoSize := unsafe.Sizeof(SHFILEINFO{})

	// 大图标（任务栏）
	procSHGetFileInfoW.Call(
		uintptr(unsafe.Pointer(exeW)),
		0,
		uintptr(unsafe.Pointer(&fiLarge)),
		infoSize,
		SHGFI_ICON|SHGFI_LARGEICON,
	)

	// 小图标（标题栏）
	procSHGetFileInfoW.Call(
		uintptr(unsafe.Pointer(exeW)),
		0,
		uintptr(unsafe.Pointer(&fiSmall)),
		infoSize,
		SHGFI_ICON|SHGFI_SMALLICON,
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
			procPostMessageW.Call(hwnd, WM_SETICON, ICON_BIG, hLarge)
		}
		if hSmall != 0 {
			procPostMessageW.Call(hwnd, WM_SETICON, ICON_SMALL, hSmall)
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
type RECT struct {
	Left, Top, Right, Bottom int32
}

type GUITHREADINFO struct {
	cbSize        uint32
	flags         uint32
	hwndActive    uintptr
	hwndFocus     uintptr
	hwndCapture   uintptr
	hwndMenuOwner uintptr
	hwndMoveSize  uintptr
	hwndCaret     uintptr
	rcCaret       RECT
}

// focusedTarget 从 GetGUIThreadInfo 的结果里挑出可安全投递 WM_CHAR 的落点。
// 拿不到焦点子窗口(hwndFocus 为 0)时返回 0, 不拿容器窗口顶替 —— 顶层窗口
// (浏览器主窗口那类)会把 WM_CHAR 丢掉, 而 SendMessageTimeout 照样返回成功,
// 于是"一个字都没进去"被报成注入成功。
// 单独抽出来是为了能直接测这个判断, 它的另一半(GUI 线程查询本身)要靠真窗口
func focusedTarget(hwndFocus uintptr) uintptr {
	if hwndFocus == 0 {
		return 0
	}
	return hwndFocus
}

// focusedHWND 返回当前实际持有键盘焦点的窗口;
// 前台顶层窗口通常只是容器(如浏览器主窗口), 直接向其发消息会被丢弃。
// 拿不到焦点子窗口时返回 0 —— 这与"没有前台窗口"一样, 都意味着没有可安全
// 投递 WM_CHAR 的落点, 调用方据此退化为按键注入(见 win32_keyboard.go)
func focusedHWND() uintptr {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return 0
	}
	tid, _, _ := procGetWindowThreadProcessId.Call(hwnd, 0)
	if tid == 0 {
		return 0
	}
	var gti GUITHREADINFO
	gti.cbSize = uint32(unsafe.Sizeof(gti))
	if ret, _, _ := procGetGUIThreadInfo.Call(tid, uintptr(unsafe.Pointer(&gti))); ret != 0 {
		return focusedTarget(gti.hwndFocus)
	}
	return 0
}

// Foreground Foreground 接口的 Win32 实现。
// HWND 为本程序主窗口句柄, 用于识别"前台是否是自己"
type Foreground struct{ HWND uintptr }

// Sample 读取当前顶层前台窗口: 句柄 + 标题 + 是否为本程序自身。
// 三要素取自同一次 GetForegroundWindow: 分多次读会在两次调用的间隙发生
// 切换时得到互相矛盾的组合(展示的标题不是锁定下来的那个窗口; "非自身"
// 判定与目标锁定之间切回 Type, 漂移守卫从第一步就失效)。
// 跨进程顶层窗口的标题是 GetWindowTextW 直接取回的缓存文本, 不会因目标
// 进程无响应而阻塞(系统如此设计), 因此每拍都读标题是安全的
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
