//go:build windows && (amd64 || arm64)

// 状态机单元测试: 通过 fake 注入器/剪贴板/前台窗口与假睡眠,
// 不触碰真实系统, 覆盖取消衔接、防重入、恢复守卫等曾出缺陷的路径

package main

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
)

// ─── fakes ────────────────────────────────────────────

// fakeInjector 记录注入调用序列: "r:X"=字符, "E"=回车, "V"=粘贴。
// failAfter >= 0 时模拟系统拒绝注入(SendInput 返回 0, 典型为 UIPI),
// 第 failAfter 个注入起返回 false 且不记录事件
type fakeInjector struct {
	mu        sync.Mutex
	events    []string
	failAfter int
}

func newFakeInjector() *fakeInjector { return &fakeInjector{failAfter: -1} }

// record 记录一次注入并返回是否被系统接受
func (f *fakeInjector) record(event string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAfter >= 0 && len(f.events) >= f.failAfter {
		return false
	}
	f.events = append(f.events, event)
	return true
}

func (f *fakeInjector) SendRune(r rune) bool { return f.record("r:" + string(r)) }

func (f *fakeInjector) SendEnter() bool { return f.record("E") }

func (f *fakeInjector) SendPaste() bool { return f.record("V") }

func (f *fakeInjector) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

func (f *fakeInjector) clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = nil
}

// fakeClipboard 内存剪贴板: 记录操作序列("snap"/"set"/"restore"),
// 可配置 SetText 失败; 快照/恢复按单一文本格式往返。
// held 是"读回来的内容", 用来模拟用户在注入期间改动剪贴板: text 是
// 程序写进去的, held 是下次读取时看到的
type fakeClipboard struct {
	mu      sync.Mutex
	text    string
	held    string
	setFail bool
	snap    []ClipboardFormat
	events  []string
}

func (f *fakeClipboard) SetText(text string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "set")
	if f.setFail {
		return false
	}
	f.text = text
	f.held = text // 写进去的内容就是下次读回来的内容, 除非测试另行改动 held
	return true
}

func (f *fakeClipboard) GetText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.text
}

func (f *fakeClipboard) HoldsText(text string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held == text
}

func (f *fakeClipboard) Snapshot() []ClipboardFormat {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "snap")
	if f.snap != nil {
		return f.snap
	}
	return []ClipboardFormat{{Fmt: CF_UNICODETEXT, Data: []byte(f.text)}}
}

func (f *fakeClipboard) RestoreSnapshotRaw(snap []ClipboardFormat) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "restore")
	if len(snap) == 0 {
		f.text = ""
		f.held = ""
		return
	}
	if f.snap != nil {
		// 带 UTF-16LE 编码的快照(如设置失败路径的未落盘快照): 解码回文本
		f.text = string(utf16.Decode(unitsOf(snap[0].Data)))
		f.held = f.text
		return
	}
	f.text = string(snap[0].Data)
	f.held = f.text
}

// unitsOf 把字节流按 UTF-16LE 切回码元
func unitsOf(raw []byte) []uint16 {
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		units = append(units, uint16(raw[i])|uint16(raw[i+1])<<8)
	}
	return units
}

func (f *fakeClipboard) ops() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

// fakeForeground 固定标题的前台窗口; self 置位模拟"焦点停在 Type 自身"
type fakeForeground struct {
	title string
	self  bool
}

func (f fakeForeground) Title() string { return f.title }
func (f fakeForeground) IsSelf() bool  { return f.self }

// switchableForeground 可变前台窗口: 模拟用户在倒计时期间切换目标/回看 Type
type switchableForeground struct {
	mu    sync.Mutex
	title string
	self  bool
}

func (f *switchableForeground) Title() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.title
}

func (f *switchableForeground) IsSelf() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.self
}

func (f *switchableForeground) set(title string, self bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.title, f.self = title, self
}

// ─── 测试辅助 ─────────────────────────────────────────

func newTestService(inj TextInjector, cb Clipboard, sleep func(time.Duration)) *TypingService {
	s := newTypingService(inj, cb, fakeForeground{title: "记事本"})
	s.sleep = sleep
	return s
}

// noSleep 假睡眠: 任务瞬间走完所有等待
func noSleep(time.Duration) {}

