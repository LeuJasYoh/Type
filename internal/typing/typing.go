// ─── 输入任务业务层: 状态机 + 并发守卫 ─────────────────
// 仅依赖本文件定义的 TextInjector/Clipboard/Foreground 接口,
// 平台细节全部下沉到 internal/win32; 状态机时序与用户可见文案是稳定契约。

package typing

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ─── 平台能力接口(消费方定义, Win32 实现见 internal/win32) ──

// TextInjector 字符注入能力。
// 五个方法一律返回"本次注入是否被系统接受": UIPI 拦截(目标权限更高)、工作站锁定
// 或切到安全桌面时 SendInput 整体失败且不产生按键, 不能把静默失败当成输入完成
type TextInjector interface {
	SendRune(r rune) bool // 按键层字符注入, 含全角标点 WM_CHAR 绕行
	SendText(r rune) bool // 文本层字符注入(WM_CHAR 直投), 见 docs/invariants.md「文本直投」
	SendEnter() bool
	SendEscape() bool // 关闭目标编辑器的补全弹窗
	SendPaste() bool  // Ctrl+V
}

// ClipboardFormat 剪贴板单一格式的原始字节快照
type ClipboardFormat struct {
	Fmt  uint32
	Data []byte
}

// ClipboardSnapshot 一次剪贴板快照。Formats 为 nil 表示剪贴板打开失败或根本
// 没读到任何格式(原状态未知, 无从恢复); 空切片表示剪贴板原本为空。
// Complete 是唯一判据; 残缺快照不许拿去恢复 —— 见 docs/invariants.md「其它不变量」
type ClipboardSnapshot struct {
	Formats  []ClipboardFormat
	Complete bool
}

// UnsafeToRestore 这份快照是否不能拿去覆盖剪贴板。
// 空剪贴板(nil 切片但 Complete)可以恢复, 那是"恢复成空"而不是"没恢复"
func (s ClipboardSnapshot) UnsafeToRestore() bool {
	return !s.Complete
}

// Clipboard 剪贴板能力
type Clipboard interface {
	SetText(text string) bool
	GetText() string
	// HoldsText 剪贴板当前文本是否就是 text, 以及这次比对有没有得出结论。
	// 第二个返回值不许省, 两种情形的处置相反 —— 见 docs/invariants.md「其它不变量」
	HoldsText(text string) (holds bool, known bool)
	// Snapshot 取剪贴板全部格式。读不全时必须如实把 Complete 报成 false ——
	// 见 docs/invariants.md「其它不变量」
	Snapshot() ClipboardSnapshot
	// RestoreSnapshotRaw 无条件写回快照(调用方需确认剪贴板未被用户改动),
	// 返回是否真的恢复成功: 失败意味着用户原本的内容可能已丢失, 调用方
	// 必须说出来而不是静默略过
	RestoreSnapshotRaw(snap []ClipboardFormat) bool
}

// TargetID 目标窗口的平台无关标识(不透明): 业务层只做相等比较, 不解释内容。
// Win32 侧就是顶层前台窗口句柄; 判定"还是不是同一个窗口"只看标识 ——
// 见 docs/invariants.md「焦点锁定与漂移防护」
type TargetID uintptr

// ForegroundSample 一次前台窗口采样的结果
type ForegroundSample struct {
	ID    TargetID // 顶层前台窗口标识; 无前台窗口时为 0
	Title string   // 该窗口标题(供预览与终态展示)
	Self  bool     // 是否为本程序自身窗口
}

// Foreground 前台窗口探测
type Foreground interface {
	// Sample 读取当前前台窗口。三要素必须取自同一次读取, 分多次读会在间隙
	// 切换时得到互相矛盾的组合 —— 见 docs/invariants.md「焦点锁定与漂移防护」
	Sample() ForegroundSample
}

// ─── 输入状态（前端轮询读取）───────────────────────────

type TypingPhase string

const (
	PhaseIdle      TypingPhase = "idle"
	PhaseCountdown TypingPhase = "countdown"
	PhaseTyping    TypingPhase = "typing"
	PhaseSuccess   TypingPhase = "success"
	PhaseError     TypingPhase = "error"
	PhaseCancel    TypingPhase = "cancel"
)

type TypingStatus struct {
	Phase        TypingPhase `json:"phase"`
	Message      string      `json:"message"`
	Progress     int         `json:"progress"`     // 0-100，-1 表示隐藏
	SecondsLeft  int         `json:"secondsLeft"`  // 倒计时剩余
	TargetWindow string      `json:"targetWindow"` // 前台窗口标题, 倒计时期间逐秒刷新; 执行后为锁定的实际注入目标
}

