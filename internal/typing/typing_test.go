// 状态机单元测试: 通过 fake 注入器/剪贴板/前台窗口与假睡眠,
// 不触碰真实系统, 覆盖取消衔接、防重入、恢复守卫等曾出缺陷的路径

package typing

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
)

// ─── fakes ────────────────────────────────────────────

// fakeInjector 记录注入调用序列: "r:X"=按键层字符, "T:X"=文本层字符(文本直投),
// "E"=回车, "ESC"=Esc, "V"=粘贴。
// failAfter >= 0 时模拟系统拒绝注入(SendInput 返回 0, 典型为 UIPI),
// 第 failAfter 个注入起返回 false 且不记录事件。
// onInject 在每次成功记录后回调: 模拟"就在这次注入的瞬间发生的事"
// (如目标窗口恰在粘贴时被切走)
type fakeInjector struct {
	mu        sync.Mutex
	events    []string
	failAfter int
	onInject  func(event string)
}

func newFakeInjector() *fakeInjector { return &fakeInjector{failAfter: -1} }

// record 记录一次注入并返回是否被系统接受
func (f *fakeInjector) record(event string) bool {
	f.mu.Lock()
	if f.failAfter >= 0 && len(f.events) >= f.failAfter {
		f.mu.Unlock()
		return false
	}
	f.events = append(f.events, event)
	hook := f.onInject
	f.mu.Unlock()
	if hook != nil {
		hook(event)
	}
	return true
}

func (f *fakeInjector) SendRune(r rune) bool { return f.record("r:" + string(r)) }

func (f *fakeInjector) SendText(r rune) bool { return f.record("T:" + string(r)) }

func (f *fakeInjector) SendEnter() bool { return f.record("E") }

func (f *fakeInjector) SendEscape() bool { return f.record("ESC") }

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
// 可配置 SetText 失败与恢复失败; 快照/恢复按单一文本格式往返。
// held 是"读回来的内容", 用来模拟用户在注入期间改动剪贴板: text 是
// 程序写进去的, held 是下次读取时看到的
type fakeClipboard struct {
	mu          sync.Mutex
	text        string
	held        string
	setFail     bool
	restoreFail bool // 恢复失败: 模拟剪贴板被别的程序占着, 原内容就此丢失
	snapFail    bool // 剪贴板打开失败: 原状态未知
	incomplete  bool // 有的格式没能照抄下来: 快照不完整, 不该拿去覆盖剪贴板
	// holdsUnreadable: 比对时读不到剪贴板(打开失败/GlobalLock 失败)。它与
	// "内容被用户改过"是两回事, 处置相反: 前者必须报"没恢复", 后者跳过恢复
	holdsUnreadable bool
	snap            []ClipboardFormat
	events          []string
}

// cfUnicodeText 假剪贴板里充当文本条目的格式号, 与 Win32 的 CF_UNICODETEXT
// 同值; 业务层只把它当作不透明的格式标识, 不解释内容
const cfUnicodeText = 13

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

// HoldsText 第二个返回值模拟"这次比对有没有得出结论": holdsUnreadable 置位时
// 是读不到剪贴板(known=false), 而不是"内容被用户改过"
func (f *fakeClipboard) HoldsText(text string) (bool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.holdsUnreadable {
		return false, false
	}
	return f.held == text, true
}

func (f *fakeClipboard) Snapshot() ClipboardSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "snap")
	if f.snapFail {
		return ClipboardSnapshot{} // 打开失败: 原状态未知
	}
	if f.incomplete {
		return ClipboardSnapshot{
			Formats:  []ClipboardFormat{{Fmt: cfUnicodeText, Data: []byte(f.text)}},
			Complete: false,
		}
	}
	if f.snap != nil {
		return ClipboardSnapshot{Formats: f.snap, Complete: true}
	}
	return ClipboardSnapshot{
		Formats:  []ClipboardFormat{{Fmt: cfUnicodeText, Data: []byte(f.text)}},
		Complete: true,
	}
}

func (f *fakeClipboard) RestoreSnapshotRaw(snap []ClipboardFormat) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "restore")
	if f.restoreFail {
		return false // 剪贴板被占用: 一个字节都没写回去
	}
	if len(snap) == 0 {
		f.text = ""
		f.held = ""
		return true
	}
	if f.snap != nil {
		// 带 UTF-16LE 编码的快照(如设置失败路径的未落盘快照): 解码回文本
		f.text = string(utf16.Decode(unitsOf(snap[0].Data)))
		f.held = f.text
		return true
	}
	f.text = string(snap[0].Data)
	f.held = f.text
	return true
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

// fakeForeground 固定标识与标题的前台窗口; self 置位模拟"焦点停在 Type 自身"
type fakeForeground struct {
	title string
	self  bool
}

func (f fakeForeground) Sample() ForegroundSample {
	return ForegroundSample{ID: 1, Title: f.title, Self: f.self}
}

// switchableForeground 可变前台窗口: 模拟用户在倒计时/注入期间切换目标、
// 回看 Type。标识与标题一起换 —— 判据是标识, 标题只用于展示
type switchableForeground struct {
	mu    sync.Mutex
	id    TargetID
	title string
	self  bool
}

func (f *switchableForeground) Sample() ForegroundSample {
	f.mu.Lock()
	defer f.mu.Unlock()
	return ForegroundSample{ID: f.id, Title: f.title, Self: f.self}
}

func (f *switchableForeground) set(id TargetID, title string, self bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.id, f.title, f.self = id, title, self
}

// samplingForeground 固定窗口 + 采样计数: 用于验证倒计时"每拍采样一次"
// 的节拍(采样与写状态是分开的, 次数就是节拍数)
type samplingForeground struct {
	mu      sync.Mutex
	samples int
}

func (f *samplingForeground) Sample() ForegroundSample {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.samples++
	return ForegroundSample{ID: 7, Title: "记事本"}
}

func (f *samplingForeground) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.samples
}

// firstSampleBlocks 第一次采样就卡住, 之后的采样照常返回。
// 用来把 Cancel 精确打进 Start 的采样窗口, 并让紧随其后的倒计时可以跑完
type firstSampleBlocks struct {
	mu      sync.Mutex
	calls   int
	entered chan struct{}
	release chan struct{}
}

func (f *firstSampleBlocks) Sample() ForegroundSample {
	f.mu.Lock()
	f.calls++
	first := f.calls == 1
	f.mu.Unlock()
	if !first {
		return ForegroundSample{ID: 42, Title: "target"}
	}
	close(f.entered)
	<-f.release
	return ForegroundSample{ID: 42, Title: "target"}
}

// ─── 测试辅助 ─────────────────────────────────────────

