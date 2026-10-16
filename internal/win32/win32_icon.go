//go:build windows && (amd64 || arm64)

// ─── 窗口图标 (从 exe 自身提取, 设到标题栏/任务栏) ────

package win32

import (
	"os"
	"syscall"
	"time"
	"unsafe"
)

const (
	wmSetIcon     = 0x0080
	iconSmall     = 0
	iconBig       = 1
	imageIcon     = 1
	lrDefaultSize = 0x0040
)

const (
	shgfiIcon      = 0x100
	shgfiLargeIcon = 0x000
	shgfiSmallIcon = 0x001
)

// SHFILEINFO 用于 SHGetFileInfoW
type shFileInfo struct {
	hIcon         uintptr
	iIcon         int32
	dwAttributes  uint32
	szDisplayName [260]uint16
	szTypeName    [80]uint16
}

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