// ─── 用户可见文案(新增) ───────────────────────────────
// 逐字冻结; 各条代表不同的落点事实, 不许合并 —— 字面量表在 contract_test.go,
// 清单与取舍见 docs/behavior-contract.md「行为契约（冻结，改动需双端同步）」

const (
	// msgPartialSendInput 注入中途被系统拒绝(绝大多数是 UIPI: 目标权限更高)
	msgPartialSendInput = "输入中断：目标窗口拒绝了模拟按键，可能其权限高于 Type"
	// msgPasteRejected 剪贴板写入成功但 Ctrl+V 未被接受
	msgPasteRejected = "粘贴未生效：目标窗口拒绝了模拟按键，可能其权限高于 Type"
	// msgClipboardFailed 剪贴板打开/写入本身失败, 尚未走到粘贴
	msgClipboardFailed = "剪贴板操作失败"
	// msgTargetSwitchedIdle 漂移中止, 且一个字都没送出去
	msgTargetSwitchedIdle = "输入中断：目标窗口已切换，未输入任何内容"
	// msgTargetSwitchedPasted 粘贴已发出而目标随即改变: 落点无法确认
	msgTargetSwitchedPasted = "输入中断：目标窗口已切换，粘贴结果无法确认"
	// msgClipboardNotRestored 已送达但剪贴板没换回原内容(两个都成立的事实)
	msgClipboardNotRestored = "输入完成，但剪贴板未恢复，原内容可能已丢失"
)

// msgTargetSwitchedTyped 逐字符路径中途检测到目标窗口切换。已注入的部分
// 无法撤回, 报出已输入的字数, 让用户知道目标窗口里留下了多少内容
func msgTargetSwitchedTyped(n int) string {
	return fmt.Sprintf("输入中断：目标窗口已切换，已输入 %d 字", n)
}

// ─── 用户可见文案(冻结清单的其余部分) ─────────────────
// 逐字固定; 新增要登记 —— 见 docs/behavior-contract.md「行为契约（冻结，改动需双端同步）」。
// msgCountdownFormat 留在本包非测试文件里即可: cmd/type/contract_test.go 扫包目录核这个名字
const (
	// 倒计时: 逐拍刷新秒数, 也是用户盯得最久的一句
	msgCountdownFormat = "剩余 %d 秒 — 请聚焦目标窗口..."
	// 逐字符进度: 分母是实际注入次数(已剔除 \r)
	msgProgressFormat = "正在逐字符输入 %d / %d ..."
	// 剪贴板路径的开工提示
	msgClipboardStart = "检测到中文，正在操作剪贴板..."
	// 终态
	msgDone              = "输入完成"
	msgNothingToType     = "无内容可输入"
	msgCancelled         = "已取消"
	msgGenericFailure    = "输入失败"
	msgAlreadyRunning    = "已有输入任务在运行中，请先取消或等待完成"
	msgPreviousTaskStuck = "启动失败：上一任务未能及时退出"
	// 焦点仍在 Type 自身: 与"漂移"不同, 这是一开始就没切过去
	msgFocusStayedOnSelf = "未切换到目标窗口：倒计时结束时焦点仍在 Type，请重新启动后聚焦目标窗口"
)

// countdownMessage 倒计时文案
func countdownMessage(sec int) string { return fmt.Sprintf(msgCountdownFormat, sec) }

// progressMessage 逐字符进度文案
func progressMessage(done, total int) string { return fmt.Sprintf(msgProgressFormat, done, total) }

// ─── 输入任务时序(调参集中处) ─────────────────────────
// 状态机里所有等待与节拍只有这一处定义, 各处按名字取用。改动前先想清楚
// 对取消响应与字符间隔的影响(见 docs/invariants.md「焦点锁定与漂移防护」)。
// 同值不同义的不要合并: 100ms 在倒计时节拍与剪贴板稳定等待上各出现一次,
// 两处可以独立调整
const (
	// countdownTick 倒计时节拍(100ms 一拍): 取消与切窗的响应缩到一拍内 ——
	// 见 docs/invariants.md「焦点锁定与漂移防护」
	countdownTick = 100 * time.Millisecond
	// countdownTicksPerSec 每秒拍数, 由节拍推出
	countdownTicksPerSec = int(time.Second / countdownTick)

	// lockSettleWait 倒计时结束到采样并锁定目标之间的稳定等待
	lockSettleWait = 150 * time.Millisecond

	// yieldDeadline 等上一任务让位的上限,
	// 超过则报"启动失败：上一任务未能及时退出"
	yieldDeadline = 2 * time.Second

	// progressEvery 逐字符路径每注入这么多个字刷新一次进度(末字必刷)
	progressEvery = 8

	// 逐字符输入的字符间隔三档: ASCII 最快, 中日韩文字居中, 标点最慢
	// (给输入法留出上屏时间)
	charDelayASCII = 8 * time.Millisecond
	charDelayCJK   = 12 * time.Millisecond
	charDelayPunct = 16 * time.Millisecond

	// escapeGap 文本直投换行前, Esc 与回车之间的间隔(补全弹窗需要时间关闭)
	escapeGap = 20 * time.Millisecond

	// clipboardSettleWait 写入剪贴板到发出粘贴之间的稳定等待
	clipboardSettleWait = 100 * time.Millisecond
	// pasteSettleWait 粘贴发出到恢复剪贴板之间的稳定等待
	pasteSettleWait = 200 * time.Millisecond
)

