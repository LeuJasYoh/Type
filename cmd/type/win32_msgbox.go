//go:build windows && (amd64 || arm64)

// ─── 原生提示框 ───────────────────────────────────────
// 只走 user32 的 MessageBoxW, 不经过 WebView2。这一点是刻意的: 需要用提示框
// 告知用户的场景里, 恰恰包含"WebView2 起不来, 界面根本画不出来"这一类 ——
// 那时唯一还能用的通道就是系统自己的对话框。
// 单实例守卫与启动预检都通过它把话说出去。

package main

import (
	"syscall"
	"unsafe"
)

const (
	MB_OK              = 0x00000000
	MB_YESNO           = 0x00000004
	MB_ICONWARNING     = 0x00000030
	MB_ICONINFORMATION = 0x00000040
	MB_SETFOREGROUND   = 0x00010000
	MB_TOPMOST         = 0x00040000
	IDYES              = 6
)

// messageBox 显示置顶提示框, 返回被按下的按钮 ID(IDYES/IDOK 等)。
// 标题或正文转换失败时不显示、返回 0: 提示框自身出问题不该拖住主流程
func messageBox(title, text string, flags uintptr) int {
	titlePtr, err1 := syscall.UTF16PtrFromString(title)
	textPtr, err2 := syscall.UTF16PtrFromString(text)
	if err1 != nil || err2 != nil {
		return 0
	}
	ret, _, _ := procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(textPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		flags)
	return int(ret)
}
