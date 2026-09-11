//go:build windows && (amd64 || arm64)

// ─── Win32 清单页 ─────────────────────────────────────
// 集中声明全部 Win32 DLL 入口: 一页即可看清整个平台接触面,
// 未来若评估移植, 此处即待替换清单

package main

import "syscall"

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
	procGlobalFree               = kernel32.NewProc("GlobalFree")
	procRegisterClipboardFormatW = user32.NewProc("RegisterClipboardFormatW")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
	procSetClassLongPtrW         = user32.NewProc("SetClassLongPtrW")
	procMessageBoxW              = user32.NewProc("MessageBoxW")
	procCreateMutexW             = kernel32.NewProc("CreateMutexW")
	procCloseHandle              = kernel32.NewProc("CloseHandle")
)
