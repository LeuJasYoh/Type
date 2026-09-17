//go:build windows && (amd64 || arm64)

// ─── 键盘模拟 (SendInput + WM_CHAR) ───────────────────

package main

import (
	"time"
	"unsafe"
)

const (
	INPUT_KEYBOARD    = 1
	KEYEVENTF_KEYUP   = 0x0002
	KEYEVENTF_UNICODE = 0x0004

	WM_CHAR          = 0x0102
	SMTO_ABORTIFHUNG = 0x0002 // 目标窗口挂起时放弃消息, 不阻塞发送方

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

// ─── 键盘模拟 ─────────────────────────────────────────

// win32Injector TextInjector 的 Win32 实现:
// SendInput 注入 + 全角标点 WM_CHAR 绕行
type win32Injector struct{}

// SendRune 注入单个 rune, 返回是否被系统接受 (见 TextInjector 契约)
func (win32Injector) SendRune(r rune) bool {
	if r >= 0xFF00 && r <= 0xFFEF {
		// 全角标点 (U+FF00-FFEF) — KEYEVENTF_UNICODE 有系统级 bug
		// 改用 WM_CHAR 直接注入到前台窗口
		return sendCharViaWMChar(r)
	}
	for _, u := range utf16Units(r) {
		if !sendChar16(u) {
			return false
		}
	}
	return true
}

func (win32Injector) SendEnter() bool { return sendVK(VK_RETURN) }

// SendPaste 注入 Ctrl+V, 返回是否被系统接受
func (win32Injector) SendPaste() bool {
	inputs := [4]INPUT{
		{_type: INPUT_KEYBOARD, ki: KEYBDINPUT{wVk: VK_CONTROL}},
		{_type: INPUT_KEYBOARD, ki: KEYBDINPUT{wVk: VK_V}},
		{_type: INPUT_KEYBOARD, ki: KEYBDINPUT{wVk: VK_V, dwFlags: KEYEVENTF_KEYUP}},
		{_type: INPUT_KEYBOARD, ki: KEYBDINPUT{wVk: VK_CONTROL, dwFlags: KEYEVENTF_KEYUP}},
	}
	return sendInput(inputs[:]) == uint32(len(inputs))
}

// sendInput 返回实际插入的事件数。SendInput 被 UIPI 拦截(目标窗口权限更高)、
// 工作站锁定或输入桌面不可用时整体失败并返回 0, 部分失败则小于请求数,
// 故一律以"返回值等于请求数"作为成功判据
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

// sendChar16 注入一个 UTF-16 码元(按下+抬起), 两者都被接受才算成功
func sendChar16(code uint16) bool {
	down := [1]INPUT{{
		_type: INPUT_KEYBOARD,
		ki:    KEYBDINPUT{wScan: code, dwFlags: KEYEVENTF_UNICODE},
	}}
	ok := sendInput(down[:]) == 1
	time.Sleep(2 * time.Millisecond)

	up := [1]INPUT{{
		_type: INPUT_KEYBOARD,
		ki:    KEYBDINPUT{wScan: code, dwFlags: KEYEVENTF_UNICODE | KEYEVENTF_KEYUP},
	}}
	ok = sendInput(up[:]) == 1 && ok
	return ok
}

// utf16Units 将 rune 拆分为 1 或 2 个 UTF-16 码元(超出 BMP 时生成代理对)
func utf16Units(r rune) []uint16 {
	if r <= 0xFFFF {
		return []uint16{uint16(r)}
	}
	r -= 0x10000
	return []uint16{0xD800 | uint16(r>>10)&0x3FF, 0xDC00 | uint16(r)&0x3FF}
}

// sendCharViaWMChar 通过 WM_CHAR 消息直接向前台窗口注入字符
// 绕过 KEYEVENTF_UNICODE 对全角标点的处理 bug
func sendCharViaWMChar(r rune) bool {
	hwnd := focusedHWND()
	if hwnd == 0 {
		// 兜底：退化为 SendInput (全角标点均在 BMP 内, uint16 安全)
		return sendChar16(uint16(r))
	}
	// WM_CHAR 的 lParam 设 1 表示模拟键盘输入;
	// 带超时发送, 目标窗口挂起时放弃而不是卡死输入循环
	var result uintptr
	ret, _, _ := procSendMessageTimeoutW.Call(hwnd, WM_CHAR, uintptr(r), 1, SMTO_ABORTIFHUNG, 1000, uintptr(unsafe.Pointer(&result)))
	time.Sleep(2 * time.Millisecond)
	return ret != 0
}

func sendVK(vk uint16) bool {
	inputs := [2]INPUT{
		{_type: INPUT_KEYBOARD, ki: KEYBDINPUT{wVk: vk}},
		{_type: INPUT_KEYBOARD, ki: KEYBDINPUT{wVk: vk, dwFlags: KEYEVENTF_KEYUP}},
	}
	return sendInput(inputs[:]) == uint32(len(inputs))
}
