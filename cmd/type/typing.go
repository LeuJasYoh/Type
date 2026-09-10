//go:build windows && (amd64 || arm64)

// ─── 输入任务业务层: 状态机 + 并发守卫 ─────────────────
// 仅依赖本文件定义的 TextInjector/Clipboard/Foreground 接口,
// 平台细节全部下沉到 win32_*.go; 状态机时序与用户可见文案是稳定契约。

package main

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// ─── 平台能力接口(消费方定义, Win32 实现见 win32_*.go) ──

// TextInjector 字符注入能力
type TextInjector interface {
	SendRune(r rune) // 含全角标点 WM_CHAR 绕行
	SendEnter()
	SendPaste() // Ctrl+V
}

// ClipboardFormat 剪贴板单一格式的原始字节快照
type ClipboardFormat struct {
	Fmt  uint32
	Data []byte
}

// Clipboard 剪贴板能力; Snapshot 返回 nil 表示剪贴板打开失败
// (原状态未知, 调用方应放弃恢复), 空切片表示剪贴板原本为空
type Clipboard interface {
	SetText(text string) bool
	GetText() string
	Snapshot() []ClipboardFormat
	RestoreSnapshotRaw(snap []ClipboardFormat) // 无条件写回(调用方需确认剪贴板未被用户改动)
}

// Foreground 前台窗口探测
type Foreground interface {
	Title() string
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
	Progress     int         `json:"progress"`    // 0-100，-1 表示隐藏
	SecondsLeft  int         `json:"secondsLeft"` // 倒计时剩余
	TargetWindow string      `json:"targetWindow"`
}

// ─── TypingService ────────────────────────────────────

// TypingService 持有输入任务生命周期的全部可变状态
// (取消/运行互斥/任务代数/状态播报), 通过接口使用平台能力
type TypingService struct {
	injector   TextInjector
	clipboard  Clipboard
	foreground Foreground

	cancelFlag   atomic.Bool
	runningFlag  atomic.Bool   // 运行中互斥, 防止重复启动
	taskGen      atomic.Uint64 // 任务代数: start/cancel 每次递增, 过代任务的状态写入一律作废
	typingStatus atomic.Value  // 存 *TypingStatus

	// 倒计时/逐字符/粘贴的等待, 测试可注入假时钟;
	// 取消衔接的 25ms 轮询与 2 秒 deadline 刻意保持真实时钟
	sleep func(time.Duration)
}

