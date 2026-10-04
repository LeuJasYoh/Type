// ─── 一次输入任务的执行流程 ────────────────────────────
// 从 typing.go 拆出来的: 那边留下"接口 + 状态 + 文案 + 时序常量 + 任务的判定与
// 占领(Start/Cancel/Status)", 这里管"任务占了槽之后怎么走完"。
//
// 拆分前它是一个 256 行、9 个形参、6 个跨 150 行持续改写的局部变量的函数, 读的
// 人要同时维护十来个状态。现在每段是一个方法, 段与段之间只靠 taskRun 的字段交接。
//
// **判定顺序、守卫位置与 sleep 注入点一个都没动**: 它们是 AGENTS.md 逐条记着、
// 40 条用例逐条钉着的行为契约(过代守卫、漂移守卫的三处位置、剪贴板失败后不再退
// 逐字符、倒计时的采样与写状态分离……), 动它们要连着契约一起改。

package typing

import "strings"

// ─── 运行现场 ─────────────────────────────────────────

// taskRun 一次输入任务的运行现场
type taskRun struct {
	// ── 开工前填好, 之后只读 ──
	gen            uint64
	cancelHonored  bool // 出生之前那次取消已经把本次启动作废(见 runTypingTask)
	text           string
	delay          int
	forceSendInput bool
	textDirect     bool
	baseline       ForegroundSample // 启动采样, 只用于倒计时首拍的预览
	slot           *taskSlot        // 本任务的收尾信号
	prev           *taskSlot        // 占领时仍在收尾的上一任务(没有则 nil)

	// ── 执行中填写 ──
	locked  ForegroundSample // 倒计时结束锁定的目标(标识与标题取自同一次采样)
	target  string           // 终态展示用的目标标题
	outcome runOutcome       // 两条注入路径各自填写的落点事实
}

// runOutcome 一次注入的落点事实, 终态文案由它决定。
//
// clipboardKept 的零值是 false、含义却是"剪贴板没保住", 所以构造 taskRun 时显式
// 写 true(与拆分前的 `clipboardOK := true` 初值一致)。当前逻辑下漏写并不可观测:
// 读它的终态分支还要求 touchedClipboard, 而那只在剪贴板成功路径里置位 —— 但别
// 依赖这一点, 将来多一条读它的路径, 零值就变成谎话。同类的坑本仓库踩过一次:
// TypingStatus.Progress 的零值恰好等于合法取值 0(见 AGENTS.md「空闲态不许有进度条」)
type runOutcome struct {
	success          bool   // 整条路径是否成功
	delivered        bool   // 是否有内容真正送达目标窗口
	clipboardKept    bool   // 剪贴板是否仍保有注入前的内容
	touchedClipboard bool   // 剪贴板是否被本次任务动过(只有成功才置位)
	reason           string // 失败的具体原因; 空则由终态兜底成通用文案
}

// dispatchResult 剪贴板路径的三种去向
type dispatchResult int

const (
	dispatchDone     dispatchResult = iota // 走完了(成功或失败), 不再走逐字符
	dispatchFallback                       // 没碰剪贴板, 改用逐字符
	dispatchAborted                        // 已被取消接管, 立即返回(状态由 Cancel 写)
)

// ─── 主流程: 五个阶段 ─────────────────────────────────

// runTypingTask 执行一次完整的输入任务(倒计时 + 注入)。
// 任务槽已由 Start 在临界区内占好, 倒计时初态也已写好。
//
// 它只负责"按原顺序走过五个阶段", 每个阶段是一个方法:
// 收尾 → 开工前置 → 倒计时与锁定 → 分流注入 → 终态
func (s *TypingService) runTypingTask(r *taskRun) {
	defer s.finishTaskSlot(r) // 收尾: 先放信号再腾空任务槽

	if !s.beginRun(r) {
		return // 上一任务没能及时退出, 超时终态已写
	}

	// 出生之前那次取消已经把本次启动作废: 这里必须自己把终态说出来。
	// 不说的话状态就停在 Start 写下的倒计时上 —— 倒计时不是终态, 前端会一直
	// 轮询下去, 用户看到的是永远不动的"剩余 N 秒"(v1.5.6 复核发现的缺口)。
	// 用 gen 当守卫: 已被更新一代接管时不能插嘴, 状态归新任务写。
	// 这一判必须放在上面"清标志"之后: 在那之前看到的标志还是上一轮留下的
	if r.cancelHonored {
		if r.gen == s.taskGen.Load() {
			s.typingStatus.Store(&TypingStatus{Phase: PhaseCancel, Message: msgCancelled, Progress: -1})
		}
		return
	}

	locked, ok := s.runCountdown(r)
	if !ok {
		return // 已取消(状态由 Cancel 写), 或焦点仍在自身(终态已写)
	}
	r.locked, r.target = locked, locked.Title

	if s.runInjection(r) {
		return // 取消已接管状态
	}
	s.reportOutcome(r)
}

