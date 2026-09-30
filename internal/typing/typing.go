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
// 五个方法均返回"本次注入是否被系统接受": SendInput 在 UIPI 拦截(目标窗口
// 权限更高)、工作站锁定或切到安全桌面时整体失败且不产生任何按键, 此时
// 返回 false, 状态机据此报错而不是把静默失败当成输入完成
type TextInjector interface {
	SendRune(r rune) bool // 按键层字符注入, 含全角标点 WM_CHAR 绕行
	SendText(r rune) bool // 文本层字符注入(WM_CHAR 直投), 见文本直投
	SendEnter() bool
	SendEscape() bool // 关闭目标编辑器的补全弹窗
	SendPaste() bool  // Ctrl+V
}

// ClipboardFormat 剪贴板单一格式的原始字节快照
type ClipboardFormat struct {
	Fmt  uint32
	Data []byte
}

// ClipboardSnapshot 一次剪贴板快照。Formats 为 nil 表示剪贴板打开失败或
// 根本没读到任何格式(原状态未知, 无从恢复); 空切片表示剪贴板原本为空。
//
// Complete 是这次快照能不能拿来恢复的唯一判据。恢复流程会先清空剪贴板,
// 所以拿一份残缺的快照去恢复, 等于把没能照抄下来的那些格式永久销毁
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
	// HoldsText 剪贴板当前文本是否就是 text。按原始字节比对而非 GetText 的
	// 字符串比较: CF_UNICODETEXT 内嵌 NUL 时字符串会在 NUL 处被截断,
	// 守卫会误判为"用户已改动"而跳过恢复
	HoldsText(text string) bool
	// Snapshot 取剪贴板全部格式。读不全时必须如实把 Complete 报成 false:
	// 调用方据此放弃剪贴板这条路, 免得销毁用户原本复制的内容(见 ClipboardSnapshot)
	Snapshot() ClipboardSnapshot
	// RestoreSnapshotRaw 无条件写回快照(调用方需确认剪贴板未被用户改动),
	// 返回是否真的恢复成功: 失败意味着用户原本的内容可能已丢失, 调用方
	// 必须说出来而不是静默略过
	RestoreSnapshotRaw(snap []ClipboardFormat) bool
}

// TargetID 目标窗口的平台无关标识(不透明): 业务层只做相等比较, 不解释内容。
// Win32 侧就是顶层前台窗口句柄。判定"还是不是同一个窗口"必须用标识而不是
// 标题 —— 标题会重名、会中途变化(浏览器切标签、文档改名), 标识不会
type TargetID uintptr

// ForegroundSample 一次前台窗口采样的结果
type ForegroundSample struct {
	ID    TargetID // 顶层前台窗口标识; 无前台窗口时为 0
	Title string   // 该窗口标题(供预览与终态展示)
	Self  bool     // 是否为本程序自身窗口
}

