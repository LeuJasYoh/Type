//go:build windows && (amd64 || arm64)

// ─── 注入通道的进程内读回 ─────────────────────────────
// 在测试进程里真建一个 EDIT 控件, 把字符打进去再读回来逐字节比对, 回答
// docs/invariants.md「文本直投」长期标着"未实测"的问题: 全角标点走 SendInput
// 会不会重复或吞字。
// 实测结论、它的边界, 以及这夹具踩过的四条坑(锁线程/WS_VISIBLE/泵到读出来/
// 消息号别传错)都在 docs/invariants.md「注入通道的进程内读回（2026-10 实测）」

package win32

import (
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"
)

// 仅这组用例用到的 Win32 入口(产物不依赖它们, 故不与 win32.go 那份清单混在一起)
var (
	injectGetParent     = user32.NewProc("GetParent")
	injectSetFocus      = user32.NewProc("SetFocus")
	injectSetForeground = user32.NewProc("SetForegroundWindow")
	injectPeek          = user32.NewProc("PeekMessageW")
	injectTranslate     = user32.NewProc("TranslateMessage")
	injectDispatch      = user32.NewProc("DispatchMessageW")
)

// injectMsg MSG; 64 位下 48 字节, 末字段是 SDK 里的 lPrivate(只读前几个字段)
type injectMsg struct {
	hwnd    uintptr
	message uint32
	_       uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	ptX     int32
	ptY     int32
	_       uint32
}

const (
	pmRemove      = 0x0001
	wmSetText     = 0x000C // 0x000B 是 WM_SETREDRAW, 传错会把控件弄成不重绘且不报错
	wsVisible     = 0x10000000
	wsChild       = 0x40000000
	wsBorder      = 0x00800000
	esMultiLine   = 0x0004
	esAutoVScroll = 0x0040
	esWantReturn  = 0x1000
)

// pumpInput 把线程输入队列里的消息喂给窗口, 直到队列空或时间片用完。
// SendInput 的事件排在队列里, 不泵不落; KEYEVENTF_UNICODE 还必须经 TranslateMessage
// 才会产出 WM_CHAR, 少了这一步 EDIT 什么都收不到, 而 SendInput 照样返回 1 ——
// 它的返回值只说明"有没有排进队列"
func pumpInput(budget time.Duration) {
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		var m injectMsg
		ret, _, _ := injectPeek.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove)
		if ret == 0 {
			return
		}
		injectTranslate.Call(uintptr(unsafe.Pointer(&m)))
		injectDispatch.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// controlMessage 向同进程的控件发消息, 一律带超时: 目标一侧卡住时不陪它卡下去,
// 与产品 sendCharUnitsViaWMChar 同一条判据
func controlMessage(hwnd, msg, wparam, lparam uintptr) bool {
	ret, _, _ := procSendMessageTimeoutW.Call(hwnd, msg, wparam, lparam, smtoAbortIfHung, 1000, 0)
	return ret != 0
}

// newEditControl 建一个顶层和它的 EDIT 子控件, 随测试结束销毁。
// tag 必须每个用例不同: newTestWindow 的类名是 "TypeTest-<tag>-<pid>", 同一进程里
// 两个用例用同一个 tag 会 RegisterClassExW 报 Class already exists(实测抖动来源)
func newEditControl(t *testing.T, tag string) uintptr {
	t.Helper()
	w := newTestWindow(t, tag)
	top := w.createStyled(t, "Type 注入读回", wsOverlappedWindow|wsVisible)
	_ = top

	className, _ := syscall.UTF16PtrFromString("EDIT")
	edit, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		0,
		uintptr(wsChild|wsVisible|wsBorder|esMultiLine|esAutoVScroll|esWantReturn),
		0, 0, 380, 260,
		top, 0, w.hInst, 0,
	)
	if edit == 0 {
		t.Fatalf("CreateWindowExW(EDIT) 失败: %v", err)
	}
	t.Cleanup(func() { procDestroyWindow.Call(edit) })
	return edit
}

