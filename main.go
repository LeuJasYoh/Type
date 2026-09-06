package main

import (
	_ "embed"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/webview/webview_go"
)

// ─── 嵌入前端资源 ─────────────────────────────────────

// Vite 构建的自包含单文件 (vue-tsc + vite build 生成于 frontend/dist/)

//go:embed frontend/dist/index.html
var indexHTML string

var version = "1.3.1"

// ─── Win32 常量与结构 ──────────────────────────────────

const (
	INPUT_KEYBOARD    = 1
	KEYEVENTF_KEYUP   = 0x0002
	KEYEVENTF_UNICODE = 0x0004

	WM_SETICON     = 0x0080
	WM_CHAR        = 0x0102
	ICON_SMALL     = 0
	ICON_BIG       = 1
	IMAGE_ICON     = 1
	LR_DEFAULTSIZE = 0x0040

	SMTO_ABORTIFHUNG = 0x0002 // 目标窗口挂起时放弃消息, 不阻塞发送方

	CF_UNICODETEXT = 13
	GMEM_MOVABLE   = 0x0002
	GMEM_ZEROINIT  = 0x0040
	GHND           = GMEM_MOVABLE | GMEM_ZEROINIT

	VK_CONTROL = 0x11
	VK_V       = 0x56
	VK_RETURN  = 0x0D
)

type KEYBDINPUT struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type INPUT struct {
	_type uint32
	_     [4]byte
	ki    KEYBDINPUT
	_     [8]byte
}

// ─── Win32 DLL ─────────────────────────────────────────

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	shell32                      = syscall.NewLazyDLL("shell32.dll")
	procSendInput                = user32.NewProc("SendInput")
	procSendMessageTimeoutW      = user32.NewProc("SendMessageTimeoutW")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procOpenClipboard            = user32.NewProc("OpenClipboard")
	procCloseClipboard           = user32.NewProc("CloseClipboard")
	procEmptyClipboard           = user32.NewProc("EmptyClipboard")
	procSetClipboardData         = user32.NewProc("SetClipboardData")
	procGetClipboardData         = user32.NewProc("GetClipboardData")
	procGlobalAlloc              = kernel32.NewProc("GlobalAlloc")
	procGlobalLock               = kernel32.NewProc("GlobalLock")
	procGlobalUnlock             = kernel32.NewProc("GlobalUnlock")
	procGlobalSize               = kernel32.NewProc("GlobalSize")
	procRtlMoveMemory            = kernel32.NewProc("RtlMoveMemory")
	procGetModuleHandleW         = kernel32.NewProc("GetModuleHandleW")
	procLoadImageW               = user32.NewProc("LoadImageW")
	procDestroyIcon              = user32.NewProc("DestroyIcon")
	procRedrawWindow             = user32.NewProc("RedrawWindow")
	procSHGetFileInfoW           = shell32.NewProc("SHGetFileInfoW")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procGetGUIThreadInfo         = user32.NewProc("GetGUIThreadInfo")
	procGlobalFree               = kernel32.NewProc("GlobalFree")
)

var cancelFlag atomic.Bool
var topmostFlag atomic.Bool
var runningFlag atomic.Bool // 运行中互斥, 防止重复启动
var taskGen atomic.Uint64   // 任务代数: start/cancel 每次递增, 过代任务的状态写入一律作废

// ─── 输入状态（前端轮询读取）───────────────────────────

type TypingPhase string

const (
	PhaseIdle      TypingPhase = "idle"
	PhaseCountdown TypingPhase = "countdown"
	PhaseTyping    TypingPhase = "typing"
	PhaseSuccess   TypingPhase = "success"
	PhaseError     TypingPhase = "error"
	PhaseCancel    TypingPhase = "cancel"
)

type TypingStatus struct {
	Phase        TypingPhase `json:"phase"`
	Message      string      `json:"message"`
	Progress     int         `json:"progress"`    // 0-100，-1 表示隐藏
	SecondsLeft  int         `json:"secondsLeft"` // 倒计时剩余
	TargetWindow string      `json:"targetWindow"`
}

var typingStatus atomic.Value // 存 *TypingStatus

// ─── 窗口置顶 ─────────────────────────────────────────

const (
	HWND_TOPMOST   = ^uintptr(0) // -1
	HWND_NOTOPMOST = ^uintptr(1) // -2
	SWP_NOSIZE     = 0x0001
	SWP_NOMOVE     = 0x0002
	SWP_SHOWWINDOW = 0x0040
	SWP_NOACTIVATE = 0x0010
)