// finishTaskSlot 任务收尾: 先放收尾信号再腾空任务槽。
// 顺序不能反 —— 下一个任务等的是信号(它自己的 done), 而"有没有任务在跑"看的是槽
func (s *TypingService) finishTaskSlot(r *taskRun) {
	close(r.slot.done)
	s.mu.Lock()
	if s.prevTask == r.slot {
		s.prevTask = nil
	}
	s.mu.Unlock()
}

// cancelled 本任务是否已被取代或取消。两个判据与拆分前逐字一致:
//   - 代数变了: 更新一代的 start/cancel 已接管, 本任务的后续写入本就作废;
//   - 取消标志置位: 让循环尽快察觉(它会被下一次 Start 清掉, 所以单独看它不够,
//     还得靠代数这条 —— 见 AGENTS.md「任务槽与并发启动」)
//
// 拆分前它是 runTypingTask 里的闭包, 现在按 run 逐个判
func (s *TypingService) cancelled(r *taskRun) bool {
	return r.gen != s.taskGen.Load() || s.cancelFlag.Load()
}

// beginRun 开工前置: 等上一任务停手, 再清掉那个用来催它退出的取消标志。
// 返回 false 表示本任务到此为止(超时终态已写)
func (s *TypingService) beginRun(r *taskRun) bool {
	// 上一任务还在收尾时先等它停手, 免得两路注入交叠。等不到就让界面如实显示
	// "没能启动": 此刻状态是 Start 写下的倒计时, 而倒计时不是终态, 前端会一直
	// 轮询下去。
	// 这里必须用本任务的代数(r.gen)去写, 不能拿 s.taskGen.Load() 当守卫 ——
	// 那是拿自己和自己比, 恒真, 迟到的旧任务会把终态盖到在途的新任务上
	// (v1.5.6 复核发现)
	if r.prev != nil && !s.waitPreviousTask(r.prev) {
		s.storeStatus(r.gen, &TypingStatus{Phase: PhaseError, Message: msgPreviousTaskStuck, Progress: -1})
		return false
	}
	// 上一任务已经停手, 现在才轮到自己当"当前任务": 清掉那个用来催它退出的取消
	// 标志。Start 里刻意没清(那时清会让第二次启动被误判成重入), 这里清才安全
	// —— 此刻再来的取消就是冲着我来的
	s.cancelFlag.Store(false)
	return true
}

// runCountdown 倒计时 + 目标锁定, 返回 (锁定的目标, 是否可以继续注入)。
//
// 目标预览 = 当前前台窗口, countdownTick 节拍采样, 但只在可见内容变化时才写状态:
// 秒边界写一次(文案与旧版逐字符一致), 窗口标识或标题一变立即跟上(用户切到哪个
// 窗口, 预览最多迟一拍), 其余节拍只采样不写。采样与写状态分离后, 取消与切窗的
// 响应从最坏 1 秒缩到 1 拍, 而每秒 10 次的冗余状态写入并不存在; 总时长仍是
// delay 秒 —— 每拍 sleep(countdownTick), 共 delay*countdownTicksPerSec 拍
func (s *TypingService) runCountdown(r *taskRun) (ForegroundSample, bool) {
	shownSec, shown := r.delay, r.baseline
	for i := 0; i < r.delay*countdownTicksPerSec; i++ {
		if s.cancelled(r) {
			return ForegroundSample{}, false // Cancel 已写入取消状态
		}
		sec := r.delay - i/countdownTicksPerSec
		sample := s.foreground.Sample()
		if sec != shownSec || sample != shown {
			shownSec, shown = sec, sample
			s.storeStatus(r.gen, &TypingStatus{
				Phase:        PhaseCountdown,
				Message:      countdownMessage(sec),
				SecondsLeft:  sec,
				Progress:     -1,
				TargetWindow: sample.Title,
			})
		}
		s.sleep(countdownTick)
	}
	if s.cancelled(r) {
		return ForegroundSample{}, false
	}

	s.sleep(lockSettleWait)

	// 执行目标锁定: 倒计时结束时的前台窗口, 贯穿到执行与终态状态。
	// 标识与标题取自同一次采样, 展示的标题一定就是锁定下来的那个窗口;
	// 焦点仍在 Type 自身时注入会落进自己的输入框 —— 明确报错, 不静默打错地方
	locked := s.foreground.Sample()
	if locked.Self {
		s.storeStatus(r.gen, &TypingStatus{
			Phase:    PhaseError,
			Message:  msgFocusStayedOnSelf,
			Progress: -1,
		})
		return ForegroundSample{}, false
	}
	return locked, true
}

