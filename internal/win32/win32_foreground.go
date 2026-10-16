//go:build windows && (amd64 || arm64)

// ─── 前台窗口探测与焦点子窗口定位 ─────────────────────

package win32

import (
	"unicode/utf16"
	"unsafe"

	"github.com/LeuJasYoh/type/internal/typing"
)

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