func setTopmost(hwnd uintptr, topmost bool) bool {
	insertAfter := HWND_NOTOPMOST
	if topmost {
		insertAfter = HWND_TOPMOST
	}
	ret, _, _ := user32.NewProc("SetWindowPos").Call(
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

// 缓存图标句柄
var cachedIcon, cachedSmall uintptr

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

func setWindowIcon(hwnd uintptr) {
	hLarge, hSmall := loadAppIcon()
	if hLarge == 0 && hSmall == 0 {
		return
	}

	// 缓存
	if hLarge != 0 {
		cachedIcon = hLarge
	}
	if hSmall != 0 {
		cachedSmall = hSmall
	}

	mi := hLarge
	if mi == 0 {
		mi = cachedIcon
	}
	si := hSmall
	if si == 0 {
		si = cachedSmall
	}

	// 用 PostMessage 异步发送
	if mi != 0 {
		procPostMessageW.Call(hwnd, WM_SETICON, ICON_BIG, mi)
	}
	if si != 0 {
		procPostMessageW.Call(hwnd, WM_SETICON, ICON_SMALL, si)
	}

	// 改窗口类图标
	const GCLP_HICON = ^uintptr(13)
	const GCLP_HICONSM = ^uintptr(33)
	if mi != 0 {
		user32.NewProc("SetClassLongPtrW").Call(hwnd, GCLP_HICON, mi)
	}
	if si != 0 {
		user32.NewProc("SetClassLongPtrW").Call(hwnd, GCLP_HICONSM, si)
	}

	// 强制重绘标题栏
	procRedrawWindow.Call(hwnd, 0, 0, 0x0001|0x0100)
}

func retrySetIcon(hwnd uintptr) {
	setWindowIcon(hwnd)
	go func() {
		delays := []time.Duration{500 * time.Millisecond, 1500 * time.Millisecond, 3000 * time.Millisecond}
		for _, d := range delays {
			time.Sleep(d)
			setWindowIcon(hwnd)
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

// focusedHWND 返回当前实际持有键盘焦点的窗口;
// 前台顶层窗口通常只是容器(如浏览器主窗口), 直接向其发消息会被丢弃
func focusedHWND() uintptr {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return 0
	}
	tid, _, _ := procGetWindowThreadProcessId.Call(hwnd, 0)
	if tid != 0 {
		var gti GUITHREADINFO
		gti.cbSize = uint32(unsafe.Sizeof(gti))
		if ret, _, _ := procGetGUIThreadInfo.Call(tid, uintptr(unsafe.Pointer(&gti))); ret != 0 && gti.hwndFocus != 0 {
			return gti.hwndFocus
		}
	}
	return hwnd
}

// foregroundWindowTitle 读取当前前台窗口标题(用于目标窗口预览)
func foregroundWindowTitle() string {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return ""
	}
	buf := make([]uint16, 256)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(unsafe.SliceData(buf))), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return string(utf16.Decode(buf[:n]))
}

// ─── 键盘模拟 ─────────────────────────────────────────

func sendInput(inputs []INPUT) uint32 {
	if len(inputs) == 0 {
		return 0
	}
	ret, _, _ := procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		unsafe.Sizeof(INPUT{}),
	)
	return uint32(ret)
}

func sendChar16(code uint16) {
	down := [1]INPUT{{
		_type: INPUT_KEYBOARD,
		ki:    KEYBDINPUT{wScan: code, dwFlags: KEYEVENTF_UNICODE},
	}}
	sendInput(down[:])
	time.Sleep(2 * time.Millisecond)

	up := [1]INPUT{{
		_type: INPUT_KEYBOARD,
		ki:    KEYBDINPUT{wScan: code, dwFlags: KEYEVENTF_UNICODE | KEYEVENTF_KEYUP},
	}}
	sendInput(up[:])
}

// utf16Units 将 rune 拆分为 1 或 2 个 UTF-16 码元(超出 BMP 时生成代理对)
func utf16Units(r rune) []uint16 {
	if r <= 0xFFFF {
		return []uint16{uint16(r)}
	}
	r -= 0x10000
	return []uint16{0xD800 | uint16(r>>10)&0x3FF, 0xDC00 | uint16(r)&0x3FF}
}

func sendRune(r rune) {
	if r >= 0xFF00 && r <= 0xFFEF {
		// 全角标点 (U+FF00-FFEF) — KEYEVENTF_UNICODE 有系统级 bug
		// 改用 WM_CHAR 直接注入到前台窗口
		sendCharViaWMChar(r)
		return
	}
	for _, u := range utf16Units(r) {
		sendChar16(u)
	}
}

// sendCharViaWMChar 通过 WM_CHAR 消息直接向前台窗口注入字符
// 绕过 KEYEVENTF_UNICODE 对全角标点的处理 bug
func sendCharViaWMChar(r rune) {
	hwnd := focusedHWND()
	if hwnd == 0 {
		// 兜底：退化为 SendInput
		sendChar16(uint16(r))
		return
	}
	// WM_CHAR 的 lParam 设 1 表示模拟键盘输入;
	// 带超时发送, 目标窗口挂起时放弃而不是卡死输入循环
	var result uintptr
	procSendMessageTimeoutW.Call(hwnd, WM_CHAR, uintptr(r), 1, SMTO_ABORTIFHUNG, 1000, uintptr(unsafe.Pointer(&result)))
	time.Sleep(2 * time.Millisecond)
}

func sendVK(vk uint16) {
	inputs := [2]INPUT{
		{_type: INPUT_KEYBOARD, ki: KEYBDINPUT{wVk: vk}},
		{_type: INPUT_KEYBOARD, ki: KEYBDINPUT{wVk: vk, dwFlags: KEYEVENTF_KEYUP}},
	}
	sendInput(inputs[:])
}

func sendCtrlV() {
	inputs := [4]INPUT{
		{_type: INPUT_KEYBOARD, ki: KEYBDINPUT{wVk: VK_CONTROL}},
		{_type: INPUT_KEYBOARD, ki: KEYBDINPUT{wVk: VK_V}},
		{_type: INPUT_KEYBOARD, ki: KEYBDINPUT{wVk: VK_V, dwFlags: KEYEVENTF_KEYUP}},
		{_type: INPUT_KEYBOARD, ki: KEYBDINPUT{wVk: VK_CONTROL, dwFlags: KEYEVENTF_KEYUP}},
	}
	sendInput(inputs[:])
}

// ─── 剪贴板 ───────────────────────────────────────────

// openClipboardWithRetry 打开剪贴板, 被其他程序占用时重试
func openClipboardWithRetry() bool {
	for attempt := 0; attempt < 4; attempt++ {
		ret, _, _ := procOpenClipboard.Call(0)
		if ret != 0 {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func clipboardSetText(text string) bool {
	if !openClipboardWithRetry() {
		return false
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()

	encoded := utf16.Encode([]rune(text + "\x00"))
	size := len(encoded) * 2
	hMem, _, _ := procGlobalAlloc.Call(GHND, uintptr(size))
	if hMem == 0 {
		return false
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem) // 加锁失败必须释放, 否则泄漏
		return false
	}
	// RtlMoveMemory 批量拷贝: 源为 Go 切片指针 (Pointer->uintptr 单向转换, vet 认可),
	// 目标为 Windows 返回的 uintptr 直接传入, 避免 uintptr->unsafe.Pointer 转换
	procRtlMoveMemory.Call(ptr, uintptr(unsafe.Pointer(unsafe.SliceData(encoded))), uintptr(size))
	procGlobalUnlock.Call(hMem)
	ret, _, _ := procSetClipboardData.Call(CF_UNICODETEXT, hMem)
	if ret == 0 {
		procGlobalFree.Call(hMem) // 系统未接管所有权时由调用方释放
	}
	return ret != 0
}

func clipboardGetText() string {
	if !openClipboardWithRetry() {
		return ""
	}
	defer procCloseClipboard.Call()
	hMem, _, _ := procGetClipboardData.Call(CF_UNICODETEXT)
	if hMem == 0 {
		return ""
	}
	size, _, _ := procGlobalSize.Call(hMem)
	if size == 0 {
		return ""
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		return ""
	}
	buf := make([]uint16, int(size)/2)
	// 拷贝长度不超过缓冲区容量, 防止 GlobalSize 返回奇数时越界
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(unsafe.SliceData(buf))), ptr, uintptr(len(buf)*2))
	procGlobalUnlock.Call(hMem)

	// 以 \x00 截断(块大小可能含填充)
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return string(utf16.Decode(buf[:n]))
}

// ─── 输入判断 ─────────────────────────────────────────

func containsNonASCII(s string) bool {
	for _, r := range s {
		if r > 0x7F {
			return true
		}
	}
	return false
}

// clipboardClear 清空剪贴板内容
func clipboardClear() bool {
	if !openClipboardWithRetry() {
		return false
	}
	defer procCloseClipboard.Call()
	ret, _, _ := procEmptyClipboard.Call()
	return ret != 0
}

// restoreClipboard 仅当剪贴板仍是本次注入的文本时恢复原内容,
// 避免覆盖用户在注入期间新复制的数据; 原内容为空则直接清空, 不留注入残留
func restoreClipboard(prev, injected string) {
	if clipboardGetText() != injected {
		return
	}
	if prev != "" {
		clipboardSetText(prev)
	} else {
		clipboardClear()
	}
}

// typeTextViaClipboard 执行剪贴板粘贴输入（两种模式共用）
func typeTextViaClipboard(text string) bool {
	prev := clipboardGetText()
	if !clipboardSetText(text) {
		return false
	}
	time.Sleep(100 * time.Millisecond)

	if cancelFlag.Load() {
		restoreClipboard(prev, text)
		return false
	}
	sendCtrlV()
	time.Sleep(200 * time.Millisecond)

	restoreClipboard(prev, text)
	return true
}

// ─── 主程序 ───────────────────────────────────────────

func main() {
	w := webview.New(true)
	defer w.Destroy()
	w.SetTitle("Type " + version)
	w.SetSize(540, 400, webview.HintFixed)

	// 设置窗口图标（首次 + 延迟重试）
	hw := uintptr(w.Window())
	retrySetIcon(hw)

	// 初始化状态
	typingStatus.Store(&TypingStatus{Phase: PhaseIdle})

	// 绑定 Go 函数到 JS
	// (须在加载页面前完成: 绑定的注入脚本对随后创建的文档生效)

	w.Bind("startTyping", func(text string, delay int, forceSendInput bool) (string, error) {
		// 上一任务活跃且并非取消收尾: 拒绝重入
		if runningFlag.Load() && !cancelFlag.Load() {
			return "", fmt.Errorf("已有输入任务在运行中，请先取消或等待完成")
		}
		// 同步写入倒计时初态: 前端 await 本调用后才开启轮询,
		// 保证首个 tick 必读到新状态; 过代旧任务被代数守卫拦截, 无法覆盖
		gen := taskGen.Add(1)
		typingStatus.Store(&TypingStatus{
			Phase:        PhaseCountdown,
			Message:      fmt.Sprintf("剩余 %d 秒 — 请聚焦目标窗口...", delay),
			SecondsLeft:  delay,
			Progress:     -1,
			TargetWindow: foregroundWindowTitle(),
		})
		go runTypingTask(gen, text, delay, forceSendInput)
		return "started", nil
	})

	w.Bind("cancelTyping", func() (string, error) {
		// 递增代数使在途任务的所有后续状态写入作废, 取消标志则加速其退出;
		// 此处不做等待 —— 旧实现阻塞 UI 线程最长 2 秒导致窗口冻结,
		// "取消后立即启动"的衔接由 runTypingTask 自行等待旧任务让出 runningFlag
		taskGen.Add(1)
		cancelFlag.Store(true)
		typingStatus.Store(&TypingStatus{
			Phase: PhaseCancel, Message: "已取消", Progress: -1,
		})
		return "cancelled", nil
	})

	w.Bind("toggleTopmost", func() (bool, error) {
		on := !topmostFlag.Load()
		topmostFlag.Store(on)
		if hw != 0 {
			setTopmost(hw, on)
		}
		return on, nil
	})

	// 前端轮询读取当前输入状态
	w.Bind("getTypingStatus", func() *TypingStatus {
		return typingStatus.Load().(*TypingStatus)
	})

	// 加载界面: 开发模式指向 Vite dev server (支持 HMR, 需先 npm run dev),
	// 默认加载嵌入的自包含页面
	if url := devServerURL(); url != "" {
		w.Navigate(url)
	} else {
		w.SetHtml(indexHTML)
	}

	w.Run()
}

// runTypingTask 执行一次完整的输入任务(倒计时 + 注入)。
// 倒计时初态已由 startTyping 同步写入, 此处从衔接/认领 runningFlag 开始。
func runTypingTask(gen uint64, text string, delay int, forceSendInput bool) {
	// gen 守卫的状态写入: 任务被更新一代的操作取代后, 静默停止输出
	setStatus := func(s *TypingStatus) {
		if gen == taskGen.Load() {
			typingStatus.Store(s)
		}
	}

	// 取消收尾衔接: 等待上一任务释放 runningFlag (取消标志会加速其退出, 上限 2 秒)
	for deadline := time.Now().Add(2 * time.Second); runningFlag.Load(); {
		if gen != taskGen.Load() {
			return // 已被新操作接管, 本次启动作废
		}
		if !time.Now().Before(deadline) {
			setStatus(&TypingStatus{Phase: PhaseError, Message: "启动失败：上一任务未能及时退出", Progress: -1})
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !runningFlag.CompareAndSwap(false, true) {
		return
	}
	defer runningFlag.Store(false)
	if gen != taskGen.Load() {
		return // 等待期间发生了新的取消/启动, 本次启动作废
	}
	// 认领成功后才清取消标志: 过早清除会让尚未退出的上一任务漏检取消而继续注入
	cancelFlag.Store(false)

	cancelled := cancelFlag.Load

	// ── 倒计时 ──
	for i := delay; i > 0; i-- {
		if cancelled() {
			return // cancelTyping 已写入取消状态
		}
		sec := i
		setStatus(&TypingStatus{
			Phase:        PhaseCountdown,
			Message:      fmt.Sprintf("剩余 %d 秒 — 请聚焦目标窗口...", sec),
			SecondsLeft:  sec,
			Progress:     -1,
			TargetWindow: foregroundWindowTitle(),
		})
		time.Sleep(1 * time.Second)
	}
	if cancelled() {
		return
	}

	time.Sleep(150 * time.Millisecond)

	// ── 执行 ──
	success := false
	if containsNonASCII(text) && !forceSendInput {
		setStatus(&TypingStatus{
			Phase: PhaseTyping, Message: "检测到中文，正在操作剪贴板...", Progress: -1,
		})
		success = typeTextViaClipboard(text)
		if !success && !cancelled() {
			setStatus(&TypingStatus{
				Phase: PhaseTyping, Message: "剪贴板操作失败", Progress: -1,
			})
		}
	} else {
		// 剔除 \r 使进度分母与实际注入次数一致 (\r\n 由 \n 触发回车)
		runes := []rune(strings.ReplaceAll(text, "\r", ""))
		total := len(runes)
		setStatus(&TypingStatus{
			Phase: PhaseTyping, Message: fmt.Sprintf("正在逐字符输入 0 / %d ...", total), Progress: 0,
		})

		typed := 0
		for _, r := range runes {
			if cancelled() {
				break
			}
			if r == '\n' {
				sendVK(VK_RETURN)
			} else {
				sendRune(r)
			}
			typed++

			if typed%8 == 0 || typed == total {
				setStatus(&TypingStatus{
					Phase:    PhaseTyping,
					Message:  fmt.Sprintf("正在逐字符输入 %d / %d ...", typed, total),
					Progress: typed * 100 / total,
				})
			}

			// 固定快速延迟: ASCII 8ms, CJK 12ms, 标点 16ms (给 IME 喘息)
			charDelay := 8 * time.Millisecond
			if r > 127 {
				if isCJKPunct(r) {
					charDelay = 16 * time.Millisecond
				} else {
					charDelay = 12 * time.Millisecond
				}
			}
			time.Sleep(charDelay)
		}
		success = !cancelled()
	}

	// 最终状态（前端检测到终止 phase 后停止轮询）; 过代则静默, 状态已由新操作接管
	switch {
	case cancelled():
		// cancelTyping 已写入取消状态
	case success:
		setStatus(&TypingStatus{Phase: PhaseSuccess, Message: "输入完成", Progress: -1})
	default:
		setStatus(&TypingStatus{Phase: PhaseError, Message: "输入失败", Progress: -1})
	}
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

// isCJKPunct 判断是否中日韩标点
func isCJKPunct(r rune) bool {
	return (r >= 0x3000 && r <= 0x303F) || // CJK 标点（、。！？：；等）
		(r >= 0xFF01 && r <= 0xFF0F) || // 全角标点 ！
		(r >= 0xFF1A && r <= 0xFF1F) || // ：；？
		(r >= 0x2018 && r <= 0x201D) // 中文引号 '' ""
}