// focusEdit 把测试窗口顶到前台并把焦点交给 EDIT, 返回控件句柄。
//
// 这一步的安全意义高于便利: focusedHWND 取的是**全局**前台窗口, 不认它就会取到别人的
// 窗口, 注入的内容会真的打进别的程序。所以宁可跳过也不许在焦点没确认时注入
func focusEdit(t *testing.T, edit uintptr) {
	t.Helper()
	top, _, _ := injectGetParent.Call(edit)
	if top == 0 {
		top = edit
	}
	injectSetForeground.Call(top)
	for i := 0; i < 40; i++ {
		if cur, _, _ := procGetForegroundWindow.Call(); cur == top {
			break
		}
		pumpInput(25 * time.Millisecond)
	}
	if cur, _, _ := procGetForegroundWindow.Call(); cur != top {
		// 与 TestClipboardSnapshotRoundtrip 同一口径: 真系统资源用不了就写明情况再跳过,
		// 但 CI 上必须红 —— 这组用例存在的理由就是守住这条读写链路的回归
		skipOrFailInCI(t, "测试窗口没当上前台窗口, 无法做注入读回(焦点没确认就不注入)")
	}
	injectSetFocus.Call(edit)
	pumpInput(50 * time.Millisecond)
	if got := focusedHWND(); got != edit {
		t.Fatalf("focusedHWND = %#x, 期望 EDIT %#x: 焦点没落到控件上", got, edit)
	}
}

// ensureFocus 重新确认焦点还在 EDIT 上, 不在就再抢一次。
// 前台是全局资源, 别的程序随时可能抢走它(实测: 跑 go test ./... 时别的包在忙,
// 或桌面上有别的窗口激活), 抢走之后 SendInput 会打到别处, 于是"读回为空"。
// 那不是被测行为出错, 是环境变了 —— 认不出这一点就会把用例做成随机红的那种
func ensureFocus(t *testing.T, edit uintptr) {
	t.Helper()
	if focusedHWND() == edit {
		return
	}
	focusEdit(t, edit)
}

// clearEdit 清空控件文本。lParam 必须指向真的宽字符串, 空指针会把 EDIT 挂死
func clearEdit(t *testing.T, edit uintptr) {
	t.Helper()
	empty, _ := syscall.UTF16PtrFromString("")
	if !controlMessage(edit, wmSetText, 0, uintptr(unsafe.Pointer(empty))) {
		t.Fatalf("WM_SETTEXT 清空 EDIT 失败")
	}
}

// editText 读回控件全文(WM_GETTEXT 由 GetWindowTextW 代发)
func editText(edit uintptr) string {
	buf := make([]uint16, 1024)
	n, _, _ := procGetWindowTextW.Call(edit, uintptr(unsafe.Pointer(unsafe.SliceData(buf))), uintptr(len(buf)))
	return string(utf16.Decode(buf[:n]))
}

// injectAndWait 清空 → 逐个注入 → 一直泵到文本稳定或超时, 返回读回的文本。
// 等待是关键: 字符会晚到, 定长泵一段就读数会把"没送到"误判成"丢了"
func injectAndWait(t *testing.T, edit uintptr, text string, send func(rune) bool) string {
	t.Helper()
	// 前台是全局资源, 随时可能被抢走(上一例刚销毁窗口、桌面上别的窗口激活、
	// 跑 go test ./... 时别的包在忙)。抢走之后 SendInput 会打到别处, 于是整串
	// 读回为空 —— 那看起来像"内容丢了", 其实是打进了别的窗口。所以最多试三轮,
	// 每轮都重新抢焦点。断言(整串逐字节相等)从头到尾没有放宽, 放宽的只是
	// "环境抖动"这一层的容忍度; 三轮都等不到才交给调用方报错
	const attempts = 3
	var got string
	for attempt := 0; attempt < attempts; attempt++ {
		ensureFocus(t, edit)
		clearEdit(t, edit)
		if leftover := editText(edit); leftover != "" {
			t.Fatalf("清空之后读回 %q: 夹具自身的前提坏了", leftover)
		}
		for _, r := range text {
			if !send(r) {
				t.Fatalf("注入 %q 中的 %U 被系统拒绝", text, r)
			}
			pumpInput(5 * time.Millisecond)
		}
		start := time.Now()
		for time.Now().Before(start.Add(1500 * time.Millisecond)) {
			pumpInput(20 * time.Millisecond)
			if got = editText(edit); got == text {
				return got
			}
		}
	}
	return got
}