// blockSleep 阻塞型假睡眠: release 关闭前任务停在首个等待点
func blockSleep(release <-chan struct{}) func(time.Duration) {
	return func(time.Duration) { <-release }
}

// waitRunning 等待任务认领 runningFlag (即已进入倒计时等待)
func waitRunning(t *testing.T, svc *TypingService) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !svc.runningFlag.Load() {
		if time.Now().After(deadline) {
			t.Fatal("任务未及时认领运行标志")
		}
		time.Sleep(time.Millisecond)
	}
}

// waitTerminal 轮询直到任务到达终止 phase(success/error/cancel)并返回该状态
func waitTerminal(t *testing.T, svc *TypingService) TypingStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := svc.Status(); isTerminal(st.Phase) {
			return *st
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("任务未在期限内到达终止态")
	return TypingStatus{}
}

func isTerminal(p TypingPhase) bool {
	return p == PhaseSuccess || p == PhaseError || p == PhaseCancel
}

// ─── 用例 ─────────────────────────────────────────────

// 初始状态契约: 服务构造后即为 idle 零值状态, 前端首次轮询读到的是它
func TestServiceInitialState(t *testing.T) {
	svc := newTestService(newFakeInjector(), &fakeClipboard{}, noSleep)
	st := svc.Status()
	if st.Phase != PhaseIdle {
		t.Fatalf("初始 phase = %s, want idle", st.Phase)
	}
	if st.Message != "" || st.Progress != 0 || st.SecondsLeft != 0 || st.TargetWindow != "" {
		t.Errorf("初始状态字段应为零值, got %+v", *st)
	}
}

// 恢复守卫(fake 层): 剪贴板仍为注入文本才恢复; 用户已复制新内容则跳过;
// 快照失败(nil)时原状态未知, 不动剪贴板
func TestServiceClipboardGuard(t *testing.T) {
	cb := &fakeClipboard{text: "用户原文本"}
	svc := newTestService(newFakeInjector(), cb, noSleep)
	snap := cb.Snapshot() // 快照内容: 用户原文本

	// 剪贴板仍是注入文本 → 守卫放行, 恢复原内容
	if !cb.SetText("注入文本") {
		t.Fatal("SetText 失败")
	}
	opsBefore := len(cb.ops())
	svc.restoreClipboardSnapshot(snap, "注入文本")
	if got := cb.GetText(); got != "用户原文本" {
		t.Errorf("守卫放行时应恢复原文本, got %q", got)
	}
	if len(cb.ops()) != opsBefore+1 {
		t.Errorf("守卫放行时应执行一次恢复, 操作数 %d → %d", opsBefore, len(cb.ops()))
	}

	// 用户在注入期间复制了新内容 → 守卫拦截, 不覆盖
	if !cb.SetText("用户新复制的内容") {
		t.Fatal("SetText 失败")
	}
	opsBefore = len(cb.ops())
	svc.restoreClipboardSnapshot(snap, "注入文本")
	if got := cb.GetText(); got != "用户新复制的内容" {
		t.Errorf("守卫应拦截恢复, 剪贴板被覆盖为 %q", got)
	}
	if len(cb.ops()) != opsBefore {
		t.Errorf("守卫拦截时不应有剪贴板操作, 操作数 %d → %d", opsBefore, len(cb.ops()))
	}

	// 快照失败(nil) → 原状态未知, 不动剪贴板
	opsBefore = len(cb.ops())
	svc.restoreClipboardSnapshot(nil, "用户新复制的内容")
	if got := cb.GetText(); got != "用户新复制的内容" {
		t.Errorf("nil 快照不应改动剪贴板, got %q", got)
	}
	if len(cb.ops()) != opsBefore {
		t.Errorf("nil 快照不应产生剪贴板操作, 操作数 %d → %d", opsBefore, len(cb.ops()))
	}
}