// runInjection 按路径分流并执行注入, 返回是否需要立即返回(取消已接管状态)。
// 分流判据与拆分前逐字一致: 含非 ASCII 且没被强制逐字符时才走剪贴板 —— 含中文
// 自动改用粘贴是发布语义(README「为什么含中文会自动改用剪贴板」)
func (s *TypingService) runInjection(r *taskRun) (aborted bool) {
	if !containsNonASCII(r.text) || r.forceSendInput {
		s.runCharPath(r)
		return false
	}
	switch s.runClipboardPath(r) {
	case dispatchAborted:
		return true
	case dispatchFallback:
		s.runCharPath(r)
	}
	return false
}

// runClipboardPath 剪贴板粘贴路径, 三种去向见 dispatchResult。
//
// 快照拿不全时退回逐字符(一个字节都不碰剪贴板: 拿残缺快照恢复会把没抄到的那些
// 格式永久销毁); 失败时不再退逐字符 —— 能走到失败说明剪贴板已经写过或粘贴已经
// 发出, 再打一遍就是注入两遍, 也会用 SendRune 的失败原因把那边的具体原因盖掉
func (s *TypingService) runClipboardPath(r *taskRun) dispatchResult {
	s.storeStatus(r.gen, &TypingStatus{
		Phase: PhaseTyping, Message: msgClipboardStart, Progress: -1,
		TargetWindow: r.target,
	})
	snap := s.clipboard.Snapshot()
	if snap.UnsafeToRestore() {
		// 剪贴板打开失败(原状态未知), 或有的格式没能照抄下来
		return dispatchFallback
	}
	// 失败原因由被调方给出: 剪贴板故障、目标窗口拒收 Ctrl+V、目标窗口切换三者的
	// 处置不同, 不能都退化成通用的"输入失败"
	success, failMsg, clipboardKept := s.typeTextViaClipboard(r.text, r.locked.ID,
		func() bool { return s.cancelled(r) }, snap.Formats)
	r.outcome.success = success
	r.outcome.delivered = success // 粘贴按键被接受即内容已送达
	r.outcome.clipboardKept = clipboardKept
	if success {
		r.outcome.touchedClipboard = true // 剪贴板确实被本任务动过了, 终态要认这个事实
		return dispatchDone
	}
	if s.cancelled(r) {
		return dispatchAborted // Cancel 已写入取消状态
	}
	if failMsg != "" {
		s.storeStatus(r.gen, &TypingStatus{
			Phase: PhaseTyping, Message: failMsg, Progress: -1,
			TargetWindow: r.target,
		})
	}
	r.outcome.reason = failMsg
	return dispatchDone
}

