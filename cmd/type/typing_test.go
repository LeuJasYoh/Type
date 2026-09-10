//go:build windows && (amd64 || arm64)

// 状态机单元测试: 通过 fake 注入器/剪贴板/前台窗口与假睡眠,
// 不触碰真实系统, 覆盖取消衔接、防重入、恢复守卫等曾出缺陷的路径

package main

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

// ─── fakes ────────────────────────────────────────────

// fakeInjector 记录注入调用序列: "r:X"=字符, "E"=回车, "V"=粘贴
type fakeInjector struct {
	mu     sync.Mutex
	events []string
}

func (f *fakeInjector) SendRune(r rune) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "r:"+string(r))
}

func (f *fakeInjector) SendEnter() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "E")
}

func (f *fakeInjector) SendPaste() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "V")
}

func (f *fakeInjector) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

// fakeClipboard 内存剪贴板: 记录操作序列("snap"/"set"/"restore"),
// 可配置 SetText 失败; 快照/恢复按单一文本格式往返
type fakeClipboard struct {
	mu      sync.Mutex
	text    string
	setFail bool
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
	return true
}

func (f *fakeClipboard) GetText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.text
}

func (f *fakeClipboard) Snapshot() []ClipboardFormat {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "snap")
	return []ClipboardFormat{{Fmt: CF_UNICODETEXT, Data: []byte(f.text)}}
}

func (f *fakeClipboard) RestoreSnapshotRaw(snap []ClipboardFormat) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "restore")
	if len(snap) == 0 {
		f.text = ""
		return
	}
	f.text = string(snap[0].Data)
}

func (f *fakeClipboard) ops() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

// fakeForeground 固定标题的前台窗口
type fakeForeground struct{ title string }

func (f fakeForeground) Title() string { return f.title }

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
	svc := newTestService(&fakeInjector{}, &fakeClipboard{}, noSleep)
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
	svc := newTestService(&fakeInjector{}, cb, noSleep)
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
	inj := &fakeInjector{}
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
	inj := &fakeInjector{}
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
	inj := &fakeInjector{}
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
	inj := &fakeInjector{}
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

// SetText 失败: 无条件写回快照, 任务以失败收尾, 不粘贴
func TestClipboardSetFailureRestoresSnapshot(t *testing.T) {
	inj := &fakeInjector{}
	cb := &fakeClipboard{text: "原内容", setFail: true}
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
	// 快照失败路径之外的无条件恢复: snap 后紧跟 restore
	if got, want := cb.ops(), []string{"snap", "set", "restore"}; !reflect.DeepEqual(got, want) {
		t.Errorf("剪贴板操作序列 = %v, want %v", got, want)
	}
	if got := cb.GetText(); got != "原内容" {
		t.Errorf("恢复后文本 = %q, want %q", got, "原内容")
	}
}

// 过代守卫: 取消后立即重启, 旧任务不注入、迟到的状态写入不覆盖新任务
func TestRestartAfterCancelSupersedesOldTask(t *testing.T) {
	inj := &fakeInjector{}
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
