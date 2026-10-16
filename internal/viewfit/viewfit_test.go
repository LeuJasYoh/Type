package viewfit

import (
	"sync"
	"testing"
)

// 同一个比值只校正一次: 页面会连着报上好几拍(创建初期读数不稳), 每拍都摆一次
// 窗口会把窗口摆来摆去
func TestSameRatioAppliedOnce(t *testing.T) {
	f := NewFitter(2)
	calls := 0
	apply := func() bool { calls++; return true }

	if !f.Report(1.75, apply) {
		t.Fatal("第一次上报应当校正")
	}
	if f.Report(1.75, apply) {
		t.Error("同一个比值第二次上报不应再校正")
	}
	if calls != 1 || f.Fixed() != 1 {
		t.Errorf("apply 调用 %d 次, Fixed=%d, want 1/1", calls, f.Fixed())
	}
}

// 比值变了允许再校正一次(创建初期可能先报一个错的), 但总数受上限约束
func TestDifferentRatiosUpToMax(t *testing.T) {
	f := NewFitter(2)
	apply := func() bool { return true }

	if !f.Report(1.0, apply) {
		t.Fatal("第 1 次校正应生效")
	}
	if !f.Report(1.75, apply) {
		t.Fatal("比值变了应允许再校正一次")
	}
	if f.Report(2.0, apply) {
		t.Error("已达上限(maxFixes=2), 不应再校正")
	}
	if f.Fixed() != 2 {
		t.Errorf("Fixed = %d, want 2", f.Fixed())
	}
}

// apply 失败(比值非法或装不进工作区)不记账: 同一个值以后可能就装得下了, 记账会把
// 那次机会吃掉。这是本次抽取最容易被改坏的一条规则
func TestFailedApplyDoesNotConsumeBudget(t *testing.T) {
	f := NewFitter(1)
	ok := false // 第一次: 比值算出的窗口装不进工作区
	apply := func() bool { return ok }

	if f.Report(1.75, apply) {
		t.Error("apply 失败时不该算作校正过")
	}
	if f.Fixed() != 0 {
		t.Fatalf("失败的校正被记了账: Fixed = %d", f.Fixed())
	}
	ok = true
	if !f.Report(1.75, apply) {
		t.Error("同一个比值在 apply 失败后应当还能再试")
	}
	// 上限 1 已用完, 再换一个比值也不该校正
	if f.Report(2.0, apply) {
		t.Error("上限用尽后不应再校正")
	}
}

// 上限为 0 的节流器从不校正(零值语义: 不许把"没配过"当成"不限次数")
func TestZeroMaxNeverApplies(t *testing.T) {
	f := NewFitter(0)
	if f.Report(1.5, func() bool { return true }) {
		t.Error("maxFixes=0 时不应校正")
	}
}

// 并发上报: 校正次数不许超过上限, 且 apply 不得被并发调用(它要摆真窗口)
func TestConcurrentReportsStayWithinBudget(t *testing.T) {
	f := NewFitter(2)
	var active sync.WaitGroup
	var mu sync.Mutex
	inApply, maxInApply := 0, 0
	apply := func() bool {
		mu.Lock()
		inApply++
		if inApply > maxInApply {
			maxInApply = inApply
		}
		mu.Unlock()
		mu.Lock()
		inApply--
		mu.Unlock()
		return true
	}
	for i := 0; i < 32; i++ {
		active.Add(1)
		go func(r float64) {
			defer active.Done()
			f.Report(r, apply)
		}(float64(i%3) + 1)
	}
	active.Wait()
	if f.Fixed() > 2 {
		t.Errorf("并发下校正了 %d 次, 超过上限 2", f.Fixed())
	}
	if maxInApply > 1 {
		t.Errorf("apply 被并发调用: 同时在飞 %d 次", maxInApply)
	}
}