// ─── TypingService ────────────────────────────────────

// taskSlot 一次任务的收尾信号: 它的 done 关闭即表示该任务已停手 ——
// 必须用它而不是运行标志等上一任务, 见 docs/invariants.md「任务槽与并发启动」
type taskSlot struct{ done chan struct{} }

// TypingService 持有输入任务生命周期的全部可变状态
// (取消/运行互斥/任务代数/状态播报), 通过接口使用平台能力
type TypingService struct {
	injector   TextInjector
	clipboard  Clipboard
	foreground Foreground

	// mu 串行化任务槽的"判定 — 占领"与状态播报: 任务槽、代数、状态三者必须一起
	// 改 —— 见 docs/invariants.md「任务槽与并发启动」
	mu         sync.Mutex
	prevTask   *taskSlot // 在途任务; nil 表示空闲。它是"有没有任务在跑"的唯一权威
	cancelFlag atomic.Bool

	// taskGen 任务代数: 每次 start/cancel 都递增, 过代任务的状态写入作废
	taskGen atomic.Uint64

	typingStatus atomic.Value // 存 *TypingStatus

	// 倒计时/逐字符/粘贴的等待, 测试可注入假时钟;
	// 等上一任务让位走的是它自己的 taskSlot.done, 刻意保持真实时钟
	sleep func(time.Duration)
}

func NewTypingService(inj TextInjector, cb Clipboard, fg Foreground) *TypingService {
	s := &TypingService{
		injector:   inj,
		clipboard:  cb,
		foreground: fg,
		sleep:      time.Sleep,
	}
	s.typingStatus.Store(&TypingStatus{
		Phase: PhaseIdle,
		// 必须有: 零值 0 会被前端当成"进度 0%"画出空进度条 ——
		// 见 docs/invariants.md「其它不变量」
		Progress: -1,
	})
	return s
}

// Status 读取当前输入状态(前端轮询)
func (s *TypingService) Status() *TypingStatus {
	return s.typingStatus.Load().(*TypingStatus)
}

// storeStatus 播报状态, 但只认自己那一代: 迟到的写入一律作废, 免得旧任务收尾
// 时把新任务的状态盖回去 —— 见 docs/invariants.md「任务槽与并发启动」
func (s *TypingService) storeStatus(gen uint64, st *TypingStatus) {
	if gen == s.taskGen.Load() {
		s.typingStatus.Store(st)
	}
}

