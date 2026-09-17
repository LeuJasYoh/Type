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
	VK_ESCAPE  = 0x1B
	VK_TAB     = 0x09
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
		return sendCharUnitsViaWMChar(r)
	}
	for _, u := range utf16Units(r) {
		if !sendChar16(u) {
			return false
		}
	}
	return true
}

// SendText 文本直投: 逐 UTF-16 码元经 WM_CHAR 直达前台焦点窗口 —— 不产生
// 按键事件, 目标编辑器挂在 keydown 层的补全弹窗劫持(空格/回车被当作
// "接受候选")与括号自动配对均无从触发。实测(Edge/Chromium 网页编辑器,
// tools/wmcharprobe + testdata/completion-guard.html): 文本逐字符原样落盘、
// 零 keydown、零配对; 与 SendRune 的按键层注入互为镜像
func (win32Injector) SendText(r rune) bool { return sendCharUnitsViaWMChar(r) }

// SendTab 注入 Tab 真键: 文本直投下回车/Tab 无法走文本层, 仍按键注入,
// 由业务层负责先发 Esc 关闭可能挂着的补全弹窗(见 sendEscaped)
func (win32Injector) SendTab() bool { return sendVK(VK_TAB) }

// SendEscape 注入 Esc 真键: 关闭目标编辑器的补全弹窗(其键义劫持的唯一
// 解除手段), 供文本直投模式在回车/Tab 前调用
func (win32Injector) SendEscape() bool { return sendVK(VK_ESCAPE) }

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

// sendCharUnitsViaWMChar 通过 WM_CHAR 消息向前台焦点窗口注入一个 rune
// (超出 BMP 时按代理对拆成两条消息)。两条用途共用: 全角标点绕行
// (KEYEVENTF_UNICODE 系统级 bug)与文本直投 SendText
func sendCharUnitsViaWMChar(r rune) bool {
	hwnd := focusedHWND()
	if hwnd == 0 {
		// 兜底：退化为 SendInput (逐码元按键注入)
		for _, u := range utf16Units(r) {
			if !sendChar16(u) {
				return false
			}
		}
		return true
	}
	// WM_CHAR 的 lParam 设 1 表示模拟键盘输入;
	// 带超时发送, 目标窗口挂起时放弃而不是卡死输入循环
	var result uintptr
	for _, u := range utf16Units(r) {
		ret, _, _ := procSendMessageTimeoutW.Call(hwnd, WM_CHAR, uintptr(u), 1, SMTO_ABORTIFHUNG, 1000, uintptr(unsafe.Pointer(&result)))
		if ret == 0 {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
	return true
}

func sendVK(vk uint16) bool {
	inputs := [2]INPUT{
		{_type: INPUT_KEYBOARD, ki: KEYBDINPUT{wVk: vk}},
		{_type: INPUT_KEYBOARD, ki: KEYBDINPUT{wVk: vk, dwFlags: KEYEVENTF_KEYUP}},
	}
	return sendInput(inputs[:]) == uint32(len(inputs))
}
