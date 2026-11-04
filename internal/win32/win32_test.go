//go:build windows && (amd64 || arm64)

package win32

import (
	"bytes"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/LeuJasYoh/type/internal/typing"
)

// clipboardExclusive 确认剪贴板此刻归我们: 写一段探针文本并立刻读回, 最多试 3 次。
//
// 真剪贴板用例的共同前提是"这段时间里没有别人动剪贴板": 剪贴板桥接/同步类常驻程序
// 会让系统剪贴板持续被别人持有(本机实测连 PowerShell 的 Set-Clipboard 都 5/5 失败,
// `SetClipboardData` 可能"成功"、紧接着读回却是对方的字节), 那种环境里断言往返结果
// 只会得到一个假的"回归"。
//
// 前提不成立时**本机**跳过并写明读到了什么, 不改断言放水; **CI 里一律红** —— runner
// 是干净环境, 那里的"剪贴板不可用"要么是真回归(这两条是全仓唯一真实的剪贴板读写
// 往返), 要么是环境真坏了, 两种都该让人看见, 不能被一个 SKIP 掩盖过去
func clipboardExclusive(t *testing.T, cb Clipboard, snap []typing.ClipboardFormat, why string) {
	t.Helper()
	const probe = "Type::ClipboardProbe"
	deadline := time.Now().Add(2 * time.Second)
	fail := func(got string) {
		if os.Getenv("CI") != "" {
			t.Fatalf("CI 环境里剪贴板不可用(写入 %q 后读回 %q): %s —— 不许跳过, 先查是写读链路回归还是环境",
				probe, got, why)
		}
		t.Skipf("剪贴板被外部程序占用/改写(写入 %q 后读回 %q): %s —— 前提不成立, 跳过而不是报假回归",
			probe, got, why)
	}
	for attempt := 1; ; attempt++ {
		if written, _ := cb.SetText(probe, snap); written {
			if got := cb.GetText(); got == probe {
				return // 前提成立
			} else if attempt >= 3 || time.Now().After(deadline) {
				fail(got)
				return
			}
		} else if attempt >= 3 || time.Now().After(deadline) {
			fail("<SetText 失败>")
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// skipOrFailInCI 环境前提不成立时的统一口径: 本机跳过并写明原因, CI 里必须红。
// runner 是干净环境, 那里的"环境不成立"要么是真回归、要么是环境真坏了, 都不能被
// 一个 SKIP 掩盖。真剪贴板用例与注入读回用例共用这一条。
func skipOrFailInCI(t *testing.T, why string) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Fatalf("CI 环境里环境前提不成立: %s —— 不许跳过, 先查是回归还是环境", why)
	}
	t.Skipf("%s —— 前提不成立, 跳过而不是报假回归", why)
}

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

// TestClipboardSnapshotRoundtrip 验证快照/恢复保留全部格式(文本 + 注册格式),
// 使用真实系统剪贴板: 先保存原状态, 测试结束还原。
func TestClipboardSnapshotRoundtrip(t *testing.T) {
	cb := Clipboard{}
	orig := cb.Snapshot()
	if orig.UnsafeToRestore() {
		skipOrFailInCI(t, "剪贴板被占用或读不全, 无法保存原始状态")
	}
	defer cb.RestoreSnapshotRaw(orig.Formats)
	clipboardExclusive(t, cb, orig.Formats, "快照/恢复往返")

	if written, _ := cb.SetText("快照测试文本", orig.Formats); !written {
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
	written := writeClipboardFormats([]typing.ClipboardFormat{{Fmt: uint32(reg), Data: marker}})
	markerReadback, markerOK := readClipboardFormat(uint32(reg))
	procCloseClipboard.Call()
	if !written || !markerOK || !bytes.Equal(markerReadback, marker) {
		t.Fatalf("注册格式写入或读回失败: written=%v readable=%v", written, markerOK)
	}

	snap := cb.Snapshot()
	if snap.UnsafeToRestore() {
		t.Fatal("快照不可用")
	}
	// 注: 系统会为 CF_UNICODETEXT 自动合成 CF_TEXT/CF_OEMTEXT/CF_LOCALE 并一并
	// 枚举, 故快照格式数 >= 2, 此处只验证两种关键格式均被捕获
	var sawText, sawReg bool
	for _, cf := range snap.Formats {
		switch cf.Fmt {
		case cfUnicodeText:
			sawText = true
		case uint32(reg):
			sawReg = true
		}
	}
	if !sawText || !sawReg {
		t.Fatalf("快照缺少格式: text=%v reg=%v (共 %d 个格式)", sawText, sawReg, len(snap.Formats))
	}

	// 覆盖破坏后恢复
	if written, _ := cb.SetText("覆盖后的内容", snap.Formats); !written {
		t.Fatal("覆盖剪贴板失败")
	}
	if !cb.RestoreSnapshot(snap.Formats, "覆盖后的内容") {
		t.Fatal("RestoreSnapshot 失败")
	}

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

// TestClipboardHoldsTextNul 旧实现用 GetText 的字符串比较, 会在 NUL 处截断而误判。
func TestClipboardHoldsTextNul(t *testing.T) {
	cb := Clipboard{}
	orig := cb.Snapshot()
	if orig.UnsafeToRestore() {
		skipOrFailInCI(t, "剪贴板被占用或读不全, 无法保存原始状态")
	}
	defer cb.RestoreSnapshotRaw(orig.Formats)
	clipboardExclusive(t, cb, orig.Formats, "内嵌 NUL 的恢复守卫")

	const text = "A\x00B"
	const restored = "恢复后的内容"
	snap := []typing.ClipboardFormat{{Fmt: cfUnicodeText, Data: encodedText(restored)}}
	if written, _ := cb.SetText(text, orig.Formats); !written {
		t.Fatal("SetText 失败")
	}
	if !cb.RestoreSnapshot(snap, "A") {
		t.Fatal("非完整前缀应跳过恢复并返回 true")
	}
	if !openClipboardWithRetry() {
		t.Fatal("打开剪贴板失败")
	}
	got, ok := readClipboardFormat(cfUnicodeText)
	procCloseClipboard.Call()
	if !ok || !bytes.Equal(got, encodedText(text)) {
		t.Fatalf("非完整前缀触发了恢复: ok=%v got=% x", ok, got)
	}
	if !cb.RestoreSnapshot(snap, text) {
		t.Fatal("完整 NUL 文本应允许恢复")
	}
	if got := cb.GetText(); got != restored {
		t.Errorf("恢复后文本 = %q, want %q", got, restored)
	}
}

type clipboardProcFunc func(...uintptr) uintptr

func (f clipboardProcFunc) Call(args ...uintptr) (uintptr, uintptr, error) {
	return f(args...), 0, syscall.Errno(0)
}

type clipboardFaults struct {
	open, empty, alloc, lock int
	set                      []int
}

type clipboardTransaction struct {
	opened  bool
	calls   []string
	formats map[uint32]uintptr
	memory  map[uintptr]bool
	lock    procCaller
}

func newClipboardTransaction(t *testing.T, current, afterClose []typing.ClipboardFormat, fail clipboardFaults) *clipboardTransaction {
	t.Helper()
	oldOpen, oldClose, oldEmpty := procOpenClipboard, procCloseClipboard, procEmptyClipboard
	oldSet, oldGet := procSetClipboardData, procGetClipboardData
	oldAlloc, oldLock, oldFree := procGlobalAlloc, procGlobalLock, procGlobalFree
	s := &clipboardTransaction{memory: make(map[uintptr]bool), lock: oldLock}
	t.Cleanup(func() {
		procOpenClipboard, procCloseClipboard, procEmptyClipboard = oldOpen, oldClose, oldEmpty
		procSetClipboardData, procGetClipboardData = oldSet, oldGet
		procGlobalAlloc, procGlobalLock, procGlobalFree = oldAlloc, oldLock, oldFree
		if s.opened {
			t.Error("剪贴板未关闭")
		}
		for h, systemOwned := range s.memory {
			if !systemOwned {
				t.Errorf("调用方内存泄漏: %#x", h)
			}
			if ret, _, err := oldFree.Call(h); ret != 0 {
				t.Errorf("清理内存 %#x 失败: %v", h, err)
			}
		}
	})
	seed := func(formats []typing.ClipboardFormat) map[uint32]uintptr {
		out := make(map[uint32]uintptr)
		for _, cf := range formats {
			var h uintptr
			if len(cf.Data) == 0 {
				h, _, _ = oldAlloc.Call(ghnd, 0)
			} else {
				h = prepareClipboardData(cf.Data)
			}
			if h == 0 {
				t.Fatal("构造模拟剪贴板内存失败")
			}
			out[cf.Fmt], s.memory[h] = h, true
		}
		return out
	}
	s.formats = seed(current)
	var next map[uint32]uintptr
	if afterClose != nil {
		next = seed(afterClose)
	}
	clearFormats := func() {
		for format, h := range s.formats {
			if ret, _, err := oldFree.Call(h); ret != 0 {
				t.Fatalf("模拟系统释放内存 %#x 失败: %v", h, err)
			}
			delete(s.memory, h)
			delete(s.formats, format)
		}
	}
	requireOpen := func(name string) {
		t.Helper()
		s.calls = append(s.calls, name)
		if !s.opened {
			t.Fatalf("%s 在剪贴板未打开时调用", name)
		}
	}
	var opens, empties, sets, allocs, locks int
	procOpenClipboard = clipboardProcFunc(func(...uintptr) uintptr {
		s.calls = append(s.calls, "open")
		if s.opened {
			t.Fatal("剪贴板重复打开")
		}
		opens++
		if opens <= fail.open {
			return 0
		}
		s.opened = true
		return 1
	})
	procCloseClipboard = clipboardProcFunc(func(...uintptr) uintptr {
		requireOpen("close")
		s.opened = false
		if next != nil {
			clearFormats()
			s.formats, next = next, nil
		}
		return 1
	})
	procEmptyClipboard = clipboardProcFunc(func(...uintptr) uintptr {
		requireOpen("empty")
		empties++
		if empties == fail.empty {
			return 0
		}
		clearFormats()
		return 1
	})
	procSetClipboardData = clipboardProcFunc(func(args ...uintptr) uintptr {
		requireOpen("set")
		format, h := uint32(args[0]), args[1]
		if systemOwned, live := s.memory[h]; !live || systemOwned {
			t.Fatalf("SetClipboardData 未收到调用方持有的内存: %#x", h)
		}
		sets++
		for _, n := range fail.set {
			if sets == n {
				return 0
			}
		}
		if s.formats[format] != 0 {
			t.Fatalf("格式 %#x 未先清空", format)
		}
		s.formats[format], s.memory[h] = h, true
		return h
	})
	procGetClipboardData = clipboardProcFunc(func(args ...uintptr) uintptr {
		requireOpen("get")
		return s.formats[uint32(args[0])]
	})
	procGlobalAlloc = clipboardProcFunc(func(args ...uintptr) uintptr {
		s.calls = append(s.calls, "alloc")
		allocs++
		if allocs == fail.alloc {
			return 0
		}
		h, _, err := oldAlloc.Call(args...)
		if h == 0 {
			t.Fatalf("GlobalAlloc 失败: %v", err)
		}
		s.memory[h] = false
		return h
	})
	procGlobalLock = clipboardProcFunc(func(args ...uintptr) uintptr {
		s.calls = append(s.calls, "lock")
		locks++
		if locks == fail.lock {
			return 0
		}
		ptr, _, err := oldLock.Call(args...)
		if ptr == 0 {
			t.Fatalf("GlobalLock 失败: %v", err)
		}
		return ptr
	})
	procGlobalFree = clipboardProcFunc(func(args ...uintptr) uintptr {
		s.calls = append(s.calls, "free")
		h := args[0]
		if systemOwned, live := s.memory[h]; !live || systemOwned {
			t.Fatalf("释放了系统持有或已释放的内存: %#x", h)
		}
		ret, _, err := oldFree.Call(args...)
		if ret != 0 {
			t.Fatalf("GlobalFree 失败: %v", err)
		}
		delete(s.memory, h)
		return ret
	})
	return s
}

func (s *clipboardTransaction) check(t *testing.T, want []typing.ClipboardFormat, calls string) {
	t.Helper()
	if got := strings.Join(s.calls, " "); got != calls {
		t.Errorf("调用顺序 = %q, want %q", got, calls)
	}
	if len(s.formats) != len(want) {
		t.Errorf("格式数 = %d, want %d", len(s.formats), len(want))
	}
	for _, cf := range want {
		h := s.formats[cf.Fmt]
		if h == 0 {
			t.Errorf("缺少格式 %#x", cf.Fmt)
			continue
		}
		size, _, _ := procGlobalSize.Call(h)
		got := make([]byte, int(size))
		if size != 0 {
			ptr, _, err := s.lock.Call(h)
			if ptr == 0 {
				t.Fatalf("读回模拟内存失败: %v", err)
			}
			procRtlMoveMemory.Call(uintptr(unsafe.Pointer(unsafe.SliceData(got))), ptr, size)
			procGlobalUnlock.Call(h)
		}
		if !bytes.Equal(got, cf.Data) {
			t.Errorf("格式 %#x = % x, want % x", cf.Fmt, got, cf.Data)
		}
	}
}

func TestClipboardWriteTransaction(t *testing.T) {
	const text = "A\x00B"
	original := []typing.ClipboardFormat{
		{Fmt: cfUnicodeText, Data: encodedText("原内容")},
		{Fmt: 0xc001, Data: []byte{0xde, 0xad, 0xbe, 0xef}},
	}
	injected := []typing.ClipboardFormat{{Fmt: cfUnicodeText, Data: encodedText(text)}}
	user := []typing.ClipboardFormat{{Fmt: cfUnicodeText, Data: encodedText("用户新内容")}}
	empty := []typing.ClipboardFormat{}
	cases := []struct {
		name               string
		fail               clipboardFaults
		current, snap      []typing.ClipboardFormat
		afterClose, want   []typing.ClipboardFormat
		written, preserved bool
		calls              string
	}{
		{
			name: "准备分配失败保留原内容", fail: clipboardFaults{alloc: 1},
			current: original, snap: original, want: original, preserved: true,
			calls: "alloc",
		},
		{
			name: "准备锁定失败保留用户新内容", fail: clipboardFaults{lock: 1},
			current: user, snap: original, want: user, preserved: true,
			calls: "alloc lock free",
		},
		{
			name: "打开失败保留用户新内容", fail: clipboardFaults{open: clipboardAttempts},
			current: user, snap: original, want: user, preserved: true,
			calls: "alloc lock open open open open free",
		},
		{
			name: "首次清空失败保留用户新内容", fail: clipboardFaults{empty: 1},
			current: user, snap: original, want: user, preserved: true,
			calls: "alloc lock open empty close free",
		},
		{
			name: "写入失败同次打开回滚全格式", fail: clipboardFaults{set: []int{1}},
			current: original, snap: original, want: original, preserved: true,
			calls: "alloc lock open empty set empty alloc lock set alloc lock set close free",
		},
		{
			name: "写入失败回滚空快照", fail: clipboardFaults{set: []int{1}},
			current: empty, snap: empty, want: empty, preserved: true,
			calls: "alloc lock open empty set empty close free",
		},
		{
			name: "写入失败无快照不可回滚", fail: clipboardFaults{set: []int{1}},
			current: original, snap: nil, want: empty,
			calls: "alloc lock open empty set close free",
		},
		{
			name: "回滚清空失败", fail: clipboardFaults{set: []int{1}, empty: 2},
			current: original, snap: original, want: empty,
			calls: "alloc lock open empty set empty close free",
		},
		{
			name: "回滚分配失败继续写其余格式", fail: clipboardFaults{set: []int{1}, alloc: 2},
			current: original, snap: original, want: original[1:],
			calls: "alloc lock open empty set empty alloc alloc lock set close free",
		},
		{
			name: "回滚锁定失败释放内存", fail: clipboardFaults{set: []int{1}, lock: 2},
			current: original, snap: original, want: original[1:],
			calls: "alloc lock open empty set empty alloc lock free alloc lock set close free",
		},
		{
			name: "回滚首格式写入失败", fail: clipboardFaults{set: []int{1, 2}},
			current: original, snap: original, want: original[1:],
			calls: "alloc lock open empty set empty alloc lock set free alloc lock set close free",
		},
		{
			name: "回滚末格式写入失败", fail: clipboardFaults{set: []int{1, 3}},
			current: original, snap: original, want: original[:1],
			calls: "alloc lock open empty set empty alloc lock set alloc lock set free close free",
		},
		{
			name:    "写入成功转交内存所有权",
			current: original, snap: original, want: injected, written: true, preserved: true,
			calls: "alloc lock open empty set close",
		},
		{
			name: "打开重试后写入成功", fail: clipboardFaults{open: 1},
			current: original, snap: original, want: injected, written: true, preserved: true,
			calls: "alloc lock open open empty set close",
		},
		{
			name: "回滚不覆盖关闭后用户复制的内容", fail: clipboardFaults{set: []int{1}},
			current: original, snap: original, afterClose: user, want: user, preserved: true,
			calls: "alloc lock open empty set empty alloc lock set alloc lock set close free",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newClipboardTransaction(t, c.current, c.afterClose, c.fail)
			written, preserved := (Clipboard{}).SetText(text, c.snap)
			if written != c.written || preserved != c.preserved {
				t.Errorf("SetText = (%v, %v), want (%v, %v)", written, preserved, c.written, c.preserved)
			}
			s.check(t, c.want, c.calls)
		})
	}
}

func TestClipboardRestoreTransaction(t *testing.T) {
	const text = "A\x00B"
	original := []typing.ClipboardFormat{
		{Fmt: cfUnicodeText, Data: encodedText("原内容")},
		{Fmt: 0xc001, Data: []byte{0xde, 0xad, 0xbe, 0xef}},
	}
	injected := []typing.ClipboardFormat{{Fmt: cfUnicodeText, Data: encodedText(text)}}
	user := []typing.ClipboardFormat{{Fmt: cfUnicodeText, Data: encodedText("用户新内容")}}
	zeroSize := []typing.ClipboardFormat{{Fmt: cfUnicodeText, Data: []byte{}}}
	empty := []typing.ClipboardFormat{}
	cases := []struct {
		name             string
		fail             clipboardFaults
		current, snap    []typing.ClipboardFormat
		afterClose, want []typing.ClipboardFormat
		injected         string
		ok               bool
		calls            string
	}{
		{
			name:    "未改动的完整NUL文本恢复全格式",
			current: injected, snap: original, want: original, ok: true,
			calls: "open get lock empty alloc lock set alloc lock set close",
		},
		{
			name:    "用户改动跳过恢复",
			current: user, snap: original, want: user, ok: true,
			calls: "open get lock close",
		},
		{
			name: "非完整NUL前缀跳过恢复", injected: "A",
			current: injected, snap: original, want: injected, ok: true,
			calls: "open get lock close",
		},
		{
			name:    "用户移除文本格式跳过恢复",
			current: original[1:], snap: original, want: original[1:], ok: true,
			calls: "open get close",
		},
		{
			name: "读取锁定失败不能视为用户改动", fail: clipboardFaults{lock: 1},
			current: injected, snap: original, want: injected,
			calls: "open get lock close",
		},
		{
			name:    "零大小内存不能视为用户改动",
			current: zeroSize, snap: original, want: zeroSize,
			calls: "open get close",
		},
		{
			name:    "nil快照不打开剪贴板",
			current: injected, snap: nil, want: injected,
		},
		{
			name:    "空快照恢复为空",
			current: injected, snap: empty, want: empty, ok: true,
			calls: "open get lock empty close",
		},
		{
			name:    "空快照仍保护用户新内容",
			current: user, snap: empty, want: user, ok: true,
			calls: "open get lock close",
		},
		{
			name: "空快照使用恢复档最后一次重试", fail: clipboardFaults{open: clipboardRestoreTries - 1},
			current: injected, snap: empty, want: empty, ok: true,
			calls: strings.Repeat("open ", clipboardRestoreTries) + "get lock empty close",
		},
		{
			name: "恢复档打开失败", fail: clipboardFaults{open: clipboardRestoreTries},
			current: injected, snap: original, want: injected,
			calls: strings.TrimSpace(strings.Repeat("open ", clipboardRestoreTries)),
		},
		{
			name: "清空失败保留注入内容", fail: clipboardFaults{empty: 1},
			current: injected, snap: original, want: injected,
			calls: "open get lock empty close",
		},
		{
			name: "写回分配失败继续其余格式", fail: clipboardFaults{alloc: 1},
			current: injected, snap: original, want: original[1:],
			calls: "open get lock empty alloc alloc lock set close",
		},
		{
			name: "写回锁定失败释放内存", fail: clipboardFaults{lock: 2},
			current: injected, snap: original, want: original[1:],
			calls: "open get lock empty alloc lock free alloc lock set close",
		},
		{
			name: "写回设置失败释放内存", fail: clipboardFaults{set: []int{1}},
			current: injected, snap: original, want: original[1:],
			calls: "open get lock empty alloc lock set free alloc lock set close",
		},
		{
			name: "写回全部失败", fail: clipboardFaults{set: []int{1, 2}},
			current: injected, snap: original, want: empty,
			calls: "open get lock empty alloc lock set free alloc lock set free close",
		},
		{
			name:    "检查与恢复之间不释放剪贴板",
			current: injected, snap: original, afterClose: user, want: user, ok: true,
			calls: "open get lock empty alloc lock set alloc lock set close",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newClipboardTransaction(t, c.current, c.afterClose, c.fail)
			injectedText := text
			if c.injected != "" {
				injectedText = c.injected
			}
			if got := (Clipboard{}).RestoreSnapshot(c.snap, injectedText); got != c.ok {
				t.Errorf("RestoreSnapshot = %v, want %v", got, c.ok)
			}
			s.check(t, c.want, c.calls)
		})
	}
}

// TestSkippableFormat 快照该跳过哪些格式。判据是"这块数据是不是普通内存块":
// 句柄型与含句柄的格式照抄下来, 恢复时写回的是失效句柄或垃圾字节, 受害的是
// 系统里别的程序; 而普通内存块格式(文本/图片/文件/注册格式/程序私有格式)
// 必须照抄 —— 多跳一个就是凭空丢内容
func TestSkippableFormat(t *testing.T) {
	skip := []struct {
		name string
		fmt  uint32
	}{
		{"CF_BITMAP", cfBitmap},
		{"CF_PALETTE", cfPalette},
		{"CF_ENHMETAFILE", cfEnhMetafile},
		{"CF_METAFILEPICT(块内含图形句柄)", cfMetafilePict},
		{"CF_OWNERDISPLAY", cfOwnerDisplay},
		{"CF_DSPTEXT", cfDspText},
		{"CF_DSPBITMAP", cfDspBitmap},
		{"CF_DSPMETAFILEPICT", cfDspMetafilePict},
		{"CF_DSPENHMETAFILE", cfDspEnhMetafile},
		{"CF_GDIOBJFIRST", cfGdiObjFirst},
		{"GDI 对象族中间值", 0x0350},
		{"CF_GDIOBJLAST", cfGdiObjLast},
	}
	for _, c := range skip {
		if !skippableFormat(c.fmt) {
			t.Errorf("%s (%#x) 应被跳过", c.name, c.fmt)
		}
	}

	keep := []struct {
		name string
		fmt  uint32
	}{
		{"CF_UNICODETEXT", cfUnicodeText},
		{"CF_TEXT", 1},
		{"CF_OEMTEXT", 7},
		{"CF_DIB", 8},
		{"CF_WAVE", 12},
		{"CF_HDROP", 15},
		{"CF_DIBV5", 17},
		{"CF_PRIVATEFIRST", cfPrivateFirst},
		{"CF_PRIVATELAST", cfPrivateLast},
		{"GDI 族下界前一个", cfGdiObjFirst - 1},
		{"GDI 族上界后一个", cfGdiObjLast + 1},
	}
	for _, c := range keep {
		if skippableFormat(c.fmt) {
			t.Errorf("%s (%#x) 是普通内存块格式, 不该被跳过", c.name, c.fmt)
		}
	}
}

// ─── 窗口类图标索引(仅测试用到的 Win32 入口) ──────────

var (
	procRegisterClassExW  = user32.NewProc("RegisterClassExW")
	procCreateWindowExW   = user32.NewProc("CreateWindowExW")
	procDefWindowProcW    = user32.NewProc("DefWindowProcW")
	procGetClassLongPtrW  = user32.NewProc("GetClassLongPtrW")
	procSetWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")
	procLoadIconW         = user32.NewProc("LoadIconW")
	procDestroyWindow     = user32.NewProc("DestroyWindow")
	procUnregisterClassW  = user32.NewProc("UnregisterClassW")
	procGetModuleHandleW  = kernel32.NewProc("GetModuleHandleW")
)

// WNDCLASSEXW 仅测试用: 注册一个窗口类, 作为类图标索引的写入对象
type WNDCLASSEXW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

// testWindow 测试用的真实窗口: 注册一个临时窗口类并建窗, 清理随测试结束
// 自动执行(先销毁窗口再注销类, t.Cleanup 的后进先出顺序正好满足)
type testWindow struct {
	className *uint16
	hInst     uintptr
}

// newTestWindow 注册窗口类; tag 用于区分同类测试, 类名随进程与 tag 唯一
func newTestWindow(t *testing.T, tag string) *testWindow {
	t.Helper()
	// **把整个用例锁在当前 OS 线程上**: 建窗与后续发消息必须是同一个线程。
	// Go 协程会在线程间迁移, 一旦迁移, 同步 SendMessage 给"自己刚建的窗口"就会
	// 阻塞等这个线程泵消息, 而这个线程已经回到运行时空闲、永不泵 —— 用例永久卡住。
	// 根治放在这里而不是逐个用例补: 建窗的入口只有这一个
	runtime.LockOSThread()
	wndProc := syscall.NewCallback(func(hwnd, msg, wparam, lparam uintptr) uintptr {
		ret, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
		return ret
	})
	hInst, _, _ := procGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString("TypeTest-" + tag + "-" + strconv.Itoa(os.Getpid()))
	wc := WNDCLASSEXW{
		cbSize:        uint32(unsafe.Sizeof(WNDCLASSEXW{})),
		lpfnWndProc:   wndProc,
		hInstance:     hInst,
		lpszClassName: className,
	}
	if ret, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); ret == 0 {
		t.Fatalf("RegisterClassExW 失败: %v", err)
	}
	t.Cleanup(func() { procUnregisterClassW.Call(uintptr(unsafe.Pointer(className)), hInst) })
	return &testWindow{className: className, hInst: hInst}
}

