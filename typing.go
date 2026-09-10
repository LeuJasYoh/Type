//go:build windows && (amd64 || arm64)

// ─── 输入任务业务层: 状态机 + 并发守卫 ─────────────────

package main

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

var cancelFlag atomic.Bool
var runningFlag atomic.Bool // 运行中互斥, 防止重复启动
var taskGen atomic.Uint64   // 任务代数: start/cancel 每次递增, 过代任务的状态写入一律作废

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

var typingStatus atomic.Value // 存 *TypingStatus

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

// ─── 粘贴流程 ─────────────────────────────────────────

// restoreClipboardSnapshot 恢复快照: 仅当剪贴板仍为本次注入的文本时执行,
// 避免覆盖用户在注入期间新复制的数据; 原内容为空则直接清空, 不留注入残留
func restoreClipboardSnapshot(snap []clipFormat, injected string) {
	if snap == nil {
		return
	}
	if clipboardGetText() != injected {
		return // 用户期间已复制新内容
	}
	restoreSnapshotRaw(snap)
}

// typeTextViaClipboard 执行剪贴板粘贴输入（两种模式共用）。
// 粘贴前快照全部剪贴板格式, 结束后原样恢复, 不销毁用户已有的
// 图片/文件等非文本内容
func typeTextViaClipboard(text string) bool {
	snap := clipboardSnapshot()
	if !clipboardSetText(text) {
		// EmptyClipboard 可能已执行(分配阶段失败), 直接写回快照
		restoreSnapshotRaw(snap)
		return false
	}
	time.Sleep(100 * time.Millisecond)

	if cancelFlag.Load() {
		restoreClipboardSnapshot(snap, text)
		return false
	}
	sendCtrlV()
	time.Sleep(200 * time.Millisecond)

	restoreClipboardSnapshot(snap, text)
	return true
}

// runTypingTask 执行一次完整的输入任务(倒计时 + 注入)。
// 倒计时初态已由 startTyping 同步写入, 此处从衔接/认领 runningFlag 开始。
func runTypingTask(gen uint64, text string, delay int, forceSendInput bool) {
	// gen 守卫的状态写入: 任务被更新一代的操作取代后, 静默停止输出
	setStatus := func(s *TypingStatus) {
		if gen == taskGen.Load() {
			typingStatus.Store(s)
		}
	}

	// 取消收尾衔接: 等待上一任务释放 runningFlag (取消标志会加速其退出, 上限 2 秒)
	for deadline := time.Now().Add(2 * time.Second); runningFlag.Load(); {
		if gen != taskGen.Load() {
			return // 已被新操作接管, 本次启动作废
		}
		if !time.Now().Before(deadline) {
			setStatus(&TypingStatus{Phase: PhaseError, Message: "启动失败：上一任务未能及时退出", Progress: -1})
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !runningFlag.CompareAndSwap(false, true) {
		return
	}
	defer runningFlag.Store(false)
	if gen != taskGen.Load() {
		return // 等待期间发生了新的取消/启动, 本次启动作废
	}
	// 认领成功后才清取消标志: 过早清除会让尚未退出的上一任务漏检取消而继续注入
	cancelFlag.Store(false)

	cancelled := cancelFlag.Load

	// ── 倒计时 ──
	for i := delay; i > 0; i-- {
		if cancelled() {
			return // cancelTyping 已写入取消状态
		}
		sec := i
		setStatus(&TypingStatus{
			Phase:        PhaseCountdown,
			Message:      fmt.Sprintf("剩余 %d 秒 — 请聚焦目标窗口...", sec),
			SecondsLeft:  sec,
			Progress:     -1,
			TargetWindow: foregroundWindowTitle(),
		})
		time.Sleep(1 * time.Second)
	}
	if cancelled() {
		return
	}

	time.Sleep(150 * time.Millisecond)

	// ── 执行 ──
	success := false
	if containsNonASCII(text) && !forceSendInput {
		setStatus(&TypingStatus{
			Phase: PhaseTyping, Message: "检测到中文，正在操作剪贴板...", Progress: -1,
		})
		success = typeTextViaClipboard(text)
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
				sendVK(VK_RETURN)
			} else {
				sendRune(r)
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
			time.Sleep(charDelay)
		}
		success = !cancelled()
	}

	// 最终状态（前端检测到终止 phase 后停止轮询）; 过代则静默, 状态已由新操作接管
	switch {
	case cancelled():
		// cancelTyping 已写入取消状态
	case success:
		setStatus(&TypingStatus{Phase: PhaseSuccess, Message: "输入完成", Progress: -1})
	default:
		setStatus(&TypingStatus{Phase: PhaseError, Message: "输入失败", Progress: -1})
	}
}
