// ─── 一次输入任务的执行流程 ────────────────────────────
// typing.go 留下"接口 + 状态 + 文案 + 时序常量 + 任务的判定与占领(Start/Cancel/
// Status)", 这里管"任务占了槽之后怎么走完"。**判定顺序、守卫位置与 sleep 注入点
// 一个都没动** —— 见 docs/invariants.md「任务槽与并发启动」; 它们是用例逐条钉着的
// 行为契约, 动它们要连着契约一起改

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
// clipboardKept 的零值 false 含义却是"剪贴板没保住", 构造 taskRun 时必须显式写
// true; 同类的零值陷阱(Progress)见 docs/invariants.md「其它不变量」
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

// runTypingTask 执行一次完整的输入任务(倒计时 + 注入): 任务槽已由 Start 在
// 临界区内占好, 倒计时初态也已写好。按原顺序走过五个阶段, 每段是一个方法 ——
// 收尾 → 开工前置 → 倒计时与锁定 → 分流注入 → 终态
func (s *TypingService) runTypingTask(r *taskRun) {
	defer s.finishTaskSlot(r) // 收尾: 先放信号再腾空任务槽

	if !s.beginRun(r) {
		return // 上一任务没能及时退出, 超时终态已写
	}

	// 出生之前那次取消已作废本次启动: 必须自己把终态说出来, 否则状态停在 Start
	// 写下的倒计时上 —— 见 docs/invariants.md「任务槽与并发启动」。这一判必须排在
	// 上面清标志之后: 在那之前看到的标志还是上一轮留下的
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

// cancelled 本任务是否已被取代或取消: 代数变了(更新一代已接管)或取消标志置位
// (让循环尽快察觉; 它会被下一次 Start 清掉, 所以还得靠代数这条) ——
// 见 docs/invariants.md「任务槽与并发启动」
func (s *TypingService) cancelled(r *taskRun) bool {
	return r.gen != s.taskGen.Load() || s.cancelFlag.Load()
}

// beginRun 开工前置: 等上一任务停手, 再清掉那个用来催它退出的取消标志。
// 返回 false 表示本任务到此为止(超时终态已写)
func (s *TypingService) beginRun(r *taskRun) bool {
	// 上一任务还在收尾时先等它停手, 免得两路注入交叠; 等不到就如实播报"没能
	// 启动"(此刻状态还是倒计时, 不是终态)。这里的守卫必须用本任务自己的代数,
	// 不能是 s.taskGen.Load() —— 自比较恒真, 见 docs/invariants.md「任务槽与并发启动」
	if r.prev != nil && !s.waitPreviousTask(r.prev) {
		s.storeStatus(r.gen, &TypingStatus{Phase: PhaseError, Message: msgPreviousTaskStuck, Progress: -1})
		return false
	}
	// 上一任务已停手, 现在才轮到自己当"当前任务": 清掉那个用来催它退出的取消
	// 标志 —— Start 里刻意没清, 见 docs/invariants.md「任务槽与并发启动」
	s.cancelFlag.Store(false)
	return true
}

// runCountdown 倒计时 + 目标锁定, 返回 (锁定的目标, 是否可以继续注入)。
// 预览 = 当前前台窗口(所见即所选, 不做排除) —— 见 docs/invariants.md「目标窗口与轮询」;
// 每拍只采样、秒边界或内容变化才写状态, 见 docs/invariants.md「焦点锁定与漂移防护」
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

	// 执行目标锁定: 倒计时结束时的前台窗口, 标识与标题取自同一次采样。
	// 焦点仍在 Type 自身时注入会落进自己的输入框, 明确报错 ——
	// 见 docs/invariants.md「目标窗口与轮询」
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

// runInjection 按分支执行注入, 返回是否要立即返回(取消已接管状态): 含非 ASCII 且
// 没被强制逐字符才走剪贴板(发布语义见 README「为什么含中文会自动改用剪贴板」)
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

// runClipboardPath 剪贴板粘贴路径, 三种去向见 dispatchResult; 不完整快照退回
// 逐字符, 失败后不再退 —— 见 docs/invariants.md「其它不变量」
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
	// 失败原因由被调方给出: 三者处置不同, 不能都退化成通用的"输入失败"
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

// runCharPath 逐字符注入路径: 每个字符注入前比对顶层窗口标识(漂移守卫), 注入
// 通道按 textDirect 分流; 注入失败即停, 不继续虚报进度 ——
// 见 docs/invariants.md「文本直投」
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
		// 漂移守卫: 每次注入前确认前台仍是锁定目标(按顶层标识) ——
		// 见 docs/invariants.md「焦点锁定与漂移防护」
		if !s.targetHeld(r.locked.ID) {
			drifted = true
			break
		}
		// 文本直投把字符(含 Tab)送到文本层; 换行无法走文本层(Chromium 过滤
		// \n/\r), 只有它仍需真按键 —— 见 docs/invariants.md「文本直投」
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

// reportOutcome 写终态(前端检测到终止 phase 后停止轮询), 过代则静默; 分支顺序:
// 取消 → 送达但剪贴板没换回 → 成功 → 兜底 → 失败(保留具体原因)
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
