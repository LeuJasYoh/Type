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
