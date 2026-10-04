//go:build windows && (amd64 || arm64)

package win32

import (
	"bytes"
	"math"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/LeuJasYoh/type/internal/typing"
)

// clipboardExclusive 确认剪贴板此刻归我们: 写一段探针文本并立刻读回, 最多试 3 次。
//
// 下面是真剪贴板用例的共同前提 —— "这段时间里没有别人动剪贴板"。某些常驻程序
// (剪贴板桥接/同步类)会让系统剪贴板持续被别人持有: 本机实测连 PowerShell 的
// Set-Clipboard 都 5/5 失败, 而 `SetClipboardData` 可能"成功"、紧接着读回来却是
// 对方的字节。那种环境里断言往返结果只会得到一个假的"回归"。
//
// 前提不成立时, **本机**跳过并说明读到什么, 而不是改断言放水; **CI 里一律红** ——
// runner 是干净环境, 那里出现"剪贴板不可用"要么是真回归(这两条是全仓唯一真实的
// 剪贴板读写往返, 写错字节/读错字节都会走到这里), 要么是环境真坏了, 两种都该让人
// 看见, 不能被一个 SKIP 掩盖过去(2026-10 独立验证提出: 跳过分支会同时吃掉这两类)
func clipboardExclusive(t *testing.T, cb Clipboard, why string) {
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
		if cb.SetText(probe) {
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
// 恢复守卫(剪贴板被用户改动时跳过恢复)属于业务层判断, 用例在 internal/typing
// (fake 剪贴板); 这里只验证真剪贴板的快照与写回本身
func TestClipboardSnapshotRoundtrip(t *testing.T) {
	cb := Clipboard{}
	orig := cb.Snapshot()
	if orig.UnsafeToRestore() {
		t.Skip("剪贴板被占用或读不全, 无法保存原始状态")
	}
	defer cb.RestoreSnapshotRaw(orig.Formats)
	clipboardExclusive(t, cb, "快照/恢复往返")

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
	writeClipboardFormats([]typing.ClipboardFormat{{Fmt: uint32(reg), Data: marker}})
	procCloseClipboard.Call()

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
	if !cb.SetText("覆盖后的内容") {
		t.Fatal("覆盖剪贴板失败")
	}
	restored := cb.RestoreSnapshotRaw(snap.Formats)

	if got := cb.GetText(); got != "快照测试文本" {
		t.Errorf("恢复后文本 = %q, want %q (RestoreSnapshotRaw 返回 %v)", got, "快照测试文本", restored)
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
	cb := Clipboard{}
	orig := cb.Snapshot()
	if orig.UnsafeToRestore() {
		t.Skip("剪贴板被占用或读不全, 无法保存原始状态")
	}
	defer cb.RestoreSnapshotRaw(orig.Formats)
	clipboardExclusive(t, cb, "内嵌 NUL 的 holdsText 比对")

	const text = "A\x00B"
	if !cb.SetText(text) {
		t.Fatal("SetText 失败")
	}
	if holds, known := cb.HoldsText(text); !known || !holds {
		t.Errorf("HoldsText(%q) = (holds=%v, known=%v), 内嵌 NUL 被误判为用户改动", text, holds, known)
	}
	if holds, _ := cb.HoldsText("A"); holds {
		t.Error(`HoldsText("A") 的 holds = true: 截断后的比较不应通过`)
	}
}

// TestClaimInstanceMutex 单实例判定: 同一进程内第二次认领必须被拦下。
// 认领名由调用方传入, 测试用带 PID 的名字: 同名第二次调用即"第二个进程
// 认领同一个名字"的场景, 又不会与真正在运行的 Type 相互干扰
// (生产守卫用的是不带 PID 的固定名, 跨进程互斥全靠名字相同)。
// 这里验证 claimInstanceMutex 而不是 GuardSingleInstance: 后者会弹模态提示框,
// 在无头 CI 上没人点确定, 会让 go test 一直挂到超时(已实际发生过一次)
func TestClaimInstanceMutex(t *testing.T) {
	name := instanceMutexName + "-test-" + strconv.Itoa(os.Getpid())
	if !claimInstanceMutex(name) {
		t.Fatal("首次认领应放行(尚无同名互斥体)")
	}
	if instanceMutex == 0 {
		t.Error("认领成功后应持有互斥体句柄")
	}
	if claimInstanceMutex(name) {
		t.Error("第二次认领应被拦下, 否则单实例形同虚设")
	}
}

// TestMutexAlreadyHeld 认领结果的分类: 哪些返回值算"已有实例占着"。
//
// 这条必须单独测, 因为 ERROR_ACCESS_DENIED 那一支在本机造不出来(要两个不同
// 完整性级别的进程), 而它恰恰是守卫会失效的地方: 该错误码与"根本没建成"
// 共用返回值 0, 语义却相反。判据是文档写明的 CreateMutexW 语义 ——
// 名字已存在且有权打开时报 ERROR_ALREADY_EXISTS 并返回句柄; 名字已存在但
// 无权打开时报 ERROR_ACCESS_DENIED 并返回 NULL
func TestMutexAlreadyHeld(t *testing.T) {
	const someHandle = uintptr(0x1234)
	cases := []struct {
		name    string
		h       uintptr
		callErr error
		want    bool
	}{
		{"有权打开已存在的同名对象", someHandle, syscall.Errno(errorAlreadyExists), true},
		{"无权打开已存在的同名对象(提权实例在先)", 0, syscall.Errno(errorAccessDenied), true},
		{"新建成功", someHandle, syscall.Errno(0), false},
		{"创建失败但不是已存在(如命名空间受限)", 0, syscall.Errno(errorAccessDenied + 1000), false},
		{"无错误信息但拿到句柄", someHandle, nil, false},
		{"无错误信息且没有句柄", 0, nil, false},
	}
	for _, c := range cases {
		if got := mutexAlreadyHeld(c.h, c.callErr); got != c.want {
			t.Errorf("mutexAlreadyHeld(%#x, %v) = %v, want %v (%s)", c.h, c.callErr, got, c.want, c.name)
		}
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

// TestApplyWindowIconClassIndex 类图标索引必须真能被系统接受。
// nIndex 是 WNDCLASSEX 内部字段的负字节偏移, 文档值 GCLP_HICON = -14 /
// GCLP_HICONSM = -34, 代码里以按位取反表达(^uintptr(13) = -14)。
// 值写错时 SetClassLongPtr 以 ERROR_INVALID_INDEX 返回 0 且什么都不做,
// 返回值又没人采信 —— 图标重设会静默空转。这里真的建一个窗口跑一遍
// applyWindowIcon, 再按文档索引把图标读回来核对: 索引一旦写错即刻变红
func TestApplyWindowIconClassIndex(t *testing.T) {
	hwnd := newTestWindow(t, "IconIndex").create(t, "TypeIconIndexTest")

	icon, _, _ := procLoadIconW.Call(0, 32512) // IDI_APPLICATION
	if icon == 0 {
		t.Fatal("LoadIconW 失败")
	}

	// false: 只走类图标路径, 不碰 WM_SETICON(避免与窗口图标语义纠缠)
	applyWindowIcon(hwnd, icon, icon, false)

	for _, c := range []struct {
		name  string
		index uintptr
	}{
		{"GCLP_HICON(-14)", ^uintptr(13)},
		{"GCLP_HICONSM(-34)", ^uintptr(33)},
	} {
		got, _, _ := procGetClassLongPtrW.Call(hwnd, c.index)
		if got != icon {
			t.Errorf("%s 未被接受: 读回 0x%X, want 0x%X —— 索引值写错了?",
				c.name, got, icon)
		}
	}
}

// TestScaledByDPIRoundsToNearest 逻辑尺寸 → 物理像素的换算必须四舍五入。
//
// 曾经的实现是 `w * dpi / 96`(整数除法, 向下截断): 在 125% 这类非整数倍缩放
// 下会稳定地少一个物理像素(1024 @ 120dpi → 1023)。客户区尺寸同时决定
// WebView2 的渲染表面大小, 表面比客户区小 1 像素就要重采样, 表现为文字发虚。
// 这里把 125%/150% 两档的"截断会给错值"钉死, 换回截断即刻变红
func TestScaledByDPIRoundsToNearest(t *testing.T) {
	cases := []struct {
		name      string
		w, h, dpi int
		wantW     int
		wantH     int
		note      string
	}{
		{"100%", 540, 480, 96, 540, 480, ""},
		{"125% 整除档", 1024, 640, 120, 1280, 800, ""},
		{"125% 半像素进位", 1000, 601, 120, 1250, 751, "601*120/96 = 751.25; 截断会给 750"},
		{"150%", 540, 480, 144, 810, 720, ""},
		{"175%", 540, 480, 168, 945, 840, ""},
		{"200%", 614, 512, 192, 1228, 1024, ""},
		{"畸形 DPI(低于 96)按 96 处理", 540, 480, 0, 540, 480, ""},
	}
	for _, c := range cases {
		w, h := scaledByDPI(c.w, c.h, c.dpi)
		if w != c.wantW || h != c.wantH {
			t.Errorf("%s: scaledByDPI(%d,%d,%d) = (%d,%d), want (%d,%d) %s",
				c.name, c.w, c.h, c.dpi, w, h, c.wantW, c.wantH, c.note)
		}
	}

	// 单独钉一条"截断与四舍五入会得到不同结果"的输入: 599 @120dpi = 748.75,
	// 四舍五入得 749, 截断得 748。这条保证将来有人换回截断时必然变红
	// (不能只靠上面那张表 —— 表里全是能整除或进位方向一致的样例)
	if got, _ := scaledByDPI(599, 1, 120); got != 749 {
		t.Errorf("599 @120dpi = %d, want 749 (截断会给 748)", got)
	}
}

// TestInitialWindowSize 尺寸策略: 以高度为主(上限 540 是刻意的, 见常量注释),
// 宽度按 6:5 推出, 小屏/workH 偏矮时按工作区收窄
func TestInitialWindowSize(t *testing.T) {
	cases := []struct {
		name  string
		workW int
		workH int
		wantW int
		wantH int
	}{
		// 1920×1040 工作区(1080p 减任务栏): 49% = 509, 宽 509*6/5 = 610
		{"1080p 100%", 1920, 1040, 610, 509},
		// 1520×912 工作区(本机实测: 125% 缩放下的逻辑工作区 1536×912):
		// 49% = 446 → 抬到下限 480, 宽按 480 推 = 576
		{"1536×912 工作区", 1536, 912, 576, 480},
		// 高工作区: 49% 超过上限, 压到 540 → 宽 648
		{"高工作区压上限", 1920, 1174, 648, 540},
		// 超宽屏不会因为宽而变高(只与工作区高度有关)
		{"21:9 超宽", 3440, 1400, 648, 540},
		// 小屏: 49% = 294 → 抬到下限 480, 宽 480*6/5 = 576
		{"1024×600 小屏", 1024, 600, 576, 480},
		// 工作区比下限还矮: 高度被工作区压到 440, 宽度按收窄后的高度推得 528,
		// 再被 MinWindowW(540)抬回来 —— 屏幕矮不代表可以把窗口弄得更窄
		{"工作区偏矮", 1024, 440, 540, 440},
	}
	for _, c := range cases {
		w, h := initialWindowSize(c.workW, c.workH)
		if w != c.wantW || h != c.wantH {
			t.Errorf("%s: initialWindowSize(%d,%d) = (%d,%d), want (%d,%d)",
				c.name, c.workW, c.workH, w, h, c.wantW, c.wantH)
		}
		if w < MinWindowW || w > maxWindowW {
			t.Errorf("%s: 宽度 %d 越界 [%d,%d]", c.name, w, MinWindowW, maxWindowW)
		}
		if h > maxWindowH {
			t.Errorf("%s: 高度 %d 超过上限 %d", c.name, h, maxWindowH)
		}
		if c.workH > MinWindowH && h > c.workH {
			t.Errorf("%s: 高度 %d 超过工作区 %d", c.name, h, c.workH)
		}
	}
}

// TestMonitorWorkArea 真建一个窗口读它的显示器工作区与目标尺寸:
// 这条同时验证 MonitorFromWindow / GetMonitorInfoW / GetDpiForWindow /
// AdjustWindowRect 几个入口真的可用 —— DLL 清单页里名字写错时, syscall 只在
// 首次调用时报错, 不调就永远不知道
func TestMonitorWorkArea(t *testing.T) {
	hwnd := newTestWindow(t, "WorkArea").create(t, "TypeWorkAreaTest")
	work, ok := monitorWorkArea(hwnd)
	if !ok {
		t.Skip("当前环境拿不到显示器工作区(无头会话?), 跳过")
	}
	if work.Right <= work.Left || work.Bottom <= work.Top {
		t.Fatalf("工作区矩形非法: %+v", work)
	}

	plan, haveSize := PlanForDisplay(hwnd)
	if !haveSize {
		t.Fatal("工作区读到了, 尺寸计算却报告失败")
	}
	cw, ch, x, y := plan.ClientW, plan.ClientH, plan.X, plan.Y
	if cw <= 0 || ch <= 0 {
		t.Fatalf("窗口尺寸非法: %dx%d", cw, ch)
	}
	// 位置必须在工作区内, 且整窗不越界
	if x < int(work.Left) || y < int(work.Top) {
		t.Errorf("窗口位置 (%d,%d) 落在工作区 %+v 之外", x, y, work)
	}
	if x+cw > int(work.Right) || y+ch > int(work.Bottom) {
		t.Errorf("窗口 (%d,%d)+%dx%d 越出工作区 %+v", x, y, cw, ch, work)
	}

	// 目标客户区尺寸必须能折回上下限之内: 这是"请求的尺寸落在策略区间里"的
	// 判据, 也是 ClearType 清晰度链条上"表面 == 客户区"的前提
	dpi, _, _ := procGetDpiForWindow.Call(hwnd)
	if dpi < 96 {
		dpi = 96
	}
	lw, lh := int(cw)*96/int(dpi), int(ch)*96/int(dpi)
	if lw < MinWindowW-1 || lw > maxWindowW+1 || lh < MinWindowH-1 {
		t.Errorf("客户区折回逻辑尺寸 = %dx%d, 越出 [%d,%d]x[%d,∞)",
			lw, lh, MinWindowW, maxWindowW, MinWindowH)
	}
	// 带出来的设计逻辑尺寸就是上面折回来的那个值: 内容缩放校正要拿它乘真实比值,
	// 基准错了就会按错的尺寸重设窗口(而界面只是"看起来挤", 不会有任何报错)
	if abs(plan.LogicalW-lw) > 1 || abs(plan.LogicalH-lh) > 1 {
		t.Errorf("PlanForDisplay 的逻辑尺寸 = %dx%d, 客户区折回值 = %dx%d",
			plan.LogicalW, plan.LogicalH, lw, lh)
	}
}

// TestScaledClientSize 内容缩放折算的算术: 客户区物理像素 = 设计逻辑尺寸 × 内容缩放。
// 这几个数是本次故障的核心(某用户机: 窗口 DPI 缩放 1.25, 内容缩放约 1.75), 改成
// 截断、或把乘除与分子分母弄反, 这里必然变红
func TestScaledClientSize(t *testing.T) {
	cases := []struct {
		name  string
		scale float64
		wantW int
		wantH int
	}{
		{"本机 125%", 1.25, 720, 600},
		{"用户机实测约 1.75", 1.75, 1008, 840},
		{"100%", 1, 576, 480},
		{"非整数倍四舍五入", 1.1, 634, 528}, // 576×1.1 = 633.6 → 634
	}
	for _, c := range cases {
		gotW, gotH := scaledClientSize(576, 480, c.scale)
		if gotW != c.wantW || gotH != c.wantH {
			t.Errorf("%s: scaledClientSize(576,480,%.2f) = (%d,%d), want (%d,%d)",
				c.name, c.scale, gotW, gotH, c.wantW, c.wantH)
		}
	}
	// 非法比值一律 (0,0): 调用方据此"不校正", 不许把 NaN/Inf 变成尺寸
	for _, bad := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if w, h := scaledClientSize(576, 480, bad); w != 0 || h != 0 {
			t.Errorf("比值 %v 非法, 却算出了 (%d,%d)", bad, w, h)
		}
	}
	// 上界: 实测 1e15 曾经一路走进窗口矩形的 int32 截断, 让"装不进工作区"的判定失真
	// (returns ok=true 加一个天量客户区), 这条钉住它必须被当成"不校正"
	for _, huge := range []float64{maxContentScale + 1, 1e15, 1e18, 1e300} {
		if w, h := scaledClientSize(576, 480, huge); w != 0 || h != 0 {
			t.Errorf("比值 %v 超出上界, 却算出了 (%d,%d)", huge, w, h)
		}
	}
	// 上界之内仍要照常工作(边界值不能被顺手挡掉)
	if w, h := scaledClientSize(576, 480, maxContentScale); w != 9216 || h != 7680 {
		t.Errorf("上界值 maxContentScale 被挡掉了: (%d,%d), want (9216,7680)", w, h)
	}
}

// TestWindowClientForScale 真建窗口验两条: ① 常规比值给出"设计逻辑尺寸 × 比值"的
// 客户区, 摆上去后读回值逐像素相等; ② 大到装不进工作区的比值必须 ok=false ——
// 调用方据此保持原尺寸、由前端滚动兜底, 不许把窗口摆到屏幕外
func TestWindowClientForScale(t *testing.T) {
	hwnd := newTestWindow(t, "ViewportScale").create(t, "TypeViewportScaleTest")
	work, ok := monitorWorkArea(hwnd)
	if !ok {
		t.Skip("当前环境拿不到显示器工作区(无头会话?), 跳过")
	}
	workH := int(work.Bottom - work.Top)

	cw, ch, x, y, usable := WindowClientForScale(hwnd, 576, 480, 1.25)
	if !usable {
		t.Fatal("1.25 这个比值被判为不可用")
	}
	if cw != 720 || ch != 600 {
		t.Errorf("客户区 = %dx%d, want 720x600", cw, ch)
	}
	SetWindowClientRect(hwnd, x, y, cw, ch)
	if pw, ph, has := clientPhysicalSize(hwnd); !has || pw != cw || ph != ch {
		t.Errorf("读回客户区 = %dx%d (ok=%v), want %dx%d", pw, ph, has, cw, ch)
	}

	// 越界: 按工作区高度反推一个必然装不下的比值, 不写死数字
	huge := float64(workH)/480 + 1
	if _, _, _, _, usable := WindowClientForScale(hwnd, 576, 480, huge); usable {
		t.Errorf("比值 %.2f 下窗口已高过工作区 %d, 却报告可用", huge, workH)
	}
	for _, bad := range []float64{0, -1, math.NaN(), math.Inf(1), 1e15} {
		if _, _, _, _, usable := WindowClientForScale(hwnd, 576, 480, bad); usable {
			t.Errorf("比值 %v 非法, 却报告可用", bad)
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// TestWindowSizeIsFixed 窗口尺寸必须真的固定: 这是用户明确定下的行为
// ("启动时按显示器定一次, 之后不可手动缩放")。
//
// 判据不能只看源码, 也不能只写"去掉样式位之后不可拖大"这一半 —— 那样写出来的是
// 恒真断言: dwStyle=0 建出的窗口本来就没有 WS_THICKFRAME, 而按**客户区**取点的
// 命中测试对任何带边框的窗口都答 HTCLIENT(1)。2026-10 的独立探针正是这样证明
// 旧版用例两条断言都不可能失败的。现在改成 A/B 两段, 缺一不可:
//
//	① 阳性对照: 用 WS_OVERLAPPEDWINDOW 建窗, 断言两个样式位都在、右下角命中
//	   htBottomRight(17) —— 先证明这套取证手段认得出"可拖大";
//	② 复刻库 SetSize(HintFixed) 的处理(清位 + SWP_FRAMECHANGED 让边框按新样式
//	   刷新), 断言样式位没了、右下角也不再是 htBottomRight。
//
// 缺 ① 则 ② 是自我安慰(怎么都会绿), 缺 ② 则 ① 什么都没证明。
//
// 客户区尺寸同时核对: 请求多少就该是多少(表面与客户区一致的前提)。这一步放在
// 改样式之前 —— SetWindowClientRect 的反推按 WS_OVERLAPPEDWINDOW 算边框, 要在
// 窗口就是那个样式时量才作数
func TestWindowSizeIsFixed(t *testing.T) {
	hwnd := newTestWindow(t, "Fixed").createStyled(t, "TypeFixedSizeTest", wsOverlappedWindow)

	const wantW, wantH = 600, 400
	SetWindowClientRect(hwnd, 100, 100, wantW, wantH)
	// 客户区必须等于请求值: 反推若把标题栏算漏, 这里会少一截 —— 界面底部被切、
	// 但表面与客户区仍一致, 不会发虚, 更难发现
	if w, h, ok := clientPhysicalSize(hwnd); !ok || w != wantW || h != wantH {
		t.Errorf("客户区 = %dx%d (ok=%v), want %dx%d: AdjustWindowRect 的反推不对?",
			w, h, ok, wantW, wantH)
	}

	// ① 阳性对照: 此刻窗口真的可拖大, 取证手段必须认得出
	if got := windowStyle(hwnd); got&wsThickFrame == 0 || got&wsMaximizeBox == 0 {
		t.Fatalf("阳性对照不成立: WS_OVERLAPPEDWINDOW 建出的窗口缺少缩放样式位 (GWL_STYLE=0x%08X)", got)
	}
	if hit := hitTestBottomRight(hwnd); hit != htBottomRight {
		t.Fatalf("阳性对照不成立: 可拖大的窗口右下角命中 = %d, want %d(HTBOTTOMRIGHT) —— 取证手段本身失效了",
			hit, htBottomRight)
	}

	// ② 复刻库 SetSize(HintFixed): 清掉样式位, 并让边框立刻按新样式刷新。
	// 少了 SWP_FRAMECHANGED, 系统可能仍按旧边框回答命中测试
	style, _, _ := procGetWindowLongPtrW.Call(hwnd, ^uintptr(15)) // GWL_STYLE = -16
	style &^= wsThickFrame | wsMaximizeBox
	procSetWindowLongPtrW.Call(hwnd, ^uintptr(15), style)
	procSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged)

	got := windowStyle(hwnd)
	if got&wsThickFrame != 0 {
		t.Error("WS_THICKFRAME 仍在: 窗口右下角可拖拽, 尺寸不固定")
	}
	if got&wsMaximizeBox != 0 {
		t.Error("WS_MAXIMIZEBOX 仍在: 最大化键可用, 尺寸不固定")
	}
	if hit := hitTestBottomRight(hwnd); hit == htBottomRight {
		t.Errorf("右下角 WM_NCHITTEST = %d: 已清掉缩放样式位, 那里却仍报缩放边框", hit)
	}
}