func newTestService(inj TextInjector, cb Clipboard, sleep func(time.Duration)) *TypingService {
	s := NewTypingService(inj, cb, fakeForeground{title: "记事本"})
	s.sleep = sleep
	return s
}

// noSleep 假睡眠: 任务瞬间走完所有等待
func noSleep(time.Duration) {}

// blockSleep 阻塞型假睡眠: release 关闭前任务停在首个等待点
func blockSleep(release <-chan struct{}) func(time.Duration) {
	return func(time.Duration) { <-release }
}

// waitRunning 等待任务占据任务槽 (即已进入倒计时等待)
func waitRunning(t *testing.T, svc *TypingService) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		svc.mu.Lock()
		running := svc.prevTask != nil
		svc.mu.Unlock()
		if running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("任务未及时占据任务槽")
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

// 初始状态契约: 服务构造后即为 idle 状态, 前端首次轮询读到的是它。
// progress 必须是 -1 而不是零值 0: 0 的含义是"进度 0%", 前端据此会显示进度条,
// 于是空闲态的界面在启动时长出一条 10px 的空轨道(跑过一次任务后又消失)
func TestServiceInitialState(t *testing.T) {
	svc := newTestService(newFakeInjector(), &fakeClipboard{}, noSleep)
	st := svc.Status()
	if st.Phase != PhaseIdle {
		t.Fatalf("初始 phase = %s, want idle", st.Phase)
	}
	if st.Message != "" || st.SecondsLeft != 0 || st.TargetWindow != "" {
		t.Errorf("初始状态的消息/秒数/目标窗口应为零值, got %+v", *st)
	}
	if st.Progress != -1 {
		t.Errorf("初始 progress = %d, want -1(隐藏); 0 会让前端画出空进度轨道", st.Progress)
	}
}

// 恢复守卫(fake 层): 剪贴板仍为注入文本才恢复; 用户已复制新内容则跳过;
// 快照失败(不完整)时原状态未知, 不动剪贴板
func TestServiceClipboardGuard(t *testing.T) {
	cb := &fakeClipboard{text: "用户原文本"}
	svc := newTestService(newFakeInjector(), cb, noSleep)
	snap := cb.Snapshot().Formats // 快照内容: 用户原文本

	// 剪贴板仍是注入文本 → 守卫放行, 恢复原内容
	if !cb.SetText("注入文本") {
		t.Fatal("SetText 失败")
	}
	opsBefore := len(cb.ops())
	if !svc.restoreClipboardSnapshot(snap, "注入文本") {
		t.Error("守卫放行且恢复成功时应返回 true")
	}
	if got := cb.GetText(); got != "用户原文本" {
		t.Errorf("守卫放行时应恢复原文本, got %q", got)
	}
	if len(cb.ops()) != opsBefore+1 {
		t.Errorf("守卫放行时应执行一次恢复, 操作数 %d → %d", opsBefore, len(cb.ops()))
	}

	// 用户在注入期间复制了新内容 → 守卫拦截, 不覆盖。剪贴板此刻归用户所有,
	// 正是想要的结果, 因此不算"未恢复"
	if !cb.SetText("用户新复制的内容") {
		t.Fatal("SetText 失败")
	}
	opsBefore = len(cb.ops())
	if !svc.restoreClipboardSnapshot(snap, "注入文本") {
		t.Error("用户已接管剪贴板时应返回 true(剪贴板内容没丢)")
	}
	if got := cb.GetText(); got != "用户新复制的内容" {
		t.Errorf("守卫应拦截恢复, 剪贴板被覆盖为 %q", got)
	}
	if len(cb.ops()) != opsBefore {
		t.Errorf("守卫拦截时不应有剪贴板操作, 操作数 %d → %d", opsBefore, len(cb.ops()))
	}

	// 快照失败(nil) → 原状态未知, 不动剪贴板, 且必须报"没恢复"
	opsBefore = len(cb.ops())
	if svc.restoreClipboardSnapshot(nil, "用户新复制的内容") {
		t.Error("快照缺失时无从恢复, 应返回 false")
	}
	if got := cb.GetText(); got != "用户新复制的内容" {
		t.Errorf("nil 快照不应改动剪贴板, got %q", got)
	}
	if len(cb.ops()) != opsBefore {
		t.Errorf("nil 快照不应产生剪贴板操作, 操作数 %d → %d", opsBefore, len(cb.ops()))
	}

	// 读不到剪贴板: 这不是"用户改过", 而是"原内容还在不在无从判断"。
	// 必须按"没恢复"上报(旧签名把两者并成一个 false, 于是静默跳过恢复、
	// 用户原内容丢了还收到"输入完成"); 同时不许覆盖剪贴板 —— 那可能盖掉
	// 用户刚复制的东西
	cb.holdsUnreadable = true
	opsBefore = len(cb.ops())
	if svc.restoreClipboardSnapshot(snap, "用户新复制的内容") {
		t.Error("读不到剪贴板时应按未恢复上报(false), 不能当成用户已改动")
	}
	if got := cb.GetText(); got != "用户新复制的内容" {
		t.Errorf("读不到剪贴板时不应改动内容, got %q", got)
	}
	if len(cb.ops()) != opsBefore {
		t.Errorf("读不到剪贴板时不应产生剪贴板操作, 操作数 %d → %d", opsBefore, len(cb.ops()))
	}
	cb.holdsUnreadable = false
}

// 并发 Start 只能有一个拿到任务槽: 另一路必须被拒, 而不是把同一段文本注入两遍。
// 修复前 Start 先读运行标志、再由 goroutine 认领, 两次几乎同时到达的启动都能
// 通过那次读, 于是各跑各的; 实测 300 轮里 246 轮注入两遍
func TestConcurrentStartNeverStacks(t *testing.T) {
	const rounds = 80
	dupRounds := 0
	for i := 0; i < rounds; i++ {
		inj := newFakeInjector()
		svc := newTestService(inj, &fakeClipboard{}, noSleep)
		// 把第一个任务钉在运行中: 第二路入场时它必须还占着任务槽,
		// 否则两路都是合法启动, 测的就不是重入了
		releaseTask := make(chan struct{})
		svc.sleep = func(d time.Duration) {
			select {
			case <-releaseTask:
			case <-time.After(500 * time.Millisecond): // 兜底: 测试自身出问题时别挂住
			}
		}

		// 两路同时入场, 起跑线用闭锁拉平
		var gate sync.WaitGroup
		gate.Add(1)
		var wg sync.WaitGroup
		var mu sync.Mutex
		accepted, rejected := 0, 0
		for j := 0; j < 2; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				gate.Wait()
				_, err := svc.Start("AB", 1, false, false)
				mu.Lock()
				if err == nil {
					accepted++
				} else {
					rejected++
				}
				mu.Unlock()
			}()
		}
		gate.Done()
		wg.Wait()
		close(releaseTask)

		if st := waitTerminal(t, svc); st.Phase != PhaseSuccess {
			t.Fatalf("第 %d 轮终态 = %s/%q, want success", i, st.Phase, st.Message)
		}
		if got := len(inj.calls()); got != 2 {
			dupRounds++
			if dupRounds <= 3 {
				t.Errorf("第 %d 轮注入 %d 次, want 2 (接受 %d 路): %v", i, got, accepted, inj.calls())
			}
		}
		if accepted != 1 || rejected != 1 {
			t.Fatalf("第 %d 轮接受 %d 路、拒绝 %d 路, want 各 1", i, accepted, rejected)
		}
	}
	if dupRounds > 0 {
		t.Errorf("%d/%d 轮出现重复注入", dupRounds, rounds)
	}
}

