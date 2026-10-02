package typing

import (
	"encoding/json"
	"testing"
)

// ─── 冻结文案: 逐字钉死 ────────────────────────────────
// 这些句子是发布语言的一部分(见 AGENTS.md「行为契约」), 改动会直接落到用户
// 眼前, 因此在这里用字面量再写一遍。看着像自我复读, 但这正是它的用途:
// 别处断言用的是同名常量(改了常量测试照样绿), 只有这里能挡住"顺手润色一下"
//
// 新增文案请一并登记; 已冻结的逐字不得改写
func TestFrozenMessages(t *testing.T) {
	frozen := []struct {
		name string
		got  string
		want string
	}{
		// 倒计时与逐字符进度: 用户盯得最久的两句, 也是去 AI 味清理时误伤过的那句
		{"倒计时(Start 初态)", countdownMessage(5), "剩余 5 秒 — 请聚焦目标窗口..."},
		{"逐字符进度(起点)", progressMessage(0, 12), "正在逐字符输入 0 / 12 ..."},
		{"逐字符进度(途中)", progressMessage(5, 12), "正在逐字符输入 5 / 12 ..."},

		// 剪贴板路径
		{"剪贴板操作失败", msgClipboardFailed, "剪贴板操作失败"},
		{"检测到中文", msgClipboardStart, "检测到中文，正在操作剪贴板..."},

		// 终态
		{"输入完成", msgDone, "输入完成"},
		{"输入失败", msgGenericFailure, "输入失败"},
		{"已取消", msgCancelled, "已取消"},
		{"无内容可输入", msgNothingToType, "无内容可输入"},
		{"剪贴板未恢复", msgClipboardNotRestored, "输入完成，但剪贴板未恢复，原内容可能已丢失"},
		{"未切换到目标窗口", msgFocusStayedOnSelf, "未切换到目标窗口：倒计时结束时焦点仍在 Type，请重新启动后聚焦目标窗口"},

		// 启动阶段的拒绝
		{"重入拒绝", msgAlreadyRunning, "已有输入任务在运行中，请先取消或等待完成"},
		{"上一任务未退出", msgPreviousTaskStuck, "启动失败：上一任务未能及时退出"},

		// 失败原因: 各代表一种不同的落点事实, 不许合并
		{"注入被拒", msgPartialSendInput, "输入中断：目标窗口拒绝了模拟按键，可能其权限高于 Type"},
		{"粘贴被拒", msgPasteRejected, "粘贴未生效：目标窗口拒绝了模拟按键，可能其权限高于 Type"},
		{"漂移: 零注入", msgTargetSwitchedIdle, "输入中断：目标窗口已切换，未输入任何内容"},
		{"漂移: 粘贴后", msgTargetSwitchedPasted, "输入中断：目标窗口已切换，粘贴结果无法确认"},
		{"漂移: 已输入 N 字", msgTargetSwitchedTyped(3), "输入中断：目标窗口已切换，已输入 3 字"},
	}
	for _, c := range frozen {
		if c.got != c.want {
			t.Errorf("%s 已漂移:\n  实际: %q\n  期望: %q", c.name, c.got, c.want)
		}
	}
}

// ─── 跨端契约: 状态 JSON 的形状 ────────────────────────
// 前端按字段名取值(见 frontend/src/types.ts 的镜像), 而两个字段名之间差一个
// 字母是编译期发现不了的: Go 侧照样编译, vue-tsc 照样通过, 只有界面上的
// 倒计时变成 NaN。键集合与 phase 取值都在这里逐字钉住

// 五个 JSON 键名, 一个不多一个不少。少了前端读到 undefined, 多了说明
// 后端加了字段而前端镜像没跟上
var typedStatusKeys = []string{"phase", "message", "progress", "secondsLeft", "targetWindow"}

func TestTypingStatusJSONContract(t *testing.T) {
	st := TypingStatus{
		Phase:        PhaseCountdown,
		Message:      "剩余 3 秒 — 请聚焦目标窗口...",
		Progress:     -1,
		SecondsLeft:  3,
		TargetWindow: "记事本",
	}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("序列化 TypingStatus 失败: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if len(decoded) != len(typedStatusKeys) {
		t.Errorf("JSON 键数量 = %d, want %d: %s", len(decoded), len(typedStatusKeys), raw)
	}
	for _, k := range typedStatusKeys {
		if _, ok := decoded[k]; !ok {
			t.Errorf("JSON 缺少字段 %q: %s", k, raw)
		}
	}
	// 前端按这些类型取值: 数字不能变成字符串, 否则进度条会算出 NaN
	if _, ok := decoded["progress"].(float64); !ok {
		t.Errorf("progress 应是数字: %s", raw)
	}
	if _, ok := decoded["secondsLeft"].(float64); !ok {
		t.Errorf("secondsLeft 应是数字: %s", raw)
	}
	if _, ok := decoded["targetWindow"].(string); !ok {
		t.Errorf("targetWindow 应是字符串: %s", raw)
	}
	if _, ok := decoded["phase"].(string); !ok {
		t.Errorf("phase 应是字符串: %s", raw)
	}
}

// 六个 phase 取值逐个钉住: 前端用它们判断是否终止(只有 success/error/cancel
// 才停止轮询), 值一旦改了, 界面会一直转下去
func TestPhaseValuesFrozen(t *testing.T) {
	phases := []struct {
		got  TypingPhase
		want string
	}{
		{PhaseIdle, "idle"},
		{PhaseCountdown, "countdown"},
		{PhaseTyping, "typing"},
		{PhaseSuccess, "success"},
		{PhaseError, "error"},
		{PhaseCancel, "cancel"},
	}
	for _, p := range phases {
		if string(p.got) != p.want {
			t.Errorf("phase 值已漂移: 实际 %q, 期望 %q", p.got, p.want)
		}
	}
}

// 初始状态是前端第一次轮询读到的: idle, 消息与目标窗口为空, 而 progress 必须是 -1。
//
// progress 不是 0 而是 -1 是有讲究的: 0 的含义是"进度 0%", 前端按 progress >= 0
// 决定显不显示进度条。写成 0 会让空闲态的界面在**启动时**长出一条高度 10px 的空
// 进度轨道(把上方 UI 顶上去), 而跑过一次任务之后状态被写成 -1、那条轨道又消失 ——
// 表现为"只有第一次启动才看得见"的怪现象。字面量断言在这里尤其重要: 这条 bug
// 正是"结构体零值恰好等于一个合法取值"造成的, 靠引用常量断言是看不出来的
func TestInitialStatusIsIdleZeroValue(t *testing.T) {
	svc := NewTypingService(newFakeInjector(), &fakeClipboard{}, fakeForeground{title: "记事本"})
	raw, err := json.Marshal(svc.Status())
	if err != nil {
		t.Fatalf("序列化初始状态失败: %v", err)
	}
	want := `{"phase":"idle","message":"","progress":-1,"secondsLeft":0,"targetWindow":""}`
	if string(raw) != want {
		t.Errorf("初始状态 JSON 已漂移:\n  实际: %s\n  期望: %s", raw, want)
	}
}
