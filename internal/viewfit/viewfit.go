// ─── 内容缩放校正的节流器 ─────────────────────────────
// 页面启动后会把真实内容缩放报上来几拍, 但窗口只该按它重摆有限次: 同一个比值只
// 处理一次, 且按该比值算出的窗口装不下时**不记账**(工作区以后变了还能再试)。
// 算式与实测见 docs/invariants.md「其它不变量」; 怎么摆窗口由调用方的 apply 给

package viewfit

import "sync"

// Fitter 校正节流器。它只认"该不该再校正一次", 不含平台依赖(怎么摆窗口由回调注入)
// —— 刻意不带构建约束, 任何系统上都能 go test(与 internal/typing 同一条理由)
type Fitter struct {
	mu       sync.Mutex
	maxFixes int
	seen     float64
	applied  int
}

// NewFitter maxFixes 是最多校正几次, 超过之后一律不再动窗口
func NewFitter(maxFixes int) *Fitter {
	return &Fitter{maxFixes: maxFixes}
}

// Report 收到一个真实内容缩放比值, 需要校正时调用 apply; apply 返回"是否真的摆成
// 了"(比值非法、或按它算出的窗口装不进工作区时为 false)。返回本次是否校正过。
//
// 记账只发生在 apply 真的成功之后: 失败的那次不许把"同一个值以后再试一次"的机会
// 吃掉 —— 详见文件头与 docs/invariants.md「其它不变量」
func (f *Fitter) Report(dpr float64, apply func() bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	// 同一个比值只处理一次; 值变了(创建初期可能先报一个错的)允许再校正一次
	if f.applied >= f.maxFixes || dpr == f.seen {
		return false
	}
	if !apply() {
		return false
	}
	f.seen = dpr
	f.applied++
	return true
}

// Fixed 已校正的次数(诊断与用例用)
func (f *Fitter) Fixed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.applied
}
