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
	// HoldsText 剪贴板当前文本是否就是 text, 以及这次比对有没有得出结论。
	// 按原始字节比对而非 GetText 的字符串比较: CF_UNICODETEXT 内嵌 NUL 时
	// 字符串会在 NUL 处被截断, 守卫会误判为"用户已改动"而跳过恢复。
	//
	// 第二个返回值不许省: "读不到剪贴板"与"用户已改动"的处置**相反** ——
	// 前者要按"没恢复"上报(注入前的内容早已被覆盖, 原内容可能已经丢了),
	// 后者要跳过恢复(剪贴板此刻归用户所有, 那正是想要的结果)。
	// 读不到的情形: 打开剪贴板重试后仍失败、GlobalLock 失败。
	// 旧签名是单个 bool, 两种事实被压成一个 false, 歧义解到了掩盖失败的那一边
	HoldsText(text string) (holds bool, known bool)
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
// 它们。允许新增, 但新增也要登记进 docs/behavior-contract.md 的清单
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
	s.typingStatus.Store(&TypingStatus{
		Phase: PhaseIdle,
		// 必须有: Progress 的零值是 0, 而 0 的含义是"进度 0%"——前端按
		// progress >= 0 决定显不显示进度条, 漏掉这一项会让空闲态的界面在
		// 启动时凭空长出一条高度 10px 的空进度轨道, 把上方 UI 顶上去;
		// 跑过一次任务后状态被写成 -1, 那条空轨道才消失("只有第一次启动
		// 才看得见"的怪象就是这么来的)。-1 才是"隐藏"
		Progress: -1,
	})
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
//
// 无内容可输入的文本(剔除 \r 后为空)当场拒绝, 不写倒计时、不占任务槽:
// 前端也有一道同样的校验, 但那道只是省一次往返 —— 界面能改、能被绕过,
// 判据必须在后端。"批准一个什么都不做的任务"的代价是用户白等一个倒计时,
// 等来的却是一句报错
func (s *TypingService) Start(text string, delay int, forceSendInput bool, textDirect bool) (string, error) {
	// 与逐字符路径剔除 \r 的口径保持一致(见 runTypingTask), 免得"\r"被当成有内容
	if strings.ReplaceAll(text, "\r", "") == "" {
		return "", fmt.Errorf(msgNothingToType)
	}

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
	// 运行现场一次填好交给任务(原先这里是 9 个形参, 见 taskrun.go)。
	// outcome.clipboardKept 显式写 true, 与拆分前的局部初值一致; 当前逻辑下漏写
	// 并不可观测(读它的终态分支还要求 touchedClipboard, 那只在剪贴板成功路径里
	// 置位), 但别依赖这一点 —— 将来多一条读它的路径, 零值就成了"剪贴板没保住"
	// 这句谎话(同类零值陷阱见 docs/invariants.md「空闲态不许有进度条」)
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
//
// 任务已经有结局(success/error)时不改写状态, 也不递增代数: 那种时候"已取消"
// 是句假话 —— 内容可能已经全部送达(用户看到"已取消"会以为没打完, 再点一次
// 启动就把同一段文本打了两遍), 或者上一条具体失败原因被抹掉。任务在写完结局
// 状态到腾空任务槽之间也在这个窗口里, 所以判据取状态而不是任务槽
func (s *TypingService) Cancel() (string, error) {
	s.cancelFlag.Store(true)

	s.mu.Lock()
	defer s.mu.Unlock()
	if phase := s.Status().Phase; phase == PhaseSuccess || phase == PhaseError {
		return "cancelled", nil
	}
	// 递增代数让在途任务的后续状态写入作废, 并立刻播报"已取消";
	// 此处不等任务退出 —— 旧实现等最长 2 秒, 窗口会冻住
	s.taskGen.Add(1)
	s.typingStatus.Store(&TypingStatus{
		Phase: PhaseCancel, Message: msgCancelled, Progress: -1,
	})
	return "cancelled", nil
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
// 返回 false 的三种情形都意味着原内容可能已丢失: 快照本身没拿到(nil)、
// 恢复时剪贴板被别的程序占着/写回失败, 以及**读不到剪贴板**——最后这种过去
// 被并进"用户已改动"里静默跳过了, 而那时原内容早已被本次注入覆盖,
// 用户却收到一句"输入完成"(见 Clipboard.HoldsText 的说明)。
// 用户已复制新内容时跳过恢复不算失败 —— 剪贴板此刻归用户所有, 正是想要的结果
func (s *TypingService) restoreClipboardSnapshot(snap []ClipboardFormat, injected string) bool {
	if snap == nil {
		return false // 原状态未知, 无从恢复
	}
	holds, known := s.clipboard.HoldsText(injected)
	if !known {
		// 读不到剪贴板: 用户原本的东西还在不在无从判断。此时既不覆盖
		// (可能盖掉用户刚复制的内容), 也不许报"恢复成功", 按未恢复上报
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