// 运行中 Start 拒绝重入, 且拒绝不影响在途任务
func TestStartRejectsReentryWhileRunning(t *testing.T) {
	inj := newFakeInjector()
	release := make(chan struct{})
	svc := newTestService(inj, &fakeClipboard{}, blockSleep(release))

	if _, err := svc.Start("AB", 3, false); err != nil {
		t.Fatalf("首次 Start 失败: %v", err)
	}
	waitRunning(t, svc)

	if _, err := svc.Start("CD", 1, false); err == nil {
		t.Fatal("运行中二次 Start 应被拒绝")
	} else if got, want := err.Error(), "已有输入任务在运行中，请先取消或等待完成"; got != want {
		t.Fatalf("重入错误文案 = %q, want %q", got, want)
	}

	close(release)
	if st := waitTerminal(t, svc); st.Phase != PhaseSuccess {
		t.Fatalf("重入被拒后原任务应正常完成, phase = %s", st.Phase)
	}
	if got, want := inj.calls(), []string{"r:A", "r:B"}; !reflect.DeepEqual(got, want) {
		t.Errorf("注入序列 = %v, want %v (二次 Start 的文本不应混入)", got, want)
	}
}

// 倒计时中取消: 零注入, 终态为 cancel
func TestCancelDuringCountdown(t *testing.T) {
	inj := newFakeInjector()
	release := make(chan struct{})
	svc := newTestService(inj, &fakeClipboard{}, blockSleep(release))

	if _, err := svc.Start("中文", 5, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	waitRunning(t, svc)

	if _, err := svc.Cancel(); err != nil {
		t.Fatalf("Cancel 失败: %v", err)
	}
	close(release)

	st := waitTerminal(t, svc)
	if st.Phase != PhaseCancel {
		t.Fatalf("终态 phase = %s, want cancel", st.Phase)
	}
	if st.Message != "已取消" {
		t.Errorf("终态文案 = %q, want %q", st.Message, "已取消")
	}
	if calls := inj.calls(); len(calls) != 0 {
		t.Errorf("取消后不应有任何注入, got %v", calls)
	}
}

// ASCII 逐字符路径: \r 剔除、\n 转回车、进度报数、成功收尾
func TestAsciiTyping(t *testing.T) {
	inj := newFakeInjector()
	var mu sync.Mutex
	var history []TypingStatus
	var svc *TypingService
	svc = newTestService(inj, &fakeClipboard{}, func(time.Duration) {
		mu.Lock()
		history = append(history, *svc.Status())
		mu.Unlock()
	})

	if _, err := svc.Start("AB\r\nCD", 1, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if st := waitTerminal(t, svc); st.Phase != PhaseSuccess {
		t.Fatalf("终态 phase = %s, want success", st.Phase)
	}

	// \r 剔除后共 5 个字符: A B \n C D, \n 转为一次回车
	want := []string{"r:A", "r:B", "E", "r:C", "r:D"}
	if got := inj.calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("注入序列 = %v, want %v", got, want)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(history) == 0 {
		t.Fatal("未捕获到任何等待时的状态")
	}
	// 倒计时初态: 目标窗口标题来自前台探测, 剩余秒数即 delay
	h0 := history[0]
	if h0.Phase != PhaseCountdown || h0.SecondsLeft != 1 || h0.TargetWindow != "记事本" {
		t.Errorf("倒计时状态异常: %+v", h0)
	}
	// 末次进度报满 (\r 不计入分母)
	hLast := history[len(history)-1]
	if hLast.Message != "正在逐字符输入 5 / 5 ..." || hLast.Progress != 100 {
		t.Errorf("末次进度 = %q progress=%d, want 5 / 5 且 100", hLast.Message, hLast.Progress)
	}
}

// 中文路径: 快照 → 写入注入文本 → 粘贴 → 恢复 的调用顺序与恢复语义
func TestChineseTypesViaClipboard(t *testing.T) {
	inj := newFakeInjector()
	cb := &fakeClipboard{text: "用户原文本"}
	svc := newTestService(inj, cb, noSleep)

	if _, err := svc.Start("你好", 1, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if st := waitTerminal(t, svc); st.Phase != PhaseSuccess {
		t.Fatalf("终态 phase = %s, want success", st.Phase)
	}

	// 注入器只收到一次粘贴, 不逐字符
	if got, want := inj.calls(), []string{"V"}; !reflect.DeepEqual(got, want) {
		t.Errorf("注入序列 = %v, want %v", got, want)
	}
	// 剪贴板操作顺序: 快照 → 写入 → 恢复
	if got, want := cb.ops(), []string{"snap", "set", "restore"}; !reflect.DeepEqual(got, want) {
		t.Errorf("剪贴板操作序列 = %v, want %v", got, want)
	}
	// 注入期间文本未被用户改动, 守卫放行, 原文本恢复
	if got := cb.GetText(); got != "用户原文本" {
		t.Errorf("恢复后文本 = %q, want %q", got, "用户原文本")
	}
}

// 过代守卫: 取消后立即重启, 旧任务不注入、迟到的状态写入不覆盖新任务
func TestRestartAfterCancelSupersedesOldTask(t *testing.T) {
	inj := newFakeInjector()
	release := make(chan struct{})
	svc := newTestService(inj, &fakeClipboard{}, blockSleep(release))

	// 旧任务: 停在倒计时
	if _, err := svc.Start("旧任务文本", 5, false); err != nil {
		t.Fatalf("首次 Start 失败: %v", err)
	}
	waitRunning(t, svc)

	if _, err := svc.Cancel(); err != nil {
		t.Fatalf("Cancel 失败: %v", err)
	}
	// 取消收尾尚未完成(旧任务仍持有 runningFlag)时立即重启
	if _, err := svc.Start("新", 2, false); err != nil {
		t.Fatalf("取消后应能立即重启: %v", err)
	}
	close(release)

	st := waitTerminal(t, svc)
	if st.Phase != PhaseSuccess {
		t.Fatalf("终态 phase = %s, want success (新任务的终态不应被旧任务覆盖)", st.Phase)
	}
	// 只有新任务注入: "新"为非 ASCII → 剪贴板粘贴一次
	if got, want := inj.calls(), []string{"V"}; !reflect.DeepEqual(got, want) {
		t.Errorf("注入序列 = %v, want %v (旧任务文本不应注入)", got, want)
	}
}

// waitIdle 等待 runningFlag 被释放
func waitIdle(t *testing.T, svc *TypingService) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for svc.runningFlag.Load() {
		if time.Now().After(deadline) {
			t.Fatal("任务未及时释放运行标志")
		}
		time.Sleep(time.Millisecond)
	}
}

// 补上未落盘的剪贴板快照: 单宽字符的 UTF-16LE 两字节, 模拟失败路径的恢复
func seedSnapshot(cb *fakeClipboard, text string) {
	units := utf16.Encode([]rune(text))
	raw := make([]byte, len(units)*2)
	for i, u := range units {
		raw[i*2] = byte(u)
		raw[i*2+1] = byte(u >> 8)
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.snap = []ClipboardFormat{{Fmt: CF_UNICODETEXT, Data: raw}}
}

// 逐字符注入被系统拒绝(UIPI 等): 立即中止, 终态给出可读原因, 不虚报"输入完成"
func TestSendInputRejectedDuringTyping(t *testing.T) {
	inj := newFakeInjector()
	inj.failAfter = 2 // A B 通过, C 起被拒
	svc := newTestService(inj, &fakeClipboard{}, noSleep)

	if _, err := svc.Start("ABCDE", 1, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseError {
		t.Fatalf("注入被拒时终态 phase = %s, want error", st.Phase)
	}
	if st.Message == "输入完成" || st.Message == "输入失败" {
		t.Errorf("终态文案 = %q, 应给出具体中断原因", st.Message)
	}
	if got, want := inj.calls(), []string{"r:A", "r:B"}; !reflect.DeepEqual(got, want) {
		t.Errorf("注入序列 = %v, want %v (被拒后不应继续注入)", got, want)
	}
}

// Ctrl+V 被系统拒绝: 报"粘贴未生效", 不报"输入完成";
// 剪贴板已是注入文本, 守卫放行恢复原内容
func TestSendPasteRejected(t *testing.T) {
	inj := newFakeInjector()
	inj.failAfter = 0
	cb := &fakeClipboard{text: "用户原文本"}
	svc := newTestService(inj, cb, noSleep)

	if _, err := svc.Start("你好", 1, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseError {
		t.Fatalf("粘贴被拒时终态 phase = %s, want error", st.Phase)
	}
	if st.Message != "输入失败" {
		t.Errorf("终态文案 = %q, want %q", st.Message, "输入失败")
	}
	if got, want := cb.ops(), []string{"snap", "set", "restore"}; !reflect.DeepEqual(got, want) {
		t.Errorf("剪贴板操作序列 = %v, want %v", got, want)
	}
	if got := cb.GetText(); got != "用户原文本" {
		t.Errorf("粘贴被拒后仍应恢复原文本, got %q", got)
	}
}

// 上一任务不让出运行标志: 第二个任务等满 2 秒超时报错, 且不得注入任何内容。
// 用"停在假睡眠里的真任务"占住标志, 而不是手工翻转标志位——后者会被
// Start 的重入检查提前拦下, 根本走不到 deadline 分支
func TestStartDeadlineWhenPreviousTaskStuck(t *testing.T) {
	release := make(chan struct{}, 1)
	inj := newFakeInjector()
	svc := newTestService(inj, &fakeClipboard{}, blockSleep(release))

	// 第一个任务认领标志后停在 sleep 上, 迟迟不让出
	if _, err := svc.Start("旧任务", 1, false); err != nil {
		t.Fatalf("首次 Start 失败: %v", err)
	}
	waitRunning(t, svc)

	// 用户取消, 随即立刻重启: 取消只置标志不等待旧任务退出, 于是新任务
	// 必须在"标志仍被占着"的情况下自己等——这正是 deadline 分支的现实入口
	svc.cancelFlag.Store(true)
	if _, err := svc.Start("新任务", 1, false); err != nil {
		t.Fatalf("取消后应能立即重启: %v", err)
	}
	const timeoutMsg = "启动失败：上一任务未能及时退出"
	// 等 deadline 报错: 此时 runningFlag 仍被旧任务占着, 不会有第二次写入竞争
	deadline := time.Now().Add(5 * time.Second)
	for svc.Status().Message != timeoutMsg && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if svc.Status().Message != timeoutMsg {
		t.Fatalf("未等到超时报错, 当前状态 = %+v", *svc.Status())
	}
	if st := svc.Status(); st.Phase != PhaseError {
		t.Fatalf("终态 phase = %s, want error", st.Phase)
	}
	if calls := inj.calls(); len(calls) != 0 {
		t.Errorf("超时任务不应注入任何内容, got %v", calls)
	}

	// 放行旧任务: 它已过代并自行退出, 迟到的写入不得覆盖上面的错误状态
	release <- struct{}{}
	waitIdle(t, svc)
	time.Sleep(20 * time.Millisecond) // 给过代任务的收尾留出窗口
	final := svc.Status()
	if final.Phase != PhaseError || final.Message != timeoutMsg {
		t.Errorf("旧任务收尾覆盖了当前状态: %s/%q", final.Phase, final.Message)
	}
}

// 剪贴板写入失败: 恢复未落盘的快照, 报"输入失败", 不粘贴
func TestClipboardSetFailureNoInjection(t *testing.T) {
	inj := newFakeInjector()
	cb := &fakeClipboard{text: "原内容", setFail: true}
	seedSnapshot(cb, "原内容")
	svc := newTestService(inj, cb, noSleep)

	if _, err := svc.Start("中文", 1, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseError || st.Message != "输入失败" {
		t.Fatalf("终态 = %s/%q, want error/输入失败", st.Phase, st.Message)
	}
	if calls := inj.calls(); len(calls) != 0 {
		t.Errorf("SetText 失败不应粘贴, got %v", calls)
	}
	// set 也会被尝试一次(失败), 记录在案才能证明"试过但没成"
	if got, want := cb.ops(), []string{"snap", "set", "restore"}; !reflect.DeepEqual(got, want) {
		t.Errorf("剪贴板操作序列 = %v, want %v", got, want)
	}
	if got := cb.GetText(); got != "原内容" {
		t.Errorf("恢复后文本 = %q, want %q", got, "原内容")
	}
}

// 守卫按原始字节比对: held 变化即视为用户改动, 跳过恢复
func TestRestoreGuardUsesHoldsText(t *testing.T) {
	// text 是程序写进去的, held 是读回来的: 这里模拟"写进去之后用户又复制了别的"
	cb := &fakeClipboard{text: "注入文本", held: "用户新复制的内容"}
	svc := newTestService(newFakeInjector(), cb, noSleep)
	snap := cb.Snapshot()

	// 读回来的与注入文本不同 → 拦截
	opsBefore := len(cb.ops())
	svc.restoreClipboardSnapshot(snap, "注入文本")
	if len(cb.ops()) != opsBefore {
		t.Errorf("held 已变化时不应恢复, 操作数 %d → %d", opsBefore, len(cb.ops()))
	}

	// 恢复快照后读回来的就是注入文本 → 放行
	opsBefore = len(cb.ops())
	svc.restoreClipboardSnapshot(snap, "用户新复制的内容")
	if len(cb.ops()) != opsBefore+1 {
		t.Errorf("held 与参数一致时应恢复, 操作数 %d → %d", opsBefore, len(cb.ops()))
	}
}

// 空文本: 不注入也不报"输入完成"
func TestEmptyTextDoesNotReportSuccess(t *testing.T) {
	inj := newFakeInjector()
	svc := newTestService(inj, &fakeClipboard{}, noSleep)
	if _, err := svc.Start("", 1, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Message == "输入完成" {
		t.Errorf("空文本不得报输入完成: %+v", st)
	}
	if calls := inj.calls(); len(calls) != 0 {
		t.Errorf("空文本不应注入, got %v", calls)
	}
}

// 目标窗口语义: 终态携带执行时锁定的目标, 执行阶段预览不再清空
func TestTargetWindowLockedIntoFinalStatus(t *testing.T) {
	inj := newFakeInjector()
	svc := newTestService(inj, &fakeClipboard{}, noSleep)
	if _, err := svc.Start("你好", 1, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseSuccess {
		t.Fatalf("终态 phase = %s, want success", st.Phase)
	}
	if st.TargetWindow != "记事本" {
		t.Errorf("终态目标窗口 = %q, want 记事本", st.TargetWindow)
	}
}

// 倒计时结束焦点仍在 Type 自身: 报错且零注入零剪贴板操作,
// 不把文本打进自己的输入框
func TestSelfForegroundAbortsBeforeInjection(t *testing.T) {
	inj := newFakeInjector()
	cb := &fakeClipboard{}
	svc := newTypingService(inj, cb, fakeForeground{title: "Type 测试", self: true})
	svc.sleep = noSleep
	if _, err := svc.Start("你好", 1, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseError {
		t.Fatalf("终态 phase = %s, want error", st.Phase)
	}
	if !strings.Contains(st.Message, "未切换到目标窗口") {
		t.Errorf("终态文案 = %q, 应说明焦点仍在 Type", st.Message)
	}
	if calls := inj.calls(); len(calls) != 0 {
		t.Errorf("不应有任何注入, got %v", calls)
	}
	if ops := cb.ops(); len(ops) != 0 {
		t.Errorf("不应触碰剪贴板, got %v", ops)
	}
}

// 倒计时预览 = 最近一个非 Type 前台窗口: 中途回看 Type 不清空预览,
// 切换目标则随之更新; 终态锁定为执行时的目标
func TestCountdownPreviewTracksLastNonSelfWindow(t *testing.T) {
	fg := &switchableForeground{title: "记事本"}
	inj := newFakeInjector()
	var mu sync.Mutex
	var history []TypingStatus
	var sleeps int
	var svc *TypingService
	svc = newTypingService(inj, &fakeClipboard{}, fg)
	svc.sleep = func(time.Duration) {
		sleeps++
		switch sleeps {
		case 1:
			fg.set("浏览器", false) // 用户切到真正的目标
		case 2:
			fg.set("Type 测试", true) // 用户中途回看 Type
		case 3:
			fg.set("浏览器", false) // 最后一秒切回目标
		}
		mu.Lock()
		history = append(history, *svc.Status())
		mu.Unlock()
	}

	if _, err := svc.Start("你好", 3, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseSuccess {
		t.Fatalf("终态 phase = %s, want success", st.Phase)
	}
	if st.TargetWindow != "浏览器" {
		t.Errorf("终态目标 = %q, want 浏览器", st.TargetWindow)
	}

	mu.Lock()
	defer mu.Unlock()
	var lastCountdown *TypingStatus
	for i := range history {
		if history[i].Phase == PhaseCountdown {
			lastCountdown = &history[i]
		}
	}
	if lastCountdown == nil {
		t.Fatal("未捕获到倒计时状态")
	}
	if lastCountdown.TargetWindow != "浏览器" {
		t.Errorf("回看 Type 期间预览 = %q, want 浏览器 (自身不得覆盖预览)", lastCountdown.TargetWindow)
	}
}
