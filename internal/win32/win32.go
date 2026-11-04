//go:build windows && (amd64 || arm64)

// ─── Win32 清单页 ─────────────────────────────────────
// 集中声明本包用到的 Win32 DLL 入口。窗体由 MyGo 管理, 这里只处理
// 外部窗口的输入注入、系统剪贴板、前台窗口探测与原生启动提示。

package win32

import (
	"syscall"

	"github.com/LeuJasYoh/type/internal/typing"
)

type procCaller interface {
	Call(...uintptr) (uintptr, uintptr, error)
}

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
)

var (
	procSendInput                = user32.NewProc("SendInput")
	procSendMessageTimeoutW      = user32.NewProc("SendMessageTimeoutW")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procOpenClipboard            = procCaller(user32.NewProc("OpenClipboard"))
	procCloseClipboard           = procCaller(user32.NewProc("CloseClipboard"))
	procEmptyClipboard           = procCaller(user32.NewProc("EmptyClipboard"))
	procSetClipboardData         = procCaller(user32.NewProc("SetClipboardData"))
	procGetClipboardData         = procCaller(user32.NewProc("GetClipboardData"))
	procEnumClipboardFormats     = user32.NewProc("EnumClipboardFormats")
	procGlobalAlloc              = procCaller(kernel32.NewProc("GlobalAlloc"))
	procGlobalLock               = procCaller(kernel32.NewProc("GlobalLock"))
	procGlobalUnlock             = kernel32.NewProc("GlobalUnlock")
	procGlobalSize               = kernel32.NewProc("GlobalSize")
	procRtlMoveMemory            = kernel32.NewProc("RtlMoveMemory")
	procGlobalFree               = procCaller(kernel32.NewProc("GlobalFree"))
	procRegisterClipboardFormatW = user32.NewProc("RegisterClipboardFormatW")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procGetGUIThreadInfo         = user32.NewProc("GetGUIThreadInfo")
	procMessageBoxW              = user32.NewProc("MessageBoxW")
	procShellExecuteW            = shell32.NewProc("ShellExecuteW")
)

// 三个平台实现与业务层接口的绑定在编译期核对: 签名一旦漂移立即构建失败,
// 不必等到装配时才发现
var (
	_ typing.TextInjector = Injector{}
	_ typing.Clipboard    = Clipboard{}
	_ typing.Foreground   = Foreground{}
)