// TestInjectorReadsBackEveryChar 逐字符过一遍两条底层通道与产品入口
func TestInjectorReadsBackEveryChar(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	edit := newEditControl(t, "edit-char")
	focusEdit(t, edit)
	inj := Injector{}

	for _, c := range []struct {
		name string
		r    rune
		want string // 与 string(r) 不同: 多行 EDIT 会把单个 \n 规范化成 \r\n
		send func(rune) bool
	}{
		{"SendInput/ASCII", 'a', "a", sendCharUnitsViaInput},
		{"SendInput/汉字", '中', "中", sendCharUnitsViaInput},
		{"SendInput/全角!(FF01)", '！', "！", sendCharUnitsViaInput},
		{"SendInput/全角:(FF1A)", '：', "：", sendCharUnitsViaInput},
		{"SendInput/emoji", '😀', "😀", sendCharUnitsViaInput},
		{"WM_CHAR/ASCII", 'b', "b", sendCharUnitsViaWMChar},
		{"WM_CHAR/全角!", '！', "！", sendCharUnitsViaWMChar},
		{"SendRune/全角!(经 WM_CHAR)", '！', "！", inj.SendRune},
		{"SendText/Tab", '\t', "\t", inj.SendText},
		// 换行刻意不在这一组里: WM_CHAR 的 \n 只要那一次没取到焦点子窗口就会退化成
		// SendInput, 而 EDIT 对 0x0A 的处理与 \r 不同, 落点于是时有时无。它既不稳定,
		// 也不是这组要钉的对象(要钉的是全角标点), 观测记录见
		// docs/invariants.md「注入通道的进程内读回（2026-10，从"未实测"变成实测结论）」
	} {
		if got := injectAndWait(t, edit, string(c.r), c.send); got != c.want {
			t.Errorf("%s 读回 %q, want %q", c.name, got, c.want)
		}
	}
}

// TestFullwidthPunctuationSurvivesSendInput 钉住 invariants.md 曾标为"未实测"的那条。
// 文档描述的症状是**序列级**的(标点重复、后续字符被吞), 逐字清空着注入看不出来,
// 所以这里整串连打再逐字节比对
func TestFullwidthPunctuationSurvivesSendInput(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	edit := newEditControl(t, "edit-fullwidth")
	focusEdit(t, edit)
	inj := Injector{}

	texts := []string{
		"你好！世界：abc", // 汉字与全角标点交替
		"！？：；，。",    // 纯全角标点连排
		"ＡＢＣ１２３",    // 全角拉丁与全角数字
		"ab！cd：ef",  // ASCII 中间夹全角
		"测试！",       // 全角标点结尾
		"😀！🎉：ok",    // emoji 与全角混合
	}
	channels := []struct {
		name string
		send func(rune) bool
	}{
		{"SendInput(退化路径)", sendCharUnitsViaInput},
		{"WM_CHAR(文本层)", sendCharUnitsViaWMChar},
		{"SendRune(产品入口)", inj.SendRune},
	}

	for _, text := range texts {
		for _, ch := range channels {
			if got := injectAndWait(t, edit, text, ch.send); got != text {
				t.Errorf("%s 注入 %q:\n  读回 %q", ch.name, text, got)
			}
		}
	}
}

// TestInjectReadbackDetectsDuplication 阳性对照: 这套读回必须认得出"重复"与"吞字",
// 否则上面两条通过只说明文本恰好相等, 证明不了比对本身有视力。
// 判据是长度与内容双重 —— 系统若真的重复全角标点, 这里会立刻红
func TestInjectReadbackDetectsDuplication(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	edit := newEditControl(t, "edit-dup")
	focusEdit(t, edit)

	const unit = "！："
	for _, r := range unit + unit {
		if !sendCharUnitsViaInput(r) {
			t.Fatalf("注入 %U 被拒", r)
		}
		pumpInput(5 * time.Millisecond)
	}
	start := time.Now()
	var got string
	for time.Now().Before(start.Add(3 * time.Second)) {
		pumpInput(20 * time.Millisecond)
		if got = editText(edit); len([]rune(got)) >= 4 {
			break
		}
	}
	want := strings.Repeat(unit, 2)
	if got != want {
		t.Errorf("连打两次 %q: 读回 %q, want %q", unit, got, want)
	}
}