// Foreground 前台窗口探测
type Foreground interface {
	// Sample 读取当前前台窗口。三要素必须取自同一次读取: 分多次读会在
	// 两次调用的间隙发生切换时得到互相矛盾的组合 —— 展示的标题不是锁定
	// 下来的那个窗口, 或"非自身"判定与目标锁定之间用户切回 Type, 让漂移
	// 守卫从第一步就失效
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

const (
	// msgPartialSendInput 注入中途被系统拒绝。绝大多数情形是 Windows UIPI:
	// 以普通权限运行的 Type 无法向管理员权限的目标窗口注入输入
	msgPartialSendInput = "输入中断：目标窗口拒绝了模拟按键，可能其权限高于 Type"
	// msgPasteRejected 剪贴板写入成功但 Ctrl+V 未被接受
	msgPasteRejected = "粘贴未生效：目标窗口拒绝了模拟按键，可能其权限高于 Type"
	// msgClipboardFailed 剪贴板打开/写入本身失败, 尚未走到粘贴这一步
	msgClipboardFailed = "剪贴板操作失败"
	// msgTargetSwitchedIdle 漂移守卫中止, 且一个字都没送出去(逐字符路径
	// 尚未注入, 或剪贴板路径尚未粘贴): 目标窗口里没有留下任何内容
	msgTargetSwitchedIdle = "输入中断：目标窗口已切换，未输入任何内容"
	// msgTargetSwitchedPasted 粘贴已经发出而目标窗口随即改变: 落点可能已
	// 不是锁定目标, 内容是否送达无法确认 —— 不能报"输入完成"
	msgTargetSwitchedPasted = "输入中断：目标窗口已切换，粘贴结果无法确认"
	// msgClipboardNotRestored 粘贴成功但剪贴板没能换回原内容: 注入是成功的,
	// 而用户原本复制的东西可能已经丢了。两处都不能含糊 —— 既不能报成
	// "输入完成"让用户以为一切如常, 也不能报成"输入失败"抹掉注入已送达的事实
	msgClipboardNotRestored = "输入完成，但剪贴板未恢复，原内容可能已丢失"
)

// msgTargetSwitchedTyped 逐字符路径中途检测到目标窗口切换。已注入的部分
// 无法撤回, 报出已输入的字数, 让用户知道目标窗口里留下了多少内容
func msgTargetSwitchedTyped(n int) string {
	return fmt.Sprintf("输入中断：目标窗口已切换，已输入 %d 字", n)
}

// ─── 用户可见文案(冻结清单的其余部分) ─────────────────
// 逐字固定, 改动会直接落到用户眼前; contract_test.go 里有一张字面量表钉着
// 它们。允许新增, 但新增也要登记进 AGENTS.md 的清单
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
// 对取消响应与字符间隔的影响(见 AGENTS.md「焦点锁定与漂移防护」)。
// 同值不同义的不要合并: 100ms 在倒计时节拍与剪贴板稳定等待上各出现一次,
// 两处可以独立调整
const (
	// countdownTick 倒计时节拍: 100ms 一拍, 每秒 10 拍, 取消与切窗的
	// 响应都缩到一拍内, 总时长仍是 delay 秒
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

// taskSlot 一次任务的收尾信号: 它的 done 关闭即表示该任务已停手。
// 为什么不用运行标志来等上一任务: 标志是"任务槽被占", 新任务一进来就把它
// 占掉了, 再拿它判断等于在等自己, 于是新任务必然误判超时(v1.5.6 踩过)
type taskSlot struct{ done chan struct{} }

// TypingService 持有输入任务生命周期的全部可变状态
// (取消/运行互斥/任务代数/状态播报), 通过接口使用平台能力
type TypingService struct {
	injector   TextInjector
	clipboard  Clipboard
	foreground Foreground

	// mu 串行化任务槽的"判定 — 占领"与状态播报。任务槽、代数、状态三者必须
	// 一起改: 光靠一个原子标志挡不住几乎同时到达的两次启动(v1.5.6 的缺陷)
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
	s.typingStatus.Store(&TypingStatus{Phase: PhaseIdle})
	return s
}

// Status 读取当前输入状态(前端轮询)
func (s *TypingService) Status() *TypingStatus {
	return s.typingStatus.Load().(*TypingStatus)
}

// storeStatus 播报状态, 但只认自己那一代: 任务被更新一代的 start/cancel
// 取代以后, 迟到的写入一律作废, 免得旧任务收尾时把新任务的状态盖回去
func (s *TypingService) storeStatus(gen uint64, st *TypingStatus) {
	if gen == s.taskGen.Load() {
		s.typingStatus.Store(st)
	}
}

// Start 启动一次输入任务(倒计时 + 注入)。
// forceSendInput 绕过剪贴板降级强制逐字符; textDirect 为文本直投:
// 字符经 WM_CHAR 文本层注入(无按键事件), 目标编辑器挂在 keydown 层的
// 补全弹窗劫持与括号自动配对均无从触发; 回车/Tab 无法走文本层,
// 自动先发 Esc 关闭补全弹窗再按键
//
// 判定与占领在同一个临界区里完成, 返回 nil 就意味着本任务已经拿到任务槽:
// "启动成功"与"确实占住"必须是同一个事实。此前是先把运行标志读一遍就算通过,
// 再由新开的 goroutine 自己去认领, 于是两次几乎同时到达的启动都能通过那次读、
// 各跑各的, 同一段文本被注入两遍(实测 300 轮里 246 轮命中, v1.5.6 修复)。
// 等待上一任务让位仍是异步的: 它最长要 yieldDeadline, 放在这里会把窗口冻住,
// 而用户此刻点得动取消按钮, 取消必须马上有反应
func (s *TypingService) Start(text string, delay int, forceSendInput bool, textDirect bool) (string, error) {
	// 采样要在锁外做: 它是 Win32 调用, 目标进程无响应时回不来, 抓着锁做
	// 会把用户的取消一起冻住。代价是"判定时的预览"与"锁定后的目标"之间
	// 存在一次采样的间隔, 而这个间隔本来就存在(倒计时结束还要再采样一次)
	//
	// 采样前先读一眼取消标志: 它是"这一次取消是不是冲我来的"的判据之一。
	// 采样前后两次读的差别正好区分两种情况 ——
	//   采样前就是 true: 那是分配给上一任务的取消(它正靠这个标志退出),
	//                    本次启动是"取消后立即重启", 不该被它拦下;
	//   采样前 false、采样后 true: 用户是在这次采样的间隙里点的取消,
	//                    目标就是本次启动, 必须认(v1.5.6 复核发现的缺口)
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
	// 标志一律不在这里清: 采样窗口里到达的取消还要靠它被任务认出来(清了就
	// 抹掉了), 上一任务退出的加速也靠它。等本任务开工时自己清(见 runTypingTask)
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
	go s.runTypingTask(gen, cancelHonored, text, delay, forceSendInput, textDirect, baseline, slot, prev)
	return "started", nil
}

// waitPreviousTask 等上一任务停手, 返回是否等到。
// 等的是它自己的收尾信号, 而不是某个共享标志: 共享标志早被本任务改过了,
// 拿它当判据等于在等自己。上界是 yieldDeadline
func (s *TypingService) waitPreviousTask(prev *taskSlot) bool {
	select {
	case <-prev.done:
		return true
	case <-time.After(yieldDeadline):
		return false
	}
}

// Cancel 取消在途任务(不等待其退出)。
// 先置取消标志再取 mu: 标志是让任务尽快退出的手段, 若先等锁, 恰好赶上
// Start 在临界区里时反而延长了旧任务的退出时间
func (s *TypingService) Cancel() (string, error) {
	s.cancelFlag.Store(true)

	s.mu.Lock()
	defer s.mu.Unlock()
	// 递增代数让在途任务的后续状态写入作废, 并立刻播报"已取消";
	// 此处不等任务退出 —— 旧实现等最长 2 秒, 窗口会冻住
	s.taskGen.Add(1)
	s.typingStatus.Store(&TypingStatus{
		Phase: PhaseCancel, Message: msgCancelled, Progress: -1,
	})
	return "cancelled", nil
}

// runTypingTask 执行一次完整的输入任务(倒计时 + 注入)。
// 任务槽已由 Start 在临界区内占好, 倒计时初态也已写好; slot 是本任务的
// 收尾信号, prev 是占领时仍在收尾的上一任务(没有则为 nil)
func (s *TypingService) runTypingTask(gen uint64, cancelHonored bool, text string, delay int, forceSendInput bool, textDirect bool, baseline ForegroundSample, slot *taskSlot, prev *taskSlot) {
	// 收尾时先放信号再腾空任务槽: 下一个任务等的是信号, 而空闲判定看的是槽
	defer func() {
		close(slot.done)
		s.mu.Lock()
		if s.prevTask == slot {
			s.prevTask = nil
		}
		s.mu.Unlock()
	}()

	// 等待点的取消判据:
	//   ① 自己出生之后来的取消 —— 取消计数涨过了基线。这条是判据的主力:
	//      取消标志会被下一次 Start 清掉, 而计数只增不减, 藏在采样窗口里的
	//      那次取消也只有它认得出来;
	//   ② 本任务已被更新一代的操作取代(任务代数变了);
	//   ③ 取消标志本身, 它让循环尽快察觉, 但单独看它不可靠(见 ①)
	cancelled := func() bool {
		return gen != s.taskGen.Load() || s.cancelFlag.Load()
	}

	// 上一任务还在收尾时先等它停手, 免得两路注入交叠。等不到就让界面如实
	// 显示"没能启动": 此刻状态是 Start 写下的倒计时, 而倒计时不是终态,
	// 前端会一直轮询下去。
	// 这里必须用本任务的代数(gen)去写, 不能拿 s.taskGen.Load() 当守卫 ——
	// 那是拿自己和自己比, 恒真, 迟到的旧任务会把终态盖到在途的新任务上
	// (v1.5.6 复核发现)
	if prev != nil && !s.waitPreviousTask(prev) {
		s.storeStatus(gen, &TypingStatus{
			Phase: PhaseError, Message: msgPreviousTaskStuck, Progress: -1,
		})
		return
	}
	// 上一任务已经停手, 现在才轮到自己当"当前任务": 清掉那个用来催它退出的
	// 取消标志。Start 里刻意没清(那时清会让第二次启动被误判成重入),
	// 这里清才安全 —— 此刻再来的取消就是冲着我来的
	s.cancelFlag.Store(false)

	// 出生之前那次取消已经把本次启动作废了: 这里必须自己把终态说出来。
	// 不说的话状态就停在 Start 写下的倒计时上 —— 倒计时不是终态, 前端会一直
	// 轮询下去, 用户看到的是永远不动的"剩余 N 秒"(v1.5.6 复核发现的缺口)。
	// 用 gen 当守卫: 已被更新一代接管时不能插嘴, 状态归新任务写。
	// 这一判必须放在上面"清标志"之后: 在那之前看到的标志还是上一轮留下的
	if cancelHonored {
		if gen == s.taskGen.Load() {
			s.typingStatus.Store(&TypingStatus{Phase: PhaseCancel, Message: msgCancelled, Progress: -1})
		}
		return
	}

	// ── 倒计时 ──
	// 目标预览 = 当前前台窗口, countdownTick 节拍采样, 但只在可见内容变化时才写
	// 状态: 秒边界写一次(文案与旧版逐字符一致), 窗口标识或标题一变立即
	// 跟上(用户切到哪个窗口, 预览最多迟一拍), 其余节拍只采样不写。
	// 采样与写状态分离后, 取消与切窗的响应从最坏 1 秒缩到 1 拍, 而每秒
	// 10 次的冗余状态写入并不存在; 总时长仍是 delay 秒 —— 每拍
	// sleep(countdownTick), 共 delay*countdownTicksPerSec 拍, 与旧实现
	// 每次写入后 sleep(1s) 的总时长一致
	shownSec, shown := delay, baseline
	for i := 0; i < delay*countdownTicksPerSec; i++ {
		if cancelled() {
			return // Cancel 已写入取消状态
		}
		sec := delay - i/countdownTicksPerSec
		sample := s.foreground.Sample()
		if sec != shownSec || sample != shown {
			shownSec, shown = sec, sample
			s.storeStatus(gen, &TypingStatus{
				Phase:        PhaseCountdown,
				Message:      countdownMessage(sec),
				SecondsLeft:  sec,
				Progress:     -1,
				TargetWindow: sample.Title,
			})
		}
		s.sleep(countdownTick)
	}
	if cancelled() {
		return
	}

	s.sleep(lockSettleWait)

	// 执行目标锁定: 倒计时结束时的前台窗口, 贯穿到执行与终态状态。
	// 标识与标题取自同一次采样, 展示的标题一定就是锁定下来的那个窗口;
	// 焦点仍在 Type 自身时注入会落进自己的输入框 —— 明确报错, 不静默打错地方
	locked := s.foreground.Sample()
	if locked.Self {
		s.storeStatus(gen, &TypingStatus{
			Phase:    PhaseError,
			Message:  msgFocusStayedOnSelf,
			Progress: -1,
		})
		return
	}
	target := locked.Title

	// ── 执行 ──
	success := false
	// injected 是否有内容真正送达目标窗口。系统拒绝注入(SendInput 返回 0,
	// 典型为 UIPI)时注入器返回 false, 此时不得报"输入完成"
	injected := false
	// failMsg 注入中途被拒的具体原因; 为空则终态用通用"输入失败"
	failMsg := ""
	// clipboardOK 剪贴板是否仍保有注入前的内容。只有粘贴路径会碰剪贴板,
	// 恢复失败时终态必须如实说明: 报完"输入完成"就把用户原本复制的东西
	// 当成还在, 是最容易让人吃亏的那种隐瞒
	clipboardOK := true
	// 先试剪贴板路径: 含非 ASCII 且没有被强制逐字符时, 粘贴是最省事也最可靠的办法。
	// 快照拿不全时(拿不全就恢复不回去)与剪贴板不可用时都退到逐字符, 而不是
	// 硬走粘贴把用户原本复制的内容销毁掉
	useClipboard := containsNonASCII(text) && !forceSendInput
	// pasteSnap 剪贴板确实被本任务动过(终态要认这个事实);
	// clipboardGaveUp 剪贴板这条路已经失败且不该重试, 逐字符通道必须让位,
	// 否则它会用 SendRune 的失败原因把剪贴板那边的具体原因盖掉
	pasteSnap, clipboardGaveUp := false, false
	if useClipboard {
		s.storeStatus(gen, &TypingStatus{
			Phase: PhaseTyping, Message: msgClipboardStart, Progress: -1,
			TargetWindow: target,
		})
		snap := s.clipboard.Snapshot()
		if snap.UnsafeToRestore() {
			// 剪贴板打开失败(原状态未知), 或有的格式没能照抄下来。这份快照
			// 一写回去就会把没抄到的那些格式永久销毁, 所以干脆不碰剪贴板
			useClipboard = false
		} else {
			// 失败原因由被调方给出: 剪贴板故障、目标窗口拒收 Ctrl+V、目标窗口
			// 切换三者的处置不同, 不能都退化成通用的"输入失败"。
			// 这里必须写在外层 failMsg 上(不是新声明一个): 终态要用它
			success, failMsg, clipboardOK = s.typeTextViaClipboard(text, locked.ID, cancelled, snap.Formats)
			injected = success // 粘贴按键被接受即内容已送达
			if success {
				pasteSnap = true // 剪贴板确实被本任务动过了, 终态要认这个事实
			} else {
				if cancelled() {
					return // Cancel 已写入取消状态
				}
				if failMsg != "" {
					s.storeStatus(gen, &TypingStatus{
						Phase: PhaseTyping, Message: failMsg, Progress: -1,
						TargetWindow: target,
					})
				}
				// 到了这里一律不再退到逐字符: 能走到这一步说明剪贴板已经被
				// 写过或粘贴已经发出(入口那次漂移守卫失败会在下个分支处理),
				// 再打一遍就是把内容注入两遍, 也会把上面那个具体原因盖成
				// 一句泛泛的"输入中断"
				clipboardGaveUp = true
			}
		}
	}
	if !useClipboard && !clipboardGaveUp {
		// 剔除 \r 使进度分母与实际注入次数一致 (\r\n 由 \n 触发回车)
		runes := []rune(strings.ReplaceAll(text, "\r", ""))
		total := len(runes)
		s.storeStatus(gen, &TypingStatus{
			Phase: PhaseTyping, Message: progressMessage(0, total), Progress: 0,
			TargetWindow: target,
		})

		typed := 0
		ok := true
		drifted := false
		for _, r := range runes {
			if cancelled() {
				break
			}
			// 漂移守卫: 每次注入前确认前台仍是倒计时结束时锁定的那个窗口。
			// 判定按顶层窗口标识(严格): 输入法候选窗与补全弹窗不是顶层前台
			// 窗口, 不会误触发; 用户切走(含切回 Type 自身)则立即停止, 不把
			// 剩余内容打进错误的窗口。检查与注入之间仍有毫秒级窗口, 切换
			// 恰好发生在其中时, 最多漏进一两个字符
			if !s.targetHeld(locked.ID) {
				drifted = true
				break
			}
			// 注入通道分流: 文本直投把字符(含 Tab, WM_CHAR 可插入制表符)
			// 送到文本层(无按键事件), 弹窗劫持与括号配对都挂在 keydown 上
			// 因而无从触发; 换行无法走文本层 —— 实测 Chromium 会过滤
			// WM_CHAR 的 \n/\r 控制字符, 只能真按键, 故先经 sendEscaped
			// 用 Esc 关掉可能挂着的弹窗再按回车
			switch {
			case textDirect && r == '\n':
				ok = s.sendEscaped(s.injector.SendEnter)
			case textDirect:
				ok = s.injector.SendText(r)
			case r == '\n':
				ok = s.injector.SendEnter()
			default:
				ok = s.injector.SendRune(r)
			}
			if !ok {
				break // 注入被拒: 停止并报错, 不继续虚报进度
			}
			injected = true
			typed++

			if typed%progressEvery == 0 || typed == total {
				s.storeStatus(gen, &TypingStatus{
					Phase:        PhaseTyping,
					Message:      progressMessage(typed, total),
					Progress:     typed * 100 / total,
					TargetWindow: target,
				})
			}

			// 固定快速延迟: 三档数值见常量区 (给 IME 喘息)
			charDelay := charDelayASCII
			if r > 127 {
				if isCJKPunct(r) {
					charDelay = charDelayPunct
				} else {
					charDelay = charDelayCJK
				}
			}
			s.sleep(charDelay)
		}
		if cancelled() {
			success = false
		} else if drifted {
			// 已注入的部分无法撤回, 如实报出停在第几个字
			failMsg = msgTargetSwitchedTyped(typed)
			success = false
		} else if !ok {
			s.storeStatus(gen, &TypingStatus{Phase: PhaseTyping, Message: msgPartialSendInput, Progress: -1, TargetWindow: target})
			failMsg = msgPartialSendInput
			success = false
		} else {
			success = true
		}
	}

	// 最终状态（前端检测到终止 phase 后停止轮询）; 过代则静默, 状态已由新操作接管
	switch {
	case cancelled():
		// Cancel 已写入取消状态
	case success && injected && pasteSnap && !clipboardOK:
		// 内容已送达, 但剪贴板没能换回原内容: 如实说明, 不报"输入完成"
		s.storeStatus(gen, &TypingStatus{Phase: PhaseSuccess, Message: msgClipboardNotRestored, Progress: -1, TargetWindow: target})
	case success && injected:
		s.storeStatus(gen, &TypingStatus{Phase: PhaseSuccess, Message: msgDone, Progress: -1, TargetWindow: target})
	case success:
		// 文本为空(或整段被剔除): 无可注入内容, 不算失败, 但也不能报"输入完成"
		s.storeStatus(gen, &TypingStatus{Phase: PhaseSuccess, Message: msgNothingToType, Progress: -1, TargetWindow: target})
	default:
		// 保留注入器给出的具体原因(如权限不足), 而不是笼统的"输入失败"
		msg := failMsg
		if msg == "" {
			msg = msgGenericFailure
		}
		s.storeStatus(gen, &TypingStatus{Phase: PhaseError, Message: msg, Progress: -1, TargetWindow: target})
	}
}

// sendEscaped 先注入 Esc 关闭目标编辑器的补全弹窗, 稍候再注入 key。
// 文本直投下只有换行还是真按键(文本层过滤 \n/\r 控制字符, 无法插入换行),
// 这个键若在弹窗开着时到达会被解释为"接受候选"; 弹窗开着则被 Esc 关闭
// (目的达成), 没开则基本无操作 —— 状态无关设计, 无需探测弹窗是否真的存在
func (s *TypingService) sendEscaped(key func() bool) bool {
	if !s.injector.SendEscape() {
		return false
	}
	s.sleep(escapeGap)
	return key()
}

// typeTextViaClipboard 执行剪贴板粘贴输入（两种模式共用）。
// 快照由调用方给(snap), 结束后原样恢复, 不销毁用户已有的图片/文件等非文本
// 内容; 调用方已确认这份快照完整, 否则不会走到这里。
// 返回值: success 粘贴按键是否被系统接受(即内容是否送达);
// failMsg 失败时用户可见的具体原因 —— 剪贴板故障、目标窗口拒收 Ctrl+V、
// 目标窗口切换三者必须分开报; 成功或中途取消时为空(取消的状态由 Cancel
// 负责写入);
// clipboardOK 剪贴板是否仍保有注入前的内容(未被本次注入弄丢), 仅在 success
// 为真时被终态采信: 注入成功而恢复失败要如实说明, 见 msgClipboardNotRestored
//
// cancelled 由调用方给出: 它同时认用户取消、本任务是否已被取代, 以及开工时
// 是否就已经被取消过; 本函数每个发送点都要问它一次, 免得一条已经过期的任务
// 把内容送进目标窗口。调用方只在"剪贴板确实被写过"之后才需要区分失败要不要
// 换条路重试 —— 而入口这次漂移守卫失败(没碰过剪贴板)由调用方按整条路径处理,
// 所以本函数不必再回报"碰没碰过"
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

// targetHeld 当前顶层前台窗口是否仍是锁定的目标窗口(漂移守卫的判据)。
// 按顶层窗口标识严格比对: 标题会重名, 不能用; 子窗口/焦点变化(输入法
// 候选窗、补全弹窗、同一程序内换输入框)不是顶层前台变化, 不会误触发
func (s *TypingService) targetHeld(locked TargetID) bool {
	return s.foreground.Sample().ID == locked
}

// restoreClipboardSnapshot 恢复快照, 返回剪贴板是否仍保有注入前的内容。
// 仅当剪贴板仍为本次注入的文本时执行, 避免覆盖用户在注入期间新复制的数据;
// 原内容为空则直接清空, 不留注入残留。
// 返回 false 的两种情形都意味着原内容可能已丢失: 快照本身没拿到(nil),
// 或恢复时剪贴板被别的程序占着/写回失败。用户已复制新内容时跳过恢复不算失败
// —— 剪贴板此刻归用户所有, 正是想要的结果
func (s *TypingService) restoreClipboardSnapshot(snap []ClipboardFormat, injected string) bool {
	if snap == nil {
		return false // 原状态未知, 无从恢复
	}
	if !s.clipboard.HoldsText(injected) {
		return true
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