// create 建出窗口并登记销毁, 返回句柄(样式为 0: 窗口只有系统给的那点边框)
func (w *testWindow) create(t *testing.T, title string) uintptr {
	return w.createStyled(t, title, 0)
}

// createStyled 同上, 但指定窗口样式。"能不能拖大"这类性质必须在一个真的带缩放
// 边框的窗口上才验得出来: dwStyle=0 的窗口本来就没有 WS_THICKFRAME, 拿它断言
// "样式位已被清掉"是恒真的(见 TestWindowSizeIsFixed)
func (w *testWindow) createStyled(t *testing.T, title string, style uintptr) uintptr {
	t.Helper()
	titlePtr, _ := syscall.UTF16PtrFromString(title)
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(w.className)),
		uintptr(unsafe.Pointer(titlePtr)), style, 0, 0, 0, 0, 0, 0, w.hInst, 0)
	if hwnd == 0 {
		t.Fatalf("CreateWindowExW 失败: %v", err)
	}
	t.Cleanup(func() { procDestroyWindow.Call(hwnd) })
	return hwnd
}

// TestWindowTitle 目标窗口标题读取: 建一个标题已知的窗口读回。
// 漂移守卫按窗口标识判定, 但预览与终态展示靠标题, 这条守住 windowTitle
func TestWindowTitle(t *testing.T) {
	const title = "目标窗口标题"
	hwnd := newTestWindow(t, "Title").create(t, title)
	if got := windowTitle(hwnd); got != title {
		t.Errorf("windowTitle = %q, want %q", got, title)
	}
}
