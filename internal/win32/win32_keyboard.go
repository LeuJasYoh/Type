//go:build windows && (amd64 || arm64)

// ─── 键盘模拟 (SendInput + WM_CHAR) ───────────────────

package win32

import (
	"time"
	"unsafe"
)

const (
	inputKeyboard    = 1
	keyeventfKeyUp   = 0x0002
	keyeventfUnicode = 0x0004

	wmChar          = 0x0102
	smtoAbortIfHung = 0x0002 // 目标窗口挂起时放弃消息, 不阻塞发送方

	vkControl = 0x11
	vkV       = 0x56
	vkReturn  = 0x0D
	vkEscape  = 0x1B
)

// 注入之间的间隔与超时(调参集中处; 字符间隔的业务层三档见 internal/typing 常量区)
const (
	// keyDownUpGap 单个 UTF-16 码元按下与抬起之间的间隔
	keyDownUpGap = 2 * time.Millisecond
	// charUnitGap 文本直投相邻码元(代理对拆分后)之间的间隔
	charUnitGap = 2 * time.Millisecond
	// wmCharTimeoutMS WM_CHAR 直投的超时(毫秒): 目标窗口挂起时放弃消息,
	// 不阻塞发送方
	wmCharTimeoutMS = 1000
)

type keybdInput struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

// INPUT 结构体的手工填充(40 字节)仅匹配 64 位 ABI: 386 下真实布局为 28 字节,
// SendInput 会静默注入乱码, 因此本包连同产品只按 64 位构建
type input struct {
	_type uint32
	_     [4]byte
	ki    keybdInput
	_     [8]byte
}

// ─── 键盘模拟 ─────────────────────────────────────────

// Injector TextInjector 的 Win32 实现:
// SendInput 注入 + 全角标点 WM_CHAR 绕行
type Injector struct{}

// SendRune 注入单个 rune, 返回是否被系统接受 (见 TextInjector 契约)
func (Injector) SendRune(r rune) bool {
	if r >= 0xFF00 && r <= 0xFFEF {
		// 全角标点 (U+FF00-FFEF) — KEYEVENTF_UNICODE 有系统级 bug
		// 改用 WM_CHAR 直接注入到前台窗口
		return sendCharUnitsViaWMChar(r)
	}
	return sendCharUnitsViaInput(r)
}

// SendText 文本直投: 逐 UTF-16 码元经 WM_CHAR 直达前台焦点窗口 —— 不产生
// 按键事件, 目标编辑器挂在 keydown 层的补全弹窗劫持(空格/回车被当作
// "接受候选")与括号自动配对均无从触发。实测(Edge/Chromium 网页编辑器,
// tools/wmcharprobe + testdata/completion-guard.html): 普通字符与 Tab 逐字
// 原样落盘、零 keydown、零配对; 但 \n/\r 控制字符会被 Chromium 过滤, 换行
// 必须走真按键(见 internal/typing 的 sendEscaped)。与 SendRune 的按键层注入互为镜像
func (Injector) SendText(r rune) bool { return sendCharUnitsViaWMChar(r) }

// SendEscape 注入 Esc 真键: 关闭目标编辑器的补全弹窗(其键义劫持的唯一
// 解除手段), 供文本直投模式在回车前调用
func (Injector) SendEscape() bool { return sendVK(vkEscape) }

func (Injector) SendEnter() bool { return sendVK(vkReturn) }

// SendPaste 注入 Ctrl+V, 返回是否被系统接受
func (Injector) SendPaste() bool {
	inputs := [4]input{
		{_type: inputKeyboard, ki: keybdInput{wVk: vkControl}},
		{_type: inputKeyboard, ki: keybdInput{wVk: vkV}},
		{_type: inputKeyboard, ki: keybdInput{wVk: vkV, dwFlags: keyeventfKeyUp}},
		{_type: inputKeyboard, ki: keybdInput{wVk: vkControl, dwFlags: keyeventfKeyUp}},
	}
	return sendInput(inputs[:]) == uint32(len(inputs))
}

// sendInput 返回实际插入的事件数。SendInput 被 UIPI 拦截(目标窗口权限更高)、
// 工作站锁定或输入桌面不可用时整体失败并返回 0, 部分失败则小于请求数,
// 故一律以"返回值等于请求数"作为成功判据
func sendInput(inputs []input) uint32 {
	if len(inputs) == 0 {
		return 0
	}
	ret, _, _ := procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		unsafe.Sizeof(input{}),
	)
	return uint32(ret)
}

// sendChar16 注入一个 UTF-16 码元(按下+抬起), 两者都被接受才算成功
func sendChar16(code uint16) bool {
	down := [1]input{{
		_type: inputKeyboard,
		ki:    keybdInput{wScan: code, dwFlags: keyeventfUnicode},
	}}
	ok := sendInput(down[:]) == 1
	time.Sleep(keyDownUpGap)

	up := [1]input{{
		_type: inputKeyboard,
		ki:    keybdInput{wScan: code, dwFlags: keyeventfUnicode | keyeventfKeyUp},
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
//
// 拿不到焦点子窗口(返回 0)时退化为 SendInput 按键注入, 与"没有前台窗口"
// 同等对待: 顶层容器窗口会把 WM_CHAR 丢掉, 而 SendMessageTimeout 仍返回成功,
// 于是"一个字都没进去"被报成注入成功。
//
// 已知盲区, 未实测: 全角标点(SendRune 的第一条分支)也走这里, 而 SendInput 对
// U+FF00-FFEF 恰有那个系统级 bug(症状是标点重复、后续字符被吞)。退化为按键
// 之后这类字符会怎样, 没有真机验证过 —— `sendChar16` 判的是 SendInput 的入队
// 计数, 而那个 bug 发生在目标侧渲染, 入队照样成功, 所以很可能静默出错而不是
// 报失败。要下结论得用 tools/wmcharprobe 在真窗口上逐字比对
func sendCharUnitsViaWMChar(r rune) bool {
	hwnd := focusedHWND()
	if hwnd == 0 {
		return sendCharUnitsViaInput(r)
	}
	// WM_CHAR 的 lParam 设 1 表示模拟键盘输入;
	// 带超时发送, 目标窗口挂起时放弃而不是卡死输入循环
	var result uintptr
	for _, u := range utf16Units(r) {
		ret, _, _ := procSendMessageTimeoutW.Call(hwnd, wmChar, uintptr(u), 1, smtoAbortIfHung, wmCharTimeoutMS, uintptr(unsafe.Pointer(&result)))
		if ret == 0 {
			return false
		}
		time.Sleep(charUnitGap)
	}
	return true
}

// sendCharUnitsViaInput 退化路径: 逐 UTF-16 码元走按键注入
func sendCharUnitsViaInput(r rune) bool {
	for _, u := range utf16Units(r) {
		if !sendChar16(u) {
			return false
		}
	}
	return true
}

func sendVK(vk uint16) bool {
	inputs := [2]input{
		{_type: inputKeyboard, ki: keybdInput{wVk: vk}},
		{_type: inputKeyboard, ki: keybdInput{wVk: vk, dwFlags: keyeventfKeyUp}},
	}
	return sendInput(inputs[:]) == uint32(len(inputs))
}