func newTypingService(inj TextInjector, cb Clipboard, fg Foreground) *TypingService {
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

// Start 启动一次输入任务(倒计时 + 注入)
func (s *TypingService) Start(text string, delay int, forceSendInput bool) (string, error) {
	// 上一任务活跃且并非取消收尾: 拒绝重入
	if s.runningFlag.Load() && !s.cancelFlag.Load() {
		return "", fmt.Errorf("已有输入任务在运行中，请先取消或等待完成")
	}
	// 同步写入倒计时初态: 前端 await 本调用后才开启轮询,
	// 保证首个 tick 必读到新状态; 过代旧任务被代数守卫拦截, 无法覆盖
	gen := s.taskGen.Add(1)
	s.typingStatus.Store(&TypingStatus{
		Phase:        PhaseCountdown,
		Message:      fmt.Sprintf("剩余 %d 秒 — 请聚焦目标窗口...", delay),
		SecondsLeft:  delay,
		Progress:     -1,
		TargetWindow: s.foreground.Title(),
	})
	go s.runTypingTask(gen, text, delay, forceSendInput)
	return "started", nil
}

// Cancel 取消在途任务(不等待其退出)
func (s *TypingService) Cancel() (string, error) {
	// 递增代数使在途任务的所有后续状态写入作废, 取消标志则加速其退出;
	// 此处不做等待 —— 旧实现阻塞 UI 线程最长 2 秒导致窗口冻结,
	// "取消后立即启动"的衔接由 runTypingTask 自行等待旧任务让出 runningFlag
	s.taskGen.Add(1)
	s.cancelFlag.Store(true)
	s.typingStatus.Store(&TypingStatus{
		Phase: PhaseCancel, Message: "已取消", Progress: -1,
	})
	return "cancelled", nil
}

// runTypingTask 执行一次完整的输入任务(倒计时 + 注入)。
// 倒计时初态已由 Start 同步写入, 此处从衔接/认领 runningFlag 开始。
func (s *TypingService) runTypingTask(gen uint64, text string, delay int, forceSendInput bool) {
	// gen 守卫的状态写入: 任务被更新一代的操作取代后, 静默停止输出
	setStatus := func(st *TypingStatus) {
		if gen == s.taskGen.Load() {
			s.typingStatus.Store(st)
		}
	}

	// 取消收尾衔接: 等待上一任务释放 runningFlag (取消标志会加速其退出, 上限 2 秒)
	for deadline := time.Now().Add(2 * time.Second); s.runningFlag.Load(); {
		if gen != s.taskGen.Load() {
			return // 已被新操作接管, 本次启动作废
		}
		if !time.Now().Before(deadline) {
			setStatus(&TypingStatus{Phase: PhaseError, Message: "启动失败：上一任务未能及时退出", Progress: -1})
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !s.runningFlag.CompareAndSwap(false, true) {
		return
	}
	defer s.runningFlag.Store(false)
	if gen != s.taskGen.Load() {
		return // 等待期间发生了新的取消/启动, 本次启动作废
	}
	// 认领成功后才清取消标志: 过早清除会让尚未退出的上一任务漏检取消而继续注入
	s.cancelFlag.Store(false)

	cancelled := s.cancelFlag.Load

	// ── 倒计时 ──
	for i := delay; i > 0; i-- {
		if cancelled() {
			return // Cancel 已写入取消状态
		}
		sec := i
		setStatus(&TypingStatus{
			Phase:        PhaseCountdown,
			Message:      fmt.Sprintf("剩余 %d 秒 — 请聚焦目标窗口...", sec),
			SecondsLeft:  sec,
			Progress:     -1,
			TargetWindow: s.foreground.Title(),
		})
		s.sleep(1 * time.Second)
	}
	if cancelled() {
		return
	}

	s.sleep(150 * time.Millisecond)

	// ── 执行 ──
	success := false
	if containsNonASCII(text) && !forceSendInput {
		setStatus(&TypingStatus{
			Phase: PhaseTyping, Message: "检测到中文，正在操作剪贴板...", Progress: -1,
		})
		success = s.typeTextViaClipboard(text)
		if !success && !cancelled() {
			setStatus(&TypingStatus{
				Phase: PhaseTyping, Message: "剪贴板操作失败", Progress: -1,
			})
		}
	} else {
		// 剔除 \r 使进度分母与实际注入次数一致 (\r\n 由 \n 触发回车)
		runes := []rune(strings.ReplaceAll(text, "\r", ""))
		total := len(runes)
		setStatus(&TypingStatus{
			Phase: PhaseTyping, Message: fmt.Sprintf("正在逐字符输入 0 / %d ...", total), Progress: 0,
		})

		typed := 0
		for _, r := range runes {
			if cancelled() {
				break
			}
			if r == '\n' {
				s.injector.SendEnter()
			} else {
				s.injector.SendRune(r)
			}
			typed++

			if typed%8 == 0 || typed == total {
				setStatus(&TypingStatus{
					Phase:    PhaseTyping,
					Message:  fmt.Sprintf("正在逐字符输入 %d / %d ...", typed, total),
					Progress: typed * 100 / total,
				})
			}

			// 固定快速延迟: ASCII 8ms, CJK 12ms, 标点 16ms (给 IME 喘息)
			charDelay := 8 * time.Millisecond
			if r > 127 {
				if isCJKPunct(r) {
					charDelay = 16 * time.Millisecond
				} else {
					charDelay = 12 * time.Millisecond
				}
			}
			s.sleep(charDelay)
		}
		success = !cancelled()
	}

	// 最终状态（前端检测到终止 phase 后停止轮询）; 过代则静默, 状态已由新操作接管
	switch {
	case cancelled():
		// Cancel 已写入取消状态
	case success:
		setStatus(&TypingStatus{Phase: PhaseSuccess, Message: "输入完成", Progress: -1})
	default:
		setStatus(&TypingStatus{Phase: PhaseError, Message: "输入失败", Progress: -1})
	}
}

// typeTextViaClipboard 执行剪贴板粘贴输入（两种模式共用）。
// 粘贴前快照全部剪贴板格式, 结束后原样恢复, 不销毁用户已有的
// 图片/文件等非文本内容
func (s *TypingService) typeTextViaClipboard(text string) bool {
	snap := s.clipboard.Snapshot()
	if !s.clipboard.SetText(text) {
		// EmptyClipboard 可能已执行(分配阶段失败), 直接写回快照
		s.clipboard.RestoreSnapshotRaw(snap)
		return false
	}
	s.sleep(100 * time.Millisecond)

	if s.cancelFlag.Load() {
		s.restoreClipboardSnapshot(snap, text)
		return false
	}
	s.injector.SendPaste()
	s.sleep(200 * time.Millisecond)

	s.restoreClipboardSnapshot(snap, text)
	return true
}

// restoreClipboardSnapshot 恢复快照: 仅当剪贴板仍为本次注入的文本时执行,
// 避免覆盖用户在注入期间新复制的数据; 原内容为空则直接清空, 不留注入残留
func (s *TypingService) restoreClipboardSnapshot(snap []ClipboardFormat, injected string) {
	if snap == nil {
		return
	}
	if s.clipboard.GetText() != injected {
		return // 用户期间已复制新内容
	}
	s.clipboard.RestoreSnapshotRaw(snap)
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