// Start 启动一次输入任务(倒计时 + 注入)。
// forceSendInput 绕过剪贴板降级强制逐字符; textDirect 为文本直投, 字符经
// WM_CHAR 文本层注入, 换行仍需真按键(先 Esc) —— 见 docs/invariants.md「文本直投」
//
// 判定与占领同在临界区, 返回 nil 即已拿到任务槽 ——
// 见 docs/invariants.md「任务槽与并发启动」; 等上一任务让位仍异步, 否则最长
// yieldDeadline 会把窗口冻住, 而用户此刻点得动取消按钮
//
// 无内容可输入的文本(剔除 \r 后为空)当场拒绝, 不写倒计时、不占任务槽:
// 判据必须在后端 —— 见 docs/behavior-contract.md「行为契约（冻结，改动需双端同步）」
func (s *TypingService) Start(text string, delay int, forceSendInput bool, textDirect bool) (string, error) {
	// 与逐字符路径剔除 \r 的口径保持一致(见 runTypingTask), 免得"\r"被当成有内容
	if strings.ReplaceAll(text, "\r", "") == "" {
		return "", fmt.Errorf(msgNothingToType)
	}

	// 采样要在锁外做: 它是 Win32 调用, 目标进程无响应时回不来, 抓着锁做会把
	// 用户的取消一起冻住 —— 见 docs/invariants.md「任务槽与并发启动」
	//
	// 采样前先读一眼取消标志, 用来区分两种取消: 分配给上一任务的(本次是"取消后
	// 立即重启", 不该被拦)与这次采样窗口里用户点的(必须认) ——
	// 见 docs/invariants.md「任务槽与并发启动」
	preCancel := s.cancelFlag.Load()
	baseline := s.foreground.Sample()

	s.mu.Lock()
	defer s.mu.Unlock()

	// 在途任务与取消标志都要在这里一次取全: 出了临界区再看, 看到的可能已经
	// 是另一个任务的现场
	prev := s.prevTask
	if prev != nil && !s.cancelFlag.Load() {
		// 有人在跑、又没人在取消: 这是重入, 当场拒绝
		return "", fmt.Errorf(msgAlreadyRunning)
	}
	// 标志一律不在这里清: 采样窗口里到达的取消还要靠它被任务认出来, 等本任务
	// 开工时自己清 —— 见 docs/invariants.md「任务槽与并发启动」
	cancelHonored := !preCancel && s.cancelFlag.Load()

	gen := s.taskGen.Add(1)
	s.typingStatus.Store(&TypingStatus{
		Phase:        PhaseCountdown,
		Message:      countdownMessage(delay),
		SecondsLeft:  delay,
		Progress:     -1,
		TargetWindow: baseline.Title,
	})
	// 本任务的收尾信号: 挂到 prevTask 上, 下一个任务据此等本任务停手
	slot := &taskSlot{done: make(chan struct{})}
	s.prevTask = slot
	// 运行现场一次填好交给任务; outcome.clipboardKept 显式写 true —— 它的零值
	// 含义是"剪贴板没保住", 当前漏写不可观测(读它的分支还要求 touchedClipboard),
	// 但别依赖: 同类零值陷阱见 docs/invariants.md「其它不变量」
	go s.runTypingTask(&taskRun{
		gen:            gen,
		cancelHonored:  cancelHonored,
		text:           text,
		delay:          delay,
		forceSendInput: forceSendInput,
		textDirect:     textDirect,
		baseline:       baseline,
		slot:           slot,
		prev:           prev,
		outcome:        runOutcome{clipboardKept: true},
	})
	return "started", nil
}

// waitPreviousTask 等上一任务停手, 返回是否等到: 等的是它自己的收尾信号而不是
// 共享标志(那早被本任务改过, 拿它判断等于在等自己) ——
// 见 docs/invariants.md「任务槽与并发启动」; 上界是 yieldDeadline
func (s *TypingService) waitPreviousTask(prev *taskSlot) bool {
	select {
	case <-prev.done:
		return true
	case <-time.After(yieldDeadline):
		return false
	}
}

// Cancel 取消在途任务(不等待其退出; 旧实现等最长 2 秒, 会把窗口冻住)。
// 先置取消标志再取 mu: 若先等锁, 恰好赶上 Start 在临界区时反而延长旧任务退出
//
// 任务已有结局(success/error)时不改写状态、不递增代数: 判据取状态而不是任务槽
// —— 见 docs/invariants.md「任务槽与并发启动」
func (s *TypingService) Cancel() (string, error) {
	s.cancelFlag.Store(true)

	s.mu.Lock()
	defer s.mu.Unlock()
	if phase := s.Status().Phase; phase == PhaseSuccess || phase == PhaseError {
		return "cancelled", nil
	}
	// 递增代数让在途任务的后续写入作废, 并立刻播报"已取消"(不等它退出)
	s.taskGen.Add(1)
	s.typingStatus.Store(&TypingStatus{
		Phase: PhaseCancel, Message: msgCancelled, Progress: -1,
	})
	return "cancelled", nil
}

// sendEscaped 先注入 Esc 关闭补全弹窗, 稍候再注入 key: 弹窗开着则被关掉、
// 没开则基本无操作 —— 状态无关设计, 见 docs/invariants.md「文本直投」
func (s *TypingService) sendEscaped(key func() bool) bool {
	if !s.injector.SendEscape() {
		return false
	}
	s.sleep(escapeGap)
	return key()
}

