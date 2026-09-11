//go:build windows && (amd64 || arm64)

package main

import (
	"bytes"
	"syscall"
	"testing"
	"unsafe"
)

func TestUtf16Units(t *testing.T) {
	cases := []struct {
		r    rune
		want []uint16
	}{
		{'A', []uint16{0x41}},
		{'中', []uint16{0x4E2D}},
		{'\uFF01', []uint16{0xFF01}},         // 全角 !
		{0x1F600, []uint16{0xD83D, 0xDE00}},  // 😀 代理对
		{0x10000, []uint16{0xD800, 0xDC00}},  // U+10000 下界
		{0x10FFFF, []uint16{0xDBFF, 0xDFFF}}, // U+10FFFF 上界
		{0xFFFF, []uint16{0xFFFF}},
	}
	for _, c := range cases {
		got := utf16Units(c.r)
		if len(got) != len(c.want) {
			t.Errorf("utf16Units(%U) len = %d, want %d", c.r, len(got), len(c.want))
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("utf16Units(%U)[%d] = %#x, want %#x", c.r, i, got[i], c.want[i])
			}
		}
	}
}

func TestContainsNonASCII(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"hello", false},
		{"hello world\n", false},
		{"h\xe9llo", true},
		{"中文", true},
		{"123", false},
		{"", false},
		{"\t\r\n", false},
		{"ab\xf0\x9f\x98\x80", true}, // ab😀
	}
	for _, c := range cases {
		if got := containsNonASCII(c.s); got != c.want {
			t.Errorf("containsNonASCII(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestIsCJKPunct(t *testing.T) {
	cases := []struct {
		r    rune
		want bool
	}{
		{'\u3001', true}, // 、
		{'\u3002', true}, // 。
		{'\uFF01', true}, // ！
		{'\uFF1F', true}, // ？
		{'\u201C', true}, // “
		{'\u201D', true}, // ”
		{'中', false},
		{'a', false},
		{'1', false},
		{'.', false},
	}
	for _, c := range cases {
		if got := isCJKPunct(c.r); got != c.want {
			t.Errorf("isCJKPunct(%U) = %v, want %v", c.r, got, c.want)
		}
	}
}

// TestClipboardSnapshotRoundtrip 验证快照/恢复保留全部格式(文本 + 注册格式),
// 使用真实系统剪贴板: 先保存原状态, 测试结束还原
func TestClipboardSnapshotRoundtrip(t *testing.T) {
	cb := win32Clipboard{}
	svc := newTypingService(win32Injector{}, cb, win32Foreground{})
	orig := cb.Snapshot()
	if orig == nil {
		t.Skip("剪贴板被占用, 无法保存原始状态")
	}
	defer cb.RestoreSnapshotRaw(orig)

	if !cb.SetText("快照测试文本") {
		t.Fatal("SetText 失败")
	}
	name, _ := syscall.UTF16PtrFromString("Type::UnitTest")
	reg, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(name)))
	if reg == 0 {
		t.Fatal("RegisterClipboardFormatW 失败")
	}
	marker := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	if !openClipboardWithRetry() {
		t.Fatal("打开剪贴板失败")
	}
	writeClipboardFormats([]ClipboardFormat{{Fmt: uint32(reg), Data: marker}})
	procCloseClipboard.Call()

	snap := cb.Snapshot()
	if snap == nil {
		t.Fatal("Snapshot 返回 nil")
	}
	// 注: 系统会为 CF_UNICODETEXT 自动合成 CF_TEXT/CF_OEMTEXT/CF_LOCALE 并一并
	// 枚举, 故快照格式数 >= 2, 此处只验证两种关键格式均被捕获
	var sawText, sawReg bool
	for _, cf := range snap {
		switch cf.Fmt {
		case CF_UNICODETEXT:
			sawText = true
		case uint32(reg):
			sawReg = true
		}
	}
	if !sawText || !sawReg {
		t.Fatalf("快照缺少格式: text=%v reg=%v (共 %d 个格式)", sawText, sawReg, len(snap))
	}

	// 覆盖破坏后恢复 (剪贴板当前文本即注入文本, 守卫应放行)
	if !cb.SetText("覆盖后的内容") {
		t.Fatal("覆盖剪贴板失败")
	}
	svc.restoreClipboardSnapshot(snap, "覆盖后的内容")

	if got := cb.GetText(); got != "快照测试文本" {
		t.Errorf("恢复后文本 = %q, want %q", got, "快照测试文本")
	}
	if !openClipboardWithRetry() {
		t.Fatal("打开剪贴板失败")
	}
	got, ok := readClipboardFormat(uint32(reg))
	procCloseClipboard.Call()
	if !ok || !bytes.Equal(got, marker) {
		t.Errorf("注册格式恢复失败: ok=%v got=%v", ok, got)
	}
}

