//go:build windows && (amd64 || arm64)

// ─── 原生提示框 ───────────────────────────────────────
// 只走 user32 的 MessageBoxW, 不经过 WebView2: 需要提示框的场景里恰恰包含
// "WebView2 起不来、界面画不出来"那一类, 那时唯一还能用的通道就是系统对话框。
// 单实例守卫与启动预检都通过它把话说出去

package win32

import (
	"syscall"
	"unsafe"
)

const (
	mbOK              = 0x00000000
	mbYesNo           = 0x00000004
	mbIconWarning     = 0x00000030
	mbIconInformation = 0x00000040
	mbSetForeground   = 0x00010000
	mbTopmost         = 0x00040000
	idYes             = 6
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
