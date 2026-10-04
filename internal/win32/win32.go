//go:build windows && (amd64 || arm64)

// ─── Win32 清单页 ─────────────────────────────────────
// 集中声明全部 Win32 DLL 入口: 一页即可看清整个平台接触面,
// 未来若评估移植, 此处即待替换清单

package win32

import (
	"syscall"

	"github.com/LeuJasYoh/type/internal/typing"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
)

var (
	procSendInput                = user32.NewProc("SendInput")
	procSendMessageTimeoutW      = user32.NewProc("SendMessageTimeoutW")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procOpenClipboard            = user32.NewProc("OpenClipboard")
	procCloseClipboard           = user32.NewProc("CloseClipboard")
	procEmptyClipboard           = user32.NewProc("EmptyClipboard")
	procSetClipboardData         = user32.NewProc("SetClipboardData")
	procGetClipboardData         = user32.NewProc("GetClipboardData")
	procEnumClipboardFormats     = user32.NewProc("EnumClipboardFormats")
	procGlobalAlloc              = kernel32.NewProc("GlobalAlloc")
	procGlobalLock               = kernel32.NewProc("GlobalLock")
	procGlobalUnlock             = kernel32.NewProc("GlobalUnlock")
	procGlobalSize               = kernel32.NewProc("GlobalSize")
	procRtlMoveMemory            = kernel32.NewProc("RtlMoveMemory")
	procGetModuleHandleW         = kernel32.NewProc("GetModuleHandleW")
	procRedrawWindow             = user32.NewProc("RedrawWindow")
	procSHGetFileInfoW           = shell32.NewProc("SHGetFileInfoW")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procGetGUIThreadInfo         = user32.NewProc("GetGUIThreadInfo")
	procGetDpiForWindow          = user32.NewProc("GetDpiForWindow")
	procMonitorFromWindow        = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfoW          = user32.NewProc("GetMonitorInfoW")
	procGetClientRect            = user32.NewProc("GetClientRect")
	// GetWindowRect: 窗口矩形(含边框), "右下角是不是缩放边框"必须按它取点 ——
	// 按客户区取点会落在客户区里面, 任何带边框的窗口都答 HTCLIENT(见
	// hitTestBottomRight 的说明)
	procGetWindowRect    = user32.NewProc("GetWindowRect")
	procAdjustWindowRect = user32.NewProc("AdjustWindowRect")
	// DPI 版本: 非 DPI 版在"系统 DPI 与显示器 DPI 不同"的机器上按错的那个算边框,
	// 客户区会差十几个物理像素(客户区同时决定 WebView2 的渲染表面大小)
	procAdjustWindowRectExForDpi = user32.NewProc("AdjustWindowRectExForDpi")
	procGetWindowLongPtrW        = user32.NewProc("GetWindowLongPtrW")
	procSendMessageW             = user32.NewProc("SendMessageW")
	procGlobalFree               = kernel32.NewProc("GlobalFree")
	procRegisterClipboardFormatW = user32.NewProc("RegisterClipboardFormatW")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
	procSetClassLongPtrW         = user32.NewProc("SetClassLongPtrW")
	procMessageBoxW              = user32.NewProc("MessageBoxW")
	procShellExecuteW            = shell32.NewProc("ShellExecuteW")
	procCreateMutexW             = kernel32.NewProc("CreateMutexW")
	procCloseHandle              = kernel32.NewProc("CloseHandle")
)

// 三个平台实现与业务层接口的绑定在编译期核对: 签名一旦漂移立即构建失败,
// 不必等到装配时才发现
var (
	_ typing.TextInjector = Injector{}
	_ typing.Clipboard    = Clipboard{}
	_ typing.Foreground   = Foreground{}
)