// 八路同时启动: 仍然只能有一路拿到任务槽, 其余七路必须当场被拒。
// 判据是临界区里的"prevTask 非空且没人取消", 与并发路数无关 —— 这条用例存在的
// 意义是把"以后有人把判定挪出临界区、或改成先读标志再认领"这类改动钉红
func TestConcurrentStartEightWay(t *testing.T) {
	const (
		ways   = 8
		rounds = 30
	)
	for i := 0; i < rounds; i++ {
		inj := newFakeInjector()
		svc := newTestService(inj, &fakeClipboard{}, noSleep)
		// 第一路钉在运行中: 后七路入场时它必须还占着任务槽
		releaseTask := make(chan struct{})
		svc.sleep = func(time.Duration) {
			select {
			case <-releaseTask:
			case <-time.After(500 * time.Millisecond): // 兜底: 测试自身出问题时别挂住
			}
		}

		gate := make(chan struct{})
		var wg sync.WaitGroup
		var mu sync.Mutex
		accepted := 0
		for j := 0; j < ways; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-gate
				if _, err := svc.Start("AB", 1, false, false); err == nil {
					mu.Lock()
					accepted++
					mu.Unlock()
				}
			}()
		}
		close(gate)
		wg.Wait()
		close(releaseTask)

		if st := waitTerminal(t, svc); st.Phase != PhaseSuccess {
			t.Fatalf("第 %d 轮终态 = %s/%q, want success", i, st.Phase, st.Message)
		}
		if got := len(inj.calls()); got != 2 {
			t.Fatalf("第 %d 轮注入 %d 次, want 2 (只许一路启动): %v", i, got, inj.calls())
		}
		if accepted != 1 {
			t.Fatalf("第 %d 轮接受了 %d 路, want 1", i, accepted)
		}
	}
}

// 取消落在 Start 的采样窗口里时: 终局必须是"已取消", 且一个字都不许注入。
//
// 两头都要钉住, 因为修的时候踩过两次坑。第一版是 Start 的初态写入没有代数守卫,
// Cancel 晚一步会被它盖回去, 于是没有任务在跑、界面却停在倒计时上; 修好之后又
// 走到另一个极端: 新任务在开工时清掉取消标志, 把采样窗口里那次取消一起抹掉,
// 任务照常注入、终态报"输入完成" —— 界面不卡了, 但用户的取消被吞了。
// 所以只断言 isTerminal 是不够的: 假成功恰好也是终态
func TestCancelDuringStartLeavesNoStuckCountdown(t *testing.T) {
	const rounds = 20
	stuck, swallowed := 0, 0
	for i := 0; i < rounds; i++ {
		inj := newFakeInjector()
		entered := make(chan struct{})
		release := make(chan struct{})
		svc := newTestService(inj, &fakeClipboard{}, noSleep)
		svc.foreground = &firstSampleBlocks{entered: entered, release: release}

		done := make(chan struct{})
		go func() {
			defer close(done)
			svc.Start("AB", 1, false, false)
		}()
		<-entered // Start 已卡在采样里, 还没写倒计时初态
		if _, err := svc.Cancel(); err != nil {
			t.Fatalf("第 %d 轮 Cancel 失败: %v", i, err)
		}
		close(release)
		<-done
		waitIdle(t, svc)

		st := svc.Status()
		switch {
		case !isTerminal(st.Phase):
			// 停在倒计时 = "没有任务在跑却以为在跑", 前端会一直轮询下去
			stuck++
			if stuck <= 3 {
				t.Errorf("第 %d 轮留下 %s/%q, 注入 %d 次", i, st.Phase, st.Message, len(inj.calls()))
			}
		case st.Phase != PhaseCancel:
			// 报了别的终态 = 取消被这轮启动吞掉了
			swallowed++
			if swallowed <= 3 {
				t.Errorf("第 %d 轮取消被吞: 终态 %s/%q, 注入 %v", i, st.Phase, st.Message, inj.calls())
			}
		}
		if calls := inj.calls(); len(calls) != 0 {
			swallowed++
			if swallowed <= 3 {
				t.Errorf("第 %d 轮取消后仍有注入: %v", i, calls)
			}
		}
	}
	if stuck > 0 {
		t.Errorf("%d/%d 轮留下未终止的状态", stuck, rounds)
	}
	if swallowed > 0 {
		t.Errorf("%d 处取消被吞(终态不是 cancel, 或取消后仍有注入)", swallowed)
	}
}

