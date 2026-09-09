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
	orig := clipboardSnapshot()
	if orig == nil {
		t.Skip("剪贴板被占用, 无法保存原始状态")
	}
	defer restoreSnapshotRaw(orig)

	if !clipboardSetText("快照测试文本") {
		t.Fatal("clipboardSetText 失败")
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
	writeClipboardFormats([]clipFormat{{fmt: uint32(reg), data: marker}})
	procCloseClipboard.Call()

	snap := clipboardSnapshot()
	if snap == nil {
		t.Fatal("clipboardSnapshot 返回 nil")
	}
	// 注: 系统会为 CF_UNICODETEXT 自动合成 CF_TEXT/CF_OEMTEXT/CF_LOCALE 并一并
	// 枚举, 故快照格式数 >= 2, 此处只验证两种关键格式均被捕获
	var sawText, sawReg bool
	for _, cf := range snap {
		switch cf.fmt {
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
	if !clipboardSetText("覆盖后的内容") {
		t.Fatal("覆盖剪贴板失败")
	}
	restoreClipboardSnapshot(snap, "覆盖后的内容")

	if got := clipboardGetText(); got != "快照测试文本" {
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

// TestClipboardRestoreGuard 验证守卫: 用户在注入期间复制了新内容时不覆盖
func TestClipboardRestoreGuard(t *testing.T) {
	orig := clipboardSnapshot()
	if orig == nil {
		t.Skip("剪贴板被占用, 无法保存原始状态")
	}
	defer restoreSnapshotRaw(orig)

	if !clipboardSetText("旧文本") {
		t.Fatal("clipboardSetText 失败")
	}
	snap := clipboardSnapshot()
	if snap == nil {
		t.Fatal("clipboardSnapshot 返回 nil")
	}

	// 模拟注入后用户又复制了新内容: 剪贴板文本 != 注入文本, 恢复应跳过
	if !clipboardSetText("用户新复制的内容") {
		t.Fatal("clipboardSetText 失败")
	}
	restoreClipboardSnapshot(snap, "注入文本")

	if got := clipboardGetText(); got != "用户新复制的内容" {
		t.Errorf("守卫未生效, 剪贴板被覆盖为 %q", got)
	}
}
