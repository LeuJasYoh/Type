//go:build windows && (amd64 || arm64)

// ─── 窗口: 置顶/图标/前台窗口探测 ─────────────────────

package main

import (
	"os"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

const (
	WM_SETICON     = 0x0080
	ICON_SMALL     = 0
	ICON_BIG       = 1
	IMAGE_ICON     = 1
	LR_DEFAULTSIZE = 0x0040
)

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

	// 改窗口类图标
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

// retrySetIcon 设置窗口图标: 句柄只加载一次, 常驻至进程结束;
// 延迟重试弥补 WebView2 窗口创建早期图标未生效的情况
func retrySetIcon(hwnd uintptr) {
	hLarge, hSmall := loadAppIcon()
	applyWindowIcon(hwnd, hLarge, hSmall, true)
	go func() {
		delays := []time.Duration{500 * time.Millisecond, 1500 * time.Millisecond, 3000 * time.Millisecond}
		for _, d := range delays {
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