// 被取代的任务等上一任务超时后, 不许把它那句"启动失败"盖到在途新任务头上。
//
// 这段终态写入必须拿本任务自己的代数当守卫; 曾经写成 s.taskGen.Load(),
// 那是拿自己和自己比、恒真, 于是退位的任务照样能改写当前状态: 前端在终止态
// 停止轮询, 用户看到"启动失败"而文本其实已经送进目标窗口(复核时实测复现过)
func TestSupersededTaskTimeoutDoesNotOverwriteNewerTask(t *testing.T) {
	// T1 停在它自己的第一个等待点上(采样不再卡住, 免得把后面任务的初态采样
	// 一起堵在锁里)。于是 T1 既不释放任务槽, 也看不到取消
	releaseOld := make(chan struct{})
	sleeping := make(chan struct{}, 1)
	svc := newTestService(newFakeInjector(), &fakeClipboard{}, func(time.Duration) {
		select {
		case sleeping <- struct{}{}:
		default:
		}
		<-releaseOld
	})

	if _, err := svc.Start("甲", 1, false, false); err != nil {
		t.Fatalf("T1 Start 失败: %v", err)
	}
	<-sleeping

	// 取消 T1, 再连开两个任务: 槽位还被 T1 占着, 所以两次启动都得排队;
	// T2 会等满 yieldDeadline 才认输, 而 T3 紧跟着把 T2 顶掉
	if _, err := svc.Cancel(); err != nil {
		t.Fatalf("Cancel 失败: %v", err)
	}
	if _, err := svc.Start("乙", 1, false, false); err != nil {
		t.Fatalf("T2 Start 失败: %v", err)
	}

	// T2 会等满 yieldDeadline 才认输; 那期间槽位归 T2, 它的超时文案本该落到
	// 状态上(它就是当前任务)。先确认它确实写了, 这同时说明这条路径活着
	deadline := time.Now().Add(yieldDeadline + time.Second)
	for time.Now().Before(deadline) {
		if svc.Status().Message == msgPreviousTaskStuck {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := svc.Status().Message; got != msgPreviousTaskStuck {
		t.Fatalf("T2 没等到超时: 状态 = %s/%q", svc.Status().Phase, got)
	}

	// 关键一步: 再取消一次把 T2 那一代变成"过代", 然后让它以过代身份再去写
	// 终态。旧缺陷用"当前代数"当守卫, 那是自比较、恒真, 退位任务的这句错话
	// 会盖到新任务头上; 正确的守卫只认它自己那一代, 应当整个丢弃
	if _, err := svc.Cancel(); err != nil {
		t.Fatalf("第二次 Cancel 失败: %v", err)
	}
	before := *svc.Status()
	svc.storeStatus(2, &TypingStatus{Phase: PhaseError, Message: msgPreviousTaskStuck, Progress: -1})
	if after := svc.Status(); *after != before {
		t.Errorf("过代任务的状态写入没被丢弃: %s/%q → %s/%q", before.Phase, before.Message, after.Phase, after.Message)
	}

	close(releaseOld)
	waitIdle(t, svc)
}

// 运行中 Start 拒绝重入, 且拒绝不影响在途任务
func TestStartRejectsReentryWhileRunning(t *testing.T) {
	inj := newFakeInjector()
	release := make(chan struct{})
	svc := newTestService(inj, &fakeClipboard{}, blockSleep(release))

	if _, err := svc.Start("AB", 3, false, false); err != nil {
		t.Fatalf("首次 Start 失败: %v", err)
	}
	waitRunning(t, svc)

	if _, err := svc.Start("CD", 1, false, false); err == nil {
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

	if _, err := svc.Start("中文", 5, false, false); err != nil {
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

// 任务已经跑完(结局已写)之后再点取消: "已取消"是句假话 —— 内容早已全部送达。
// 必须保留原来的结局, 否则用户以为没打完, 再点一次启动就把同一段文本打了两遍
func TestCancelDoesNotOverwriteFinishedTask(t *testing.T) {
	inj := newFakeInjector()
	svc := newTestService(inj, &fakeClipboard{}, noSleep)

	if _, err := svc.Start("AB", 1, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	done := waitTerminal(t, svc)
	if done.Phase != PhaseSuccess || done.Message != msgDone {
		t.Fatalf("前置条件不成立: 终态 = %s/%q, want success/%q", done.Phase, done.Message, msgDone)
	}
	// 写完结局到腾空任务槽之间也算"已经跑完", 判据取状态而不是任务槽
	waitIdle(t, svc)

	if _, err := svc.Cancel(); err != nil {
		t.Fatalf("Cancel 失败: %v", err)
	}
	if st := svc.Status(); *st != done {
		t.Errorf("已完成的结局被取消盖掉了: %s/%q → %s/%q", done.Phase, done.Message, st.Phase, st.Message)
	}
	if calls := inj.calls(); len(calls) != 2 {
		t.Errorf("取消不该再产生注入, got %v", calls)
	}
}

// 失败结局同理: 具体原因不许被一句"已取消"抹掉
func TestCancelKeepsFailureReason(t *testing.T) {
	inj := newFakeInjector()
	inj.failAfter = 0 // 第一次注入就被拒
	svc := newTestService(inj, &fakeClipboard{}, noSleep)

	if _, err := svc.Start("AB", 1, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	done := waitTerminal(t, svc)
	if done.Phase != PhaseError {
		t.Fatalf("前置条件不成立: 终态 = %s/%q, want error", done.Phase, done.Message)
	}
	waitIdle(t, svc)

	if _, err := svc.Cancel(); err != nil {
		t.Fatalf("Cancel 失败: %v", err)
	}
	if st := svc.Status(); *st != done {
		t.Errorf("失败原因被取消盖掉了: %s/%q → %s/%q", done.Phase, done.Message, st.Phase, st.Message)
	}
}

// 上面两条是例外, 别顺手把正常路径也"优化"掉: 取消一个真正在跑的任务,
// 仍然要覆盖倒计时并播报"已取消"
func TestCancelStillOverwritesRunningTask(t *testing.T) {
	inj := newFakeInjector()
	release := make(chan struct{})
	svc := newTestService(inj, &fakeClipboard{}, blockSleep(release))

	if _, err := svc.Start("AB", 5, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	waitRunning(t, svc)

	if _, err := svc.Cancel(); err != nil {
		t.Fatalf("Cancel 失败: %v", err)
	}
	if st := svc.Status(); st.Phase != PhaseCancel || st.Message != msgCancelled {
		t.Errorf("在途任务被取消后状态 = %s/%q, want cancel/%q", st.Phase, st.Message, msgCancelled)
	}
	close(release)
	waitIdle(t, svc)
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

	if _, err := svc.Start("AB\r\nCD", 1, false, false); err != nil {
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

// 文本直投: 字符(含 Tab)走文本层(T:), 无需 Esc(文本层无键义可劫持);
// 换行无法走文本层, 仍按键注入但前面必须补一次 Esc 关闭补全弹窗
func TestTextDirectRoutesViaTextLayer(t *testing.T) {
	inj := newFakeInjector()
	svc := newTestService(inj, &fakeClipboard{}, noSleep)

	if _, err := svc.Start("a b\tc\nd", 1, false, true); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if st := waitTerminal(t, svc); st.Phase != PhaseSuccess {
		t.Fatalf("终态 phase = %s, want success", st.Phase)
	}

	// 序列: T:a, T:空格, T:b, T:Tab, T:c, Esc, E, T:d
	want := []string{"T:a", "T: ", "T:b", "T:\t", "T:c", "ESC", "E", "T:d"}
	if got := inj.calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("注入序列 = %q, want %q (字符含 Tab 走文本层, 换行前有 Esc)", got, want)
	}
}

// 文本直投关闭(默认): 仍走按键层, 无任何 Esc
func TestTextDirectOffUsesKeyLayer(t *testing.T) {
	inj := newFakeInjector()
	svc := newTestService(inj, &fakeClipboard{}, noSleep)

	if _, err := svc.Start("a b\n", 1, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if st := waitTerminal(t, svc); st.Phase != PhaseSuccess {
		t.Fatalf("终态 phase = %s, want success", st.Phase)
	}

	want := []string{"r:a", "r: ", "r:b", "E"}
	if got := inj.calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("注入序列 = %q, want %q (文本直投关闭时不应出现 T:/Esc)", got, want)
	}
}

// 文本直投下 Esc 被拒: 立即中止并报注入中断(回车/Tab 依赖 Esc 先行)
func TestTextDirectEscapeRejected(t *testing.T) {
	inj := newFakeInjector()
	inj.failAfter = 2 // T:a、T:空格 通过, 其后的 Esc 被拒
	svc := newTestService(inj, &fakeClipboard{}, noSleep)

	if _, err := svc.Start("a \n", 1, false, true); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseError {
		t.Fatalf("Esc 被拒时终态 phase = %s, want error", st.Phase)
	}
	if st.Message != msgPartialSendInput {
		t.Errorf("终态文案 = %q, want %q", st.Message, msgPartialSendInput)
	}
	// "a \n" → T:a, T:空格, Esc(被拒) → 序列止于 T:空格
	want := []string{"T:a", "T: "}
	if got := inj.calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("注入序列 = %v, want %v (Esc 被拒后不应继续)", got, want)
	}
}

// 中文路径: 快照 → 写入注入文本 → 粘贴 → 恢复 的调用顺序与恢复语义
func TestChineseTypesViaClipboard(t *testing.T) {
	inj := newFakeInjector()
	cb := &fakeClipboard{text: "用户原文本"}
	svc := newTestService(inj, cb, noSleep)

	if _, err := svc.Start("你好", 1, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if st := waitTerminal(t, svc); st.Phase != PhaseSuccess {
		t.Fatalf("终态 phase = %s, want success", st.Phase)
	} else if st.Message != "输入完成" {
		t.Errorf("剪贴板恢复成功时终态文案 = %q, want 输入完成", st.Message)
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
	if _, err := svc.Start("旧任务文本", 5, false, false); err != nil {
		t.Fatalf("首次 Start 失败: %v", err)
	}
	waitRunning(t, svc)

	if _, err := svc.Cancel(); err != nil {
		t.Fatalf("Cancel 失败: %v", err)
	}
	// 取消收尾尚未完成(旧任务仍占着任务槽)时立即重启
	if _, err := svc.Start("新", 2, false, false); err != nil {
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

// waitIdle 等待任务腾空任务槽
func waitIdle(t *testing.T, svc *TypingService) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		svc.mu.Lock()
		taken := svc.prevTask != nil
		svc.mu.Unlock()
		if !taken {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("任务未及时腾空任务槽")
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
	cb.snap = []ClipboardFormat{{Fmt: cfUnicodeText, Data: raw}}
}

// 逐字符注入被系统拒绝(UIPI 等): 立即中止, 终态给出可读原因, 不虚报"输入完成"
func TestSendInputRejectedDuringTyping(t *testing.T) {
	inj := newFakeInjector()
	inj.failAfter = 2 // A B 通过, C 起被拒
	svc := newTestService(inj, &fakeClipboard{}, noSleep)

	if _, err := svc.Start("ABCDE", 1, false, false); err != nil {
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

	if _, err := svc.Start("你好", 1, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseError {
		t.Fatalf("粘贴被拒时终态 phase = %s, want error", st.Phase)
	}
	if st.Message != msgPasteRejected {
		t.Errorf("终态文案 = %q, want %q (粘贴被拒要给出权限原因, 不能退化成通用文案)", st.Message, msgPasteRejected)
	}
	if got, want := cb.ops(), []string{"snap", "set", "restore"}; !reflect.DeepEqual(got, want) {
		t.Errorf("剪贴板操作序列 = %v, want %v", got, want)
	}
	if got := cb.GetText(); got != "用户原文本" {
		t.Errorf("粘贴被拒后仍应恢复原文本, got %q", got)
	}
}

// 粘贴送达但剪贴板没能换回原内容: 注入本身是成功的, 因此不报"输入失败";
// 但用户原本复制的东西可能已经丢了, 也不许报成"输入完成"
func TestClipboardNotRestoredIsReported(t *testing.T) {
	inj := newFakeInjector()
	cb := &fakeClipboard{text: "用户原文本", restoreFail: true}
	svc := newTestService(inj, cb, noSleep)

	if _, err := svc.Start("你好", 1, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseSuccess {
		t.Fatalf("终态 phase = %s, want success (注入已经送达)", st.Phase)
	}
	if st.Message != msgClipboardNotRestored {
		t.Errorf("终态文案 = %q, want %q", st.Message, msgClipboardNotRestored)
	}
	if got, want := inj.calls(), []string{"V"}; !reflect.DeepEqual(got, want) {
		t.Errorf("注入序列 = %v, want %v", got, want)
	}
	// 恢复失败 = 用户原内容丢失, 终态必须带上目标窗口, 便于判断注入落在了哪
	if st.TargetWindow != "记事本" {
		t.Errorf("终态目标窗口 = %q, want 记事本", st.TargetWindow)
	}
}

// 比对时读不到剪贴板: 原内容还在不在无从判断, 必须按"没恢复"上报。
// 旧签名把"读不到"并进"用户已改动"里静默跳过恢复, 于是用户原本复制的东西
// 已经丢了, 收到的却是"输入完成"
func TestUnreadableClipboardIsReported(t *testing.T) {
	inj := newFakeInjector()
	cb := &fakeClipboard{text: "用户原文本", holdsUnreadable: true}
	svc := newTestService(inj, cb, noSleep)

	if _, err := svc.Start("你好", 1, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseSuccess {
		t.Fatalf("终态 phase = %s, want success (粘贴已经送达)", st.Phase)
	}
	if st.Message != msgClipboardNotRestored {
		t.Errorf("终态文案 = %q, want %q", st.Message, msgClipboardNotRestored)
	}
	if got, want := inj.calls(), []string{"V"}; !reflect.DeepEqual(got, want) {
		t.Errorf("注入序列 = %v, want %v", got, want)
	}
}

// 上一任务迟迟不让位: 重启在 yieldDeadline 后认输, 播报终态, 且不得注入任何
// 内容。旧任务停在它的第一个等待点上, 所以它既不释放任务槽, 也来不及看到取消
func TestStartDeadlineWhenPreviousTaskStuck(t *testing.T) {
	const timeoutMsg = "启动失败：上一任务未能及时退出"
	inj := newFakeInjector()
	releaseOld := make(chan struct{})
	sleeping := make(chan struct{}, 1)
	svc := newTestService(inj, &fakeClipboard{}, func(time.Duration) {
		select {
		case sleeping <- struct{}{}:
		default:
		}
		<-releaseOld
	})

	// 旧任务: 停在第一个等待点上, 任务槽被它占着
	if _, err := svc.Start("旧任务", 1, false, false); err != nil {
		t.Fatalf("首次 Start 失败: %v", err)
	}
	<-sleeping

	// 用户先取消: Cancel 必须立刻返回, 不能被等不到让位的重启拖住
	cancelStart := time.Now()
	if _, err := svc.Cancel(); err != nil {
		t.Fatalf("Cancel 失败: %v", err)
	}
	if waited := time.Since(cancelStart); waited > yieldDeadline/2 {
		t.Errorf("Cancel 被拖住了: 耗时 %v", waited)
	}

	// 取消之后立刻重启: 这次启动本身应当成功(它拿到了任务槽), 但旧任务还停着
	// 不让位, 所以它只能在 yieldDeadline 之后认输
	if _, err := svc.Start("新任务", 1, false, false); err != nil {
		t.Fatalf("取消后应能立即重启: %v", err)
	}
	deadline := time.Now().Add(yieldDeadline + time.Second)
	for !isTerminal(svc.Status().Phase) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	// 超时必须播报成终态: 只回错误不播报的话, 界面会停在上一次的倒计时里,
	// 而前端只在终态才停止轮询
	if st := svc.Status(); st.Phase != PhaseError || st.Message != timeoutMsg {
		t.Fatalf("超时后状态 = %s/%q, want error/%q", st.Phase, st.Message, timeoutMsg)
	}
	if calls := inj.calls(); len(calls) != 0 {
		t.Errorf("超时任务不应注入任何内容, got %v", calls)
	}

	// 放行旧任务: 它已过代并自行退出, 迟到的写入不得覆盖上面的错误状态
	close(releaseOld)
	waitIdle(t, svc)
	time.Sleep(20 * time.Millisecond) // 给过代任务的收尾留出窗口
	final := svc.Status()
	if final.Phase != PhaseError || final.Message != timeoutMsg {
		t.Errorf("旧任务收尾覆盖了当前状态: %s/%q", final.Phase, final.Message)
	}
	if calls := inj.calls(); len(calls) != 0 {
		t.Errorf("过代任务不应注入任何内容, got %v", calls)
	}
}

// 快照不完整(有的格式没能照抄下来): 不碰剪贴板, 退回逐字符把字送出去。
// 恢复流程会先清空剪贴板, 拿残缺的快照去恢复等于把没抄到的格式永久销毁
func TestIncompleteSnapshotFallsBackToTyping(t *testing.T) {
	inj := newFakeInjector()
	cb := &fakeClipboard{text: "用户原本复制的东西", incomplete: true}
	svc := newTestService(inj, cb, noSleep)

	if _, err := svc.Start("中文", 1, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseSuccess {
		t.Fatalf("终态 = %s/%q, want success(文本照样送到)", st.Phase, st.Message)
	}
	// 逐字符路径: 两个汉字各注入一次
	if got, want := inj.calls(), []string{"r:中", "r:文"}; !reflect.DeepEqual(got, want) {
		t.Errorf("注入序列 = %v, want %v", got, want)
	}
	// 只快照过, 没写过也没恢复过: 用户剪贴板里的东西一个字都没动
	if got, want := cb.ops(), []string{"snap"}; !reflect.DeepEqual(got, want) {
		t.Errorf("剪贴板操作序列 = %v, want %v", got, want)
	}
	if got := cb.GetText(); got != "用户原本复制的东西" {
		t.Errorf("剪贴板被改动: %q", got)
	}
}

// 剪贴板打开失败(原状态未知): 同样不碰, 退回逐字符
func TestSnapshotOpenFailureFallsBackToTyping(t *testing.T) {
	inj := newFakeInjector()
	cb := &fakeClipboard{text: "用户原本复制的东西", snapFail: true}
	svc := newTestService(inj, cb, noSleep)

	if _, err := svc.Start("你好", 1, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseSuccess {
		t.Fatalf("终态 = %s/%q, want success", st.Phase, st.Message)
	}
	if got, want := inj.calls(), []string{"r:你", "r:好"}; !reflect.DeepEqual(got, want) {
		t.Errorf("注入序列 = %v, want %v", got, want)
	}
	if got, want := cb.ops(), []string{"snap"}; !reflect.DeepEqual(got, want) {
		t.Errorf("剪贴板操作序列 = %v, want %v", got, want)
	}
	if got := cb.GetText(); got != "用户原本复制的东西" {
		t.Errorf("剪贴板被改动: %q", got)
	}
}

// 剪贴板写入失败: 恢复未落盘的快照, 报"剪贴板操作失败", 不粘贴
func TestClipboardSetFailureNoInjection(t *testing.T) {
	inj := newFakeInjector()
	cb := &fakeClipboard{text: "原内容", setFail: true}
	seedSnapshot(cb, "原内容")
	svc := newTestService(inj, cb, noSleep)

	if _, err := svc.Start("中文", 1, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseError || st.Message != msgClipboardFailed {
		t.Fatalf("终态 = %s/%q, want error/%q", st.Phase, st.Message, msgClipboardFailed)
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
	snap := cb.Snapshot().Formats

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

// 无内容可输入的文本(空串、只有 \r)在后端当场被拒: 不写倒计时、不占任务槽、
// 不注入。前端也有一道同样的校验, 但那道只是省一次往返 —— 界面能改、能被绕过,
// 判据必须在后端; 否则用户看到的是一个白等的倒计时, 等来的却是报错
func TestEmptyTextRejectedByStart(t *testing.T) {
	for _, text := range []string{"", "\r", "\r\r"} {
		inj := newFakeInjector()
		svc := newTestService(inj, &fakeClipboard{}, noSleep)
		if _, err := svc.Start(text, 1, false, false); err == nil {
			t.Fatalf("Start(%q) 应被拒绝", text)
		} else if err.Error() != msgNothingToType {
			t.Errorf("Start(%q) 的错误文案 = %q, want %q", text, err.Error(), msgNothingToType)
		}
		// 拒绝必须发生在写状态之前: 状态仍是构造时的 idle, 界面不会出现倒计时
		if st := svc.Status(); st.Phase != PhaseIdle || st.Message != "" {
			t.Errorf("Start(%q) 被拒后状态 = %s/%q, 不该写倒计时", text, st.Phase, st.Message)
		}
		if calls := inj.calls(); len(calls) != 0 {
			t.Errorf("Start(%q) 被拒后不应注入, got %v", text, calls)
		}
	}
}

// 只含空白(空格/Tab/换行)的文本是合法的输入内容: 空格真的敲得出去, 一个换行
// 就是一次回车。前端曾用 text.trim() 把它连同空文本一起拦掉, 两层结论不一致
func TestWhitespaceOnlyTextIsTyped(t *testing.T) {
	inj := newFakeInjector()
	svc := newTestService(inj, &fakeClipboard{}, noSleep)
	if _, err := svc.Start(" \t\n ", 1, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseSuccess || st.Message != msgDone {
		t.Fatalf("终态 = %s/%q, want success/%q", st.Phase, st.Message, msgDone)
	}
	if got, want := inj.calls(), []string{"r: ", "r:\t", "E", "r: "}; !reflect.DeepEqual(got, want) {
		t.Errorf("注入序列 = %v, want %v", got, want)
	}
}

// 目标窗口语义: 终态携带执行时锁定的目标, 执行阶段预览不再清空
func TestTargetWindowLockedIntoFinalStatus(t *testing.T) {
	inj := newFakeInjector()
	svc := newTestService(inj, &fakeClipboard{}, noSleep)
	if _, err := svc.Start("你好", 1, false, false); err != nil {
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
	svc := NewTypingService(inj, cb, fakeForeground{title: "Type 测试", self: true})
	svc.sleep = noSleep
	if _, err := svc.Start("你好", 1, false, false); err != nil {
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

// 目标预览跟随当前前台窗口(不做任何排除): 用户聚焦谁就显示谁,
// 回看 Type 期间预览即 Type 自身; 执行后锁定为结束那一刻的窗口
func TestCountdownPreviewFollowsCurrentForeground(t *testing.T) {
	fg := &switchableForeground{id: 1, title: "记事本"}
	inj := newFakeInjector()
	var mu sync.Mutex
	var history []TypingStatus
	var sleeps int
	var svc *TypingService
	svc = NewTypingService(inj, &fakeClipboard{}, fg)
	svc.sleep = func(time.Duration) {
		sleeps++
		switch sleeps {
		case 1:
			fg.set(2, "浏览器", false) // 用户切到真正的目标
		case 2:
			fg.set(3, "Type 测试", true) // 用户中途回看 Type
		case 3:
			fg.set(2, "浏览器", false) // 最后一秒切回目标
		}
		mu.Lock()
		history = append(history, *svc.Status())
		mu.Unlock()
	}

	if _, err := svc.Start("你好", 3, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseSuccess {
		t.Fatalf("终态 phase = %s, want success", st.Phase)
	}
	if st.TargetWindow != "浏览器" {
		t.Errorf("终态目标 = %q, want 浏览器(执行时锁定的前台窗口)", st.TargetWindow)
	}

	mu.Lock()
	defer mu.Unlock()
	var sawSelf bool
	for i := range history {
		if history[i].Phase == PhaseCountdown && history[i].TargetWindow == "Type 测试" {
			sawSelf = true
		}
	}
	if !sawSelf {
		t.Error("回看 Type 期间的预览应为 Type 自身(不做排除), 未捕获到")
	}
}

// ─── 焦点漂移防护 ─────────────────────────────────────

// 倒计时节拍: 100ms 一拍(每秒 10 拍), 每拍采样一次前台窗口, 可见状态
// 只在秒边界更新 —— 采样与写状态分离, 总时长仍是 delay 秒
func TestCountdownTicksAndSecondBoundaries(t *testing.T) {
	fg := &samplingForeground{}
	inj := newFakeInjector()
	var mu sync.Mutex
	var history []TypingStatus
	var sleeps int
	var svc *TypingService
	svc = NewTypingService(inj, &fakeClipboard{}, fg)
	svc.sleep = func(time.Duration) {
		sleeps++
		mu.Lock()
		history = append(history, *svc.Status())
		mu.Unlock()
	}

	// 单字符文本: 注入一次、多一次漂移守卫采样与一次字符间隔, 账要跟着算
	if _, err := svc.Start("A", 3, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if st := waitTerminal(t, svc); st.Phase != PhaseSuccess {
		t.Fatalf("终态 phase = %s, want success", st.Phase)
	}

	// 30 拍倒计时 + 锁定前那一次 150ms 稳定等待 + 一个字符的间隔
	if sleeps != 32 {
		t.Errorf("等待次数 = %d, want 32 (30 拍倒计时 + 1 次稳定等待 + 1 次字符间隔)", sleeps)
	}
	// 采样: Start 初态 1 次 + 倒计时每拍 1 次 ×30 + 锁定 1 次 + 逐字符的漂移守卫 1 次
	if got := fg.count(); got != 33 {
		t.Errorf("采样次数 = %d, want 33 (初态 1 + 每拍 1×30 + 锁定 1 + 漂移守卫 1)", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(history) < 30 {
		t.Fatalf("倒计时期间的等待记录只有 %d 条, want >= 30", len(history))
	}
	// 秒数每 10 拍降一档: 3 → 2 → 1, 目标窗口全程不变
	for i, h := range history[:30] {
		want := 3 - i/10
		if h.Phase != PhaseCountdown || h.SecondsLeft != want {
			t.Fatalf("第 %d 拍状态 = %s/%d, want countdown/%d", i, h.Phase, h.SecondsLeft, want)
		}
		if h.TargetWindow != "记事本" {
			t.Fatalf("第 %d 拍目标窗口 = %q, want 记事本", i, h.TargetWindow)
		}
	}
}

// 切窗预览: 前台一变, 下一拍预览就更新, 不必等秒边界
func TestCountdownPreviewFollowsSwitchWithinOneTick(t *testing.T) {
	fg := &switchableForeground{id: 1, title: "记事本"}
	inj := newFakeInjector()
	var mu sync.Mutex
	var history []TypingStatus
	var sleeps int
	var svc *TypingService
	svc = NewTypingService(inj, &fakeClipboard{}, fg)
	svc.sleep = func(time.Duration) {
		sleeps++
		if sleeps == 3 { // 在第一秒内切走
			fg.set(2, "浏览器", false)
		}
		mu.Lock()
		history = append(history, *svc.Status())
		mu.Unlock()
	}

	if _, err := svc.Start("A", 3, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if st := waitTerminal(t, svc); st.Phase != PhaseSuccess {
		t.Fatalf("终态 phase = %s, want success", st.Phase)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(history) < 5 {
		t.Fatalf("等待记录过少: %d 条", len(history))
	}
	for i := 0; i < 3; i++ {
		if got := history[i].TargetWindow; got != "记事本" {
			t.Errorf("切换前的第 %d 拍预览 = %q, want 记事本", i, got)
		}
	}
	// 切换发生在第 3 拍之后: 第 4 拍预览已是新窗口, 秒数还没到边界不动
	if got := history[3].TargetWindow; got != "浏览器" {
		t.Errorf("切换后第一拍预览 = %q, want 浏览器", got)
	}
	if got := history[3].SecondsLeft; got != 3 {
		t.Errorf("预览更新不应顺带跳秒数, got %d, want 3", got)
	}
}

// 取消响应: 下一拍(100ms)即被察觉, 不必等到秒边界
func TestCancelDuringCountdownDetectedWithinOneTick(t *testing.T) {
	inj := newFakeInjector()
	var svc *TypingService
	var sleeps int
	svc = newTestService(inj, &fakeClipboard{}, func(time.Duration) {
		sleeps++
		if sleeps == 3 {
			if _, err := svc.Cancel(); err != nil {
				t.Errorf("Cancel 失败: %v", err)
			}
		}
	})

	// 9 秒倒计时: 若按秒检查取消, 任务会一路跑满 90 拍
	if _, err := svc.Start("你好", 9, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseCancel {
		t.Fatalf("终态 phase = %s, want cancel", st.Phase)
	}
	if sleeps != 3 {
		t.Errorf("取消应在下一拍生效(共 3 次等待), got %d 次", sleeps)
	}
	if calls := inj.calls(); len(calls) != 0 {
		t.Errorf("取消后不应有任何注入, got %v", calls)
	}
}

// 逐字符路径: 中途切窗立即停止, 之后的字符一个都不再注入, 终态报出已输入字数
func TestTypingStopsWhenTargetSwitches(t *testing.T) {
	inj := newFakeInjector()
	fg := &switchableForeground{id: 1, title: "记事本"}
	var svc *TypingService
	var sleeps int
	svc = NewTypingService(inj, &fakeClipboard{}, fg)
	svc.sleep = func(time.Duration) {
		sleeps++
		// 倒计时 10 拍 + 150ms 稳定等待 1 次 = 11 次; 第 12/13 次分别是
		// 第一个/第二个字符后的间隔 —— 在第二个字符之后切走
		if sleeps == 13 {
			fg.set(2, "浏览器", false)
		}
	}

	if _, err := svc.Start("ABCDE", 1, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseError {
		t.Fatalf("漂移后终态 phase = %s, want error", st.Phase)
	}
	if want := msgTargetSwitchedTyped(2); st.Message != want {
		t.Errorf("终态文案 = %q, want %q", st.Message, want)
	}
	if st.Progress != -1 {
		t.Errorf("失败终态 progress 应为 -1, got %d", st.Progress)
	}
	if st.TargetWindow != "记事本" {
		t.Errorf("终态目标窗口 = %q, want 记事本(锁定的那个, 不是切过去的)", st.TargetWindow)
	}
	want := []string{"r:A", "r:B"}
	if got := inj.calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("注入序列 = %v, want %v (切走后不应继续注入)", got, want)
	}
	// 终态之后不得再补注入
	time.Sleep(20 * time.Millisecond)
	if got := inj.calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("到达终态后仍在注入: %v", got)
	}
}

// 漂移判定用标识而不是标题: 同一个窗口的标题变化(浏览器切标签一类)
// 不应中断输入
func TestTitleChangeOnSameWindowDoesNotStopTyping(t *testing.T) {
	inj := newFakeInjector()
	fg := &switchableForeground{id: 1, title: "记事本"}
	var svc *TypingService
	var sleeps int
	svc = NewTypingService(inj, &fakeClipboard{}, fg)
	svc.sleep = func(time.Duration) {
		sleeps++
		if sleeps == 13 { // 键入途中标题变了, 窗口标识还是同一个
			fg.set(1, "记事本 - 已修改", false)
		}
	}

	if _, err := svc.Start("ABCDE", 1, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if st := waitTerminal(t, svc); st.Phase != PhaseSuccess {
		t.Fatalf("同一窗口改标题不应中断输入, 终态 = %s/%q", st.Phase, st.Message)
	}
	want := []string{"r:A", "r:B", "r:C", "r:D", "r:E"}
	if got := inj.calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("注入序列 = %v, want %v", got, want)
	}
}

// 剪贴板路径: 写入剪贴板之后、粘贴之前切走 —— 不粘贴, 剪贴板照旧恢复
func TestClipboardStopsWhenTargetSwitchesBeforePaste(t *testing.T) {
	inj := newFakeInjector()
	fg := &switchableForeground{id: 1, title: "记事本"}
	cb := &fakeClipboard{text: "用户原文本"}
	var svc *TypingService
	var sleeps int
	svc = NewTypingService(inj, cb, fg)
	svc.sleep = func(time.Duration) {
		sleeps++
		// 倒计时 10 拍 + 稳定等待 1 次 → 第 12 次是写入剪贴板后的 100ms 等待
		if sleeps == 12 {
			fg.set(2, "浏览器", false)
		}
	}

	if _, err := svc.Start("你好", 1, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseError || st.Message != msgTargetSwitchedIdle {
		t.Fatalf("终态 = %s/%q, want error/%q", st.Phase, st.Message, msgTargetSwitchedIdle)
	}
	if calls := inj.calls(); len(calls) != 0 {
		t.Errorf("粘贴前的漂移不应注入任何按键, got %v", calls)
	}
	if got, want := cb.ops(), []string{"snap", "set", "restore"}; !reflect.DeepEqual(got, want) {
		t.Errorf("剪贴板操作序列 = %v, want %v (写过就必须恢复)", got, want)
	}
	if got := cb.GetText(); got != "用户原文本" {
		t.Errorf("恢复后文本 = %q, want 用户原文本", got)
	}
}

// 剪贴板路径: 粘贴的瞬间目标被切走 —— 报"结果无法确认", 剪贴板仍恢复
func TestClipboardReportsUnconfirmedWhenTargetSwitchesAtPaste(t *testing.T) {
	inj := newFakeInjector()
	fg := &switchableForeground{id: 1, title: "记事本"}
	cb := &fakeClipboard{text: "用户原文本"}
	inj.onInject = func(event string) {
		if event == "V" {
			fg.set(2, "浏览器", false)
		}
	}
	svc := NewTypingService(inj, cb, fg)
	svc.sleep = noSleep

	if _, err := svc.Start("你好", 1, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseError || st.Message != msgTargetSwitchedPasted {
		t.Fatalf("终态 = %s/%q, want error/%q", st.Phase, st.Message, msgTargetSwitchedPasted)
	}
	if got, want := inj.calls(), []string{"V"}; !reflect.DeepEqual(got, want) {
		t.Errorf("注入序列 = %v, want %v", got, want)
	}
	if got, want := cb.ops(), []string{"snap", "set", "restore"}; !reflect.DeepEqual(got, want) {
		t.Errorf("剪贴板操作序列 = %v, want %v", got, want)
	}
	if got := cb.GetText(); got != "用户原文本" {
		t.Errorf("恢复后文本 = %q, want 用户原文本", got)
	}
}