// typeTextViaClipboard 执行剪贴板粘贴输入(两种模式共用): 快照由调用方给且已确认
// 完整, 结束后原样恢复, 不销毁用户已有的图片/文件等非文本内容 ——
// 见 docs/invariants.md「其它不变量」
// 返回值: success 粘贴按键是否被接受; failMsg 失败时用户可见的具体原因(剪贴板故障、
// 目标拒收 Ctrl+V、目标切换三者分开报); clipboardOK 剪贴板是否仍保有注入前的内容,
// 仅在 success 为真时被终态采信
//
// cancelled 由调用方给出, 同时认三件事: 用户取消、本任务已被取代、开工前就被
// 取消过; 每个发送点都要问它一次, 免得过期的任务把内容送进目标窗口
func (s *TypingService) typeTextViaClipboard(text string, locked TargetID, cancelled func() bool, snap []ClipboardFormat) (success bool, failMsg string, clipboardOK bool) {
	// 粘贴前的漂移守卫: 目标已切走就一个字都不送, 也不碰剪贴板
	if !s.targetHeld(locked) {
		return false, msgTargetSwitchedIdle, true // 没碰过剪贴板, 原内容还在
	}
	if !s.clipboard.SetText(text) {
		// EmptyClipboard 可能已执行(分配阶段失败), 直接写回快照
		return false, msgClipboardFailed, s.clipboard.RestoreSnapshotRaw(snap)
	}
	s.sleep(clipboardSettleWait)

	if cancelled() {
		return false, "", s.restoreClipboardSnapshot(snap, text)
	}
	// 写入剪贴板与粘贴之间还隔着 clipboardSettleWait 稳定等待, 期间目标可能被切走;
	// 此刻放弃粘贴, 但剪贴板已经写过, 快照恢复照旧执行
	if !s.targetHeld(locked) {
		return false, msgTargetSwitchedIdle, s.restoreClipboardSnapshot(snap, text)
	}
	// 发送前最后确认一次: 上面那次检查之后还隔着一次采样, 用户在这中间点取消,
	// 或者重启把本任务顶掉, 都不该再把内容送出去
	if cancelled() {
		return false, "", s.restoreClipboardSnapshot(snap, text)
	}
	pasted := s.injector.SendPaste()
	// 紧随其后的复检: SendPaste 返回后再漂移, 说明这次粘贴的落点已不是
	// 锁定目标, 内容是否送达无法确认。检查刻意紧贴 SendPaste 而不放在
	// pasteSettleWait 稳定等待之后 —— 用户在看到内容粘贴成功后才切窗口是正常操作,
	// 那时顶多是"没多等一会儿", 不该被误报成失败
	drifted := !s.targetHeld(locked)
	s.sleep(pasteSettleWait)

	restored := s.restoreClipboardSnapshot(snap, text)
	if drifted {
		return false, msgTargetSwitchedPasted, restored
	}
	if !pasted {
		return false, msgPasteRejected, restored
	}
	return true, "", restored
}

// targetHeld 当前顶层前台窗口是否仍是锁定的目标窗口(漂移守卫的判据): 按顶层
// 标识严格比对 —— 见 docs/invariants.md「焦点锁定与漂移防护」
func (s *TypingService) targetHeld(locked TargetID) bool {
	return s.foreground.Sample().ID == locked
}

// restoreClipboardSnapshot 恢复快照, 返回剪贴板是否仍保有注入前的内容: 仅当
// 剪贴板仍为本次注入的文本时才恢复, 用户已复制新内容则跳过(那不算失败, 剪贴板
// 此刻归用户所有) —— 见 docs/invariants.md「其它不变量」
// 返回 false 的三种情形(nil、写回失败、读不到)都意味着原内容可能已丢失
func (s *TypingService) restoreClipboardSnapshot(snap []ClipboardFormat, injected string) bool {
	if snap == nil {
		return false // 原状态未知, 无从恢复
	}
	holds, known := s.clipboard.HoldsText(injected)
	if !known {
		// 读不到: 原内容还在不在无从判断。既不覆盖, 也不许报"恢复成功"
		return false
	}
	if !holds {
		return true // 用户已复制新内容: 剪贴板归用户所有, 跳过恢复正是想要的结果
	}
	return s.clipboard.RestoreSnapshotRaw(snap)
}

// ─── 输入判断 ─────────────────────────────────────────

func containsNonASCII(s string) bool {
	for _, r := range s {
		if r > 0x7F {
			return true
		}
	}
	return false
}

// isCJKPunct 判断是否中日韩标点
func isCJKPunct(r rune) bool {
	return (r >= 0x3000 && r <= 0x303F) || // CJK 标点（、。！？：；等）
		(r >= 0xFF01 && r <= 0xFF0F) || // 全角标点 ！
		(r >= 0xFF1A && r <= 0xFF1F) || // ：；？
		(r >= 0x2018 && r <= 0x201D) // 中文引号 '' ""
}