// runCharPath 逐字符注入路径。
//
// 漂移守卫在每个字符注入前比对(判定按顶层窗口标识, 严格); 注入通道按 textDirect
// 分流(文本直投下只有换行还是真按键); 进度每 progressEvery 个字写一次(末字必刷);
// 字符间隔三档见常量区。注入失败即停, 不继续虚报进度
func (s *TypingService) runCharPath(r *taskRun) {
	// 剔除 \r 使进度分母与实际注入次数一致 (\r\n 由 \n 触发回车)
	runes := []rune(strings.ReplaceAll(r.text, "\r", ""))
	total := len(runes)
	s.storeStatus(r.gen, &TypingStatus{
		Phase: PhaseTyping, Message: progressMessage(0, total), Progress: 0,
		TargetWindow: r.target,
	})

	typed := 0
	ok := true
	drifted := false
	for _, ru := range runes {
		if s.cancelled(r) {
			break
		}
		// 漂移守卫: 每次注入前确认前台仍是倒计时结束时锁定的那个窗口。判定按顶层
		// 窗口标识(严格): 输入法候选窗与补全弹窗不是顶层前台窗口, 不会误触发;
		// 用户切走(含切回 Type 自身)则立即停止, 不把剩余内容打进错误的窗口。
		// 检查与注入之间仍有毫秒级窗口, 切换恰好发生在其中时, 最多漏进一两个字符
		if !s.targetHeld(r.locked.ID) {
			drifted = true
			break
		}
		// 注入通道分流: 文本直投把字符(含 Tab, WM_CHAR 可插入制表符)送到文本层
		// (无按键事件), 弹窗劫持与括号配对都挂在 keydown 上因而无从触发; 换行
		// 无法走文本层 —— 实测 Chromium 会过滤 WM_CHAR 的 \n/\r 控制字符, 只能
		// 真按键, 故先经 sendEscaped 用 Esc 关掉可能挂着的弹窗再按回车
		switch {
		case r.textDirect && ru == '\n':
			ok = s.sendEscaped(s.injector.SendEnter)
		case r.textDirect:
			ok = s.injector.SendText(ru)
		case ru == '\n':
			ok = s.injector.SendEnter()
		default:
			ok = s.injector.SendRune(ru)
		}
		if !ok {
			break // 注入被拒: 停止并报错, 不继续虚报进度
		}
		r.outcome.delivered = true
		typed++

		if typed%progressEvery == 0 || typed == total {
			s.storeStatus(r.gen, &TypingStatus{
				Phase:        PhaseTyping,
				Message:      progressMessage(typed, total),
				Progress:     typed * 100 / total,
				TargetWindow: r.target,
			})
		}

		// 固定快速延迟: 三档数值见常量区 (给 IME 喘息)
		charDelay := charDelayASCII
		if ru > 127 {
			if isCJKPunct(ru) {
				charDelay = charDelayPunct
			} else {
				charDelay = charDelayCJK
			}
		}
		s.sleep(charDelay)
	}

	// 三支的判定顺序与拆分前一致: 取消 → 漂移 → 注入被拒
	switch {
	case s.cancelled(r):
		r.outcome.success = false
	case drifted:
		// 已注入的部分无法撤回, 如实报出停在第几个字
		r.outcome.reason = msgTargetSwitchedTyped(typed)
		r.outcome.success = false
	case !ok:
		s.storeStatus(r.gen, &TypingStatus{Phase: PhaseTyping, Message: msgPartialSendInput, Progress: -1, TargetWindow: r.target})
		r.outcome.reason = msgPartialSendInput
		r.outcome.success = false
	default:
		r.outcome.success = true
	}
}

// reportOutcome 写终态(前端检测到终止 phase 后停止轮询); 过代则静默, 状态已由
// 新操作接管。分支顺序与拆分前逐字一致: 取消 → 送达但剪贴板没换回 → 成功 →
// 兜底成功(整段文本无内容可注入) → 失败(保留具体原因)
func (s *TypingService) reportOutcome(r *taskRun) {
	st := &TypingStatus{Progress: -1, TargetWindow: r.target}
	switch {
	case s.cancelled(r):
		return // Cancel 已写入取消状态
	case r.outcome.success && r.outcome.delivered && r.outcome.touchedClipboard && !r.outcome.clipboardKept:
		// 内容已送达, 但剪贴板没能换回原内容: 如实说明, 不报"输入完成"
		st.Phase, st.Message = PhaseSuccess, msgClipboardNotRestored
	case r.outcome.success && r.outcome.delivered:
		st.Phase, st.Message = PhaseSuccess, msgDone
	case r.outcome.success:
		// 兜底: 一个字符都没注入成功(即整段文本无内容可注入)。正常走不到这里 ——
		// 那种文本已被 Start 当场拒掉, 留着是为了将来有别的路径把文本整段剔除时,
		// 界面不会把"什么都没做"报成"输入完成"
		st.Phase, st.Message = PhaseSuccess, msgNothingToType
	default:
		// 保留注入器给出的具体原因(如权限不足), 而不是笼统的"输入失败"
		st.Phase = PhaseError
		if r.outcome.reason != "" {
			st.Message = r.outcome.reason
		} else {
			st.Message = msgGenericFailure
		}
	}
	s.storeStatus(r.gen, st)
}