// TestEncodedText 剪贴板文本的字节表示: UTF-16LE + 结尾 NUL。
// 内嵌 NUL 也必须原样保留——它是 holdsText 按字节比对的基础
func TestEncodedText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []byte
	}{
		{"空串", "", []byte{0x00, 0x00}},
		{"ASCII", "A", []byte{0x41, 0x00, 0x00, 0x00}},
		{"CJK", "中", []byte{0x2D, 0x4E, 0x00, 0x00}},
		{"内嵌 NUL", "A\x00B", []byte{0x41, 0x00, 0x00, 0x00, 0x42, 0x00, 0x00, 0x00}},
	}
	for _, c := range cases {
		got := encodedText(c.in)
		if !bytes.Equal(got, c.want) {
			t.Errorf("%s: encodedText(%q) = % x, want % x", c.name, c.in, got, c.want)
		}
		// 结尾 NUL 终止符必须落在最后, 表示块本身不含填充
		if n := len(got); n < 2 || got[n-1] != 0 || got[n-2] != 0 {
			t.Errorf("%s: 末尾缺少 NUL 终止符: % x", c.name, got)
		}
	}
}

// TestClipboardHoldsTextNul 内嵌 NUL 的文本必须被 holdsText 精确识别。
// 旧实现用 GetText 的字符串比较, 会在 NUL 处截断而误判为"用户已改动", 跳过恢复
func TestClipboardHoldsTextNul(t *testing.T) {
	cb := win32Clipboard{}
	orig := cb.Snapshot()
	if orig == nil {
		t.Skip("剪贴板被占用, 无法保存原始状态")
	}
	defer cb.RestoreSnapshotRaw(orig)

	const text = "A\x00B"
	if !cb.SetText(text) {
		t.Fatal("SetText 失败")
	}
	if !cb.HoldsText(text) {
		t.Errorf("HoldsText(%q) = false, 内嵌 NUL 被误判为用户改动", text)
	}
	if cb.HoldsText("A") {
		t.Error(`HoldsText("A") = true: 截断后的比较不应通过`)
	}
}

// TestGuardSingleInstance 单实例守卫: 同一进程内第二次守卫必须被拦下。
// 互斥体名字带 PID, 因此同一测试的第二次调用就是"第二个实例"的场景,
// 又不会与真正在运行的 Type 相互干扰
func TestGuardSingleInstance(t *testing.T) {
	if !guardSingleInstance() {
		t.Fatal("首次守卫应放行(尚无同名互斥体)")
	}
	if guardSingleInstance() {
		t.Error("第二次守卫应被拦下, 否则单实例形同虚设")
	}
	if instanceMutex == 0 {
		t.Error("守卫放行后应持有互斥体句柄")
	}
}

// TestClipboardRestoreGuard 验证守卫: 用户在注入期间复制了新内容时不覆盖
func TestClipboardRestoreGuard(t *testing.T) {
	cb := win32Clipboard{}
	svc := newTypingService(win32Injector{}, cb, win32Foreground{})
	orig := cb.Snapshot()
	if orig == nil {
		t.Skip("剪贴板被占用, 无法保存原始状态")
	}
	defer cb.RestoreSnapshotRaw(orig)

	if !cb.SetText("旧文本") {
		t.Fatal("SetText 失败")
	}
	snap := cb.Snapshot()
	if snap == nil {
		t.Fatal("Snapshot 返回 nil")
	}

	// 模拟注入后用户又复制了新内容: 剪贴板文本 != 注入文本, 恢复应跳过
	if !cb.SetText("用户新复制的内容") {
		t.Fatal("SetText 失败")
	}
	svc.restoreClipboardSnapshot(snap, "注入文本")

	if got := cb.GetText(); got != "用户新复制的内容" {
		t.Errorf("守卫未生效, 剪贴板被覆盖为 %q", got)
	}
}
