//go:build windows && (amd64 || arm64)

// 跨端契约: webview Bind 的函数名与签名, 以及前端 ipc.ts 的镜像。这些名字与参数顺序
// 是 Go 与 JS 之间唯一的事实来源, 而两边各自的编译器都管不到对方 —— 改个名字、换一下
// 两个 bool 的先后, 两边都编译通过、界面照常渲染, 只是功能悄悄错位, 要等用户发现。
// 见 docs/behavior-contract.md「行为契约（冻结，改动需双端同步）」

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/LeuJasYoh/type/internal/typing"
)

// contractBindNames 六个绑定名, 前端 window 上必须是同名的这六个
var contractBindNames = []string{"startTyping", "cancelTyping", "toggleTopmost", "getTopmost", "getTypingStatus", "reportViewport"}

var (
	bindCallRE  = regexp.MustCompile(`w\.Bind\("([A-Za-z]+)"`)
	ipcExportRE = regexp.MustCompile(`(?m)^export const ([A-Za-z]+) = \(([^)]*)\)`)
	// reportViewport 是匿名闭包, 反射够不着, 只能在源码上钉: 它只收一个 float64
	// (devicePixelRatio), 参数个数或类型一变, 内容缩放校正就静默失效 ——
	// 界面回到"输入框压住选项行"的样子, 而没有任何环节会报错
	reportViewportRE = regexp.MustCompile(`w\.Bind\("reportViewport",\s*func\((\w+) float64\) error`)
)

// mainSource 读装配层源码: 绑定名与前端镜像都要对着它核
func mainSource(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("读取 main.go 失败: %v", err)
	}
	return string(data)
}

// 装配层绑定的名字必须正好是约定的六个: 少一个前端会拿到 undefined,
// 多一个说明后端加了能力而前端镜像与文档都没跟上
func TestBindNamesMatchContract(t *testing.T) {
	src := mainSource(t)
	matches := bindCallRE.FindAllStringSubmatch(src, -1)
	got := make([]string, 0, len(matches))
	for _, m := range matches {
		got = append(got, m[1])
	}
	if !reflect.DeepEqual(got, contractBindNames) {
		t.Fatalf("Bind 名 = %v, want %v", got, contractBindNames)
	}
}

// startTyping 的四个参数按位置传递: text/delay/forceSendInput/textDirect。
// 后两个同为 bool, 只数个数不够 —— 互换位置时个数照样是 4, 界面却把"绕过粘贴检测"
// 当成"文本直投"。参数表见 docs/behavior-contract.md「行为契约（冻结，改动需双端同步）」
func TestStartSignatureFrozen(t *testing.T) {
	mt := reflect.TypeOf((*typing.TypingService)(nil).Start)
	want := []reflect.Type{
		reflect.TypeOf(""),
		reflect.TypeOf(int(0)),
		reflect.TypeOf(false),
		reflect.TypeOf(false),
	}
	if mt.NumIn() != len(want) {
		t.Fatalf("Start 参数个数 = %d, want %d", mt.NumIn(), len(want))
	}
	for i, w := range want {
		if mt.In(i) != w {
			t.Errorf("Start 第 %d 个参数类型 = %v, want %v", i+1, mt.In(i), w)
		}
	}
	if mt.NumOut() != 2 || mt.Out(0).Kind() != reflect.String || mt.Out(1) != reflect.TypeOf((*error)(nil)).Elem() {
		t.Errorf("Start 返回值 = %v, want (string, error)", mt)
	}

	// 后两个参数同为 bool, 光看类型分不出谁是谁, 所以把顺序写进注释并由
	// 前端那侧的位置一起核对(见下一个用例)
	t.Log("startTyping(text string, delay int, forceSendInput bool, textDirect bool)")
}

// 另外几个绑定的签名: cancelTyping() / toggleTopmost() bool / getTopmost() bool /
// getTypingStatus() *TypingStatus
func TestOtherSignaturesFrozen(t *testing.T) {
	typeOf := reflect.TypeOf(typing.NewTypingService(nil, nil, nil))

	var cancel, status *reflect.Method
	for i := 0; i < typeOf.NumMethod(); i++ {
		m := typeOf.Method(i)
		switch m.Name {
		case "Cancel":
			cancel = &m
		case "Status":
			status = &m
		}
	}
	if cancel == nil {
		t.Fatal("TypingService 缺少 Cancel 方法")
	}
	if cancel.Type.NumIn() != 1 || cancel.Type.NumOut() != 2 {
		t.Errorf("Cancel 签名 = %v, want () (string, error)", cancel.Type)
	}
	if status == nil {
		t.Fatal("TypingService 缺少 Status 方法")
	}
	if status.Type.NumIn() != 1 || status.Type.NumOut() != 1 {
		t.Fatalf("Status 签名 = %v, want () *TypingStatus", status.Type)
	}
	if got := status.Type.Out(0); got != reflect.TypeOf(&typing.TypingStatus{}) {
		t.Errorf("Status 返回 %v, want *TypingStatus (前端按字段名取值)", got)
	}
}

// reportViewportBody 取 main.go 里 reportViewport 回调体的源码: 从绑定那行起, 到该行
// 收尾的 "\n\t})" 为止。用它钉"回调真的去校正了", 光钉签名是不够的
func reportViewportBody(src string) string {
	i := strings.Index(src, `w.Bind("reportViewport"`)
	if i < 0 {
		return ""
	}
	rest := src[i:]
	if j := strings.Index(rest, "\n\t})"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// reportViewport(dpr float64) 的签名、回调体与前端调用端都要钉: 它是"真实内容缩放"
// 唯一的传递通道。**只钉签名是假绿** —— 回调体掏空成 `return nil`, 或删掉页面的上报
// 调用, 整套闸门全绿而校正彻底失效; 所以连回调体与调用点一起钉。
// 见 docs/invariants.md「其它不变量」
func TestReportViewportSignatureFrozen(t *testing.T) {
	src := mainSource(t)
	if n := len(reportViewportRE.FindAllStringSubmatch(src, -1)); n != 1 {
		t.Fatalf("main.go 里 reportViewport 的绑定形式匹配到 %d 处, want 1: 需要 `w.Bind(\"reportViewport\", func(dpr float64) error`", n)
	}
	body := reportViewportBody(src)
	for _, need := range []string{"win32.WindowClientForScale(", "win32.SetWindowClientRect("} {
		if !strings.Contains(body, need) {
			t.Errorf("reportViewport 的回调体里没有 %s: 校正被掏空了, 而界面只会重新变挤", need)
		}
	}

	ipc := repoFile(t, "frontend", "src", "ipc.ts")
	m := regexp.MustCompile(`(?m)^export const reportViewport = \(([^)]*)\)`).FindStringSubmatch(ipc)
	if m == nil {
		t.Fatal("ipc.ts 里找不到 `export const reportViewport = (...)`")
	}
	if args := strings.TrimSpace(m[1]); args != "dpr: number" {
		t.Errorf("ipc.ts 的 reportViewport 参数 = %q, want %q", args, "dpr: number")
	}
	if !strings.Contains(ipc, "reportViewport(dpr: number): Promise<void>;") {
		t.Error("ipc.ts 的 window 接口里 reportViewport 的声明不是 (dpr: number): Promise<void>")
	}

	// 调用端: 通道两端都在, 页面不调用等于没上报。上报被抽进 viewportReport.ts,
	// 所以"有没有真的启动它"要看 main.ts 的调用点 —— 只断言"模块里出现过某个表达式"
	// 拦不住"调用点被删掉而函数体还在"(独立复核实测: 那样断言照绿)
	if ts := repoFile(t, "frontend", "src", "main.ts"); !strings.Contains(ts, "startViewportReporting()") {
		t.Error("main.ts 没有启动内容缩放上报: 缺 startViewportReporting()")
	}
	// 默认参数才是真正的接线: 读 window.devicePixelRatio, 交给 ipc 的 reportViewport。
	// 匹配的是**默认值本身**(`= reportViewport,`), 不是"文件里出现过 reportViewport" ——
	// 后者在"import 留着、默认值换成空函数"时照样成立(独立复核的变异实测)
	if vr := repoFile(t, "frontend", "src", "viewportReport.ts"); !strings.Contains(vr, "window.devicePixelRatio") ||
		!strings.Contains(vr, "= reportViewport,") {
		t.Error("viewportReport.ts 的默认参数没接上: 需要 window.devicePixelRatio 与 `= reportViewport,`")
	}
}

// 前端镜像: ipc.ts 里声明并导出的四个函数名必须与绑定名一一对应。
// 前端重构改了 window 接口或导出名时, 这里会先红
func TestFrontendMirrorsBindNames(t *testing.T) {
	path := filepath.Join("..", "..", "frontend", "src", "ipc.ts")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v (前端目录被移动了?)", path, err)
	}
	src := string(data)

	declared := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s{4}([A-Za-z]+)\(`).FindAllStringSubmatch(src, -1) {
		declared[m[1]] = true
	}
	for _, name := range contractBindNames {
		if !declared[name] {
			t.Errorf("frontend/src/ipc.ts 的 window 接口里找不到 %q", name)
		}
	}

	exported := map[string]string{} // 名字 -> 参数列表
	for _, m := range ipcExportRE.FindAllStringSubmatch(src, -1) {
		exported[m[1]] = strings.TrimSpace(m[2])
	}
	for _, name := range contractBindNames {
		if _, ok := exported[name]; !ok {
			t.Errorf("frontend/src/ipc.ts 未导出 %q", name)
		}
	}
	// startTyping 是唯一带参数的: 四个, 且**顺序**也要与 Go 侧一致 —— 只数个数不够,
	// 两个 bool 互换位置时个数照样是 4, 界面却把"绕过粘贴检测"当成"文本直投"。
	// 所以这里逐字钉参数表、转发调用与窗口接口声明。
	// 见 docs/behavior-contract.md「行为契约（冻结，改动需双端同步）」
	const wantStartArgs = "text: string, delay: number, forceRaw: boolean, textDirect: boolean"
	if args, ok := exported["startTyping"]; ok && args != wantStartArgs {
		t.Errorf("ipc.ts 的 startTyping 参数 = %q, want %q", args, wantStartArgs)
	}
	if want := "window.startTyping(text, delay, forceRaw, textDirect)"; !strings.Contains(src, want) {
		t.Errorf("ipc.ts 没有按 %q 原样转发: 参数顺序错位会静默交换两个开关", want)
	}
	if want := "startTyping(text: string, delay: number, forceRaw: boolean, textDirect: boolean): Promise<string>;"; !strings.Contains(src, want) {
		t.Errorf("ipc.ts 的 window 接口里 startTyping 的声明与约定不符, want %q", want)
	}
	for _, name := range []string{"cancelTyping", "toggleTopmost", "getTopmost", "getTypingStatus"} {
		if args, ok := exported[name]; ok && strings.TrimSpace(args) != "" {
			t.Errorf("ipc.ts 的 %s 应当无参, 实际 (%s)", name, args)
		}
	}
}

// ─── 前端镜像: types.ts / 倒计时文案 / 主题防闪烁脚本 ─────

// repoFile 读仓库内的文本文件(路径相对 cmd/type)
func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, parts...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v (文件被移动了?)", path, err)
	}
	return string(data)
}

// packageGoSource 拼接一个包目录下全部非 _test.go 的 .go 文件文本: 契约钉的是"符号在
// 包里", 不是"符号在某个文件里", 文件拆分搬家不该撞红。必须排除 _test.go —— 契约正则的
// 模式串自己就写在测试里, 扫进去会"自己吃掉自己", 断言变成永真
func packageGoSource(t *testing.T, dir ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, dir...)...)
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("读取包目录 %s 失败: %v (目录被移动了?)", path, err)
	}
	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(path, name))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", filepath.Join(path, name), err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}

var (
	tsStatusBodyRE = regexp.MustCompile(`(?s)export interface TypingStatus \{(.*?)\n\}`)
	tsFieldRE      = regexp.MustCompile(`(?m)^\s*([A-Za-z_][A-Za-z0-9_]*)\??\s*:`)
	tsPhaseBodyRE  = regexp.MustCompile(`(?s)export type TypingPhase =(.*?);`)
	tsPhaseLitRE   = regexp.MustCompile(`'([a-z]+)'`)
	goPhaseDeclRE  = regexp.MustCompile(`(?m)Phase[A-Za-z]+\s+TypingPhase\s*=\s*"([a-z]+)"`)
	// 倒计时文案: 后端是 msgCountdownFormat 的字面量, 前端为了不等第一次轮询
	// 自己拼了一份反引号模板串。把 ${...} 归一成 %d 之后, 两者必须逐字相同
	goCountdownFormatRE = regexp.MustCompile(`(?m)^\s*msgCountdownFormat\s*=\s*"([^"]*)"`)
	tsCountdownLitRE    = regexp.MustCompile("`([^`]*剩余[^`]*)`")
	tsInterpRE          = regexp.MustCompile(`\$\{[^}]*\}`)
)

// TypingStatus 的 JSON 键以 Go 结构体的 tag 为准。前端少一个键, 运行时读到
// undefined(倒计时变成 NaN); 多一个键说明后端删了字段而镜像没跟上
func TestFrontendTypesStatusFields(t *testing.T) {
	body := tsStatusBodyRE.FindStringSubmatch(repoFile(t, "frontend", "src", "types.ts"))
	if body == nil {
		t.Fatal("frontend/src/types.ts 里找不到 export interface TypingStatus")
	}
	got := map[string]bool{}
	for _, f := range tsFieldRE.FindAllStringSubmatch(body[1], -1) {
		got[f[1]] = true
	}

	rt := reflect.TypeOf(typing.TypingStatus{})
	want := map[string]bool{}
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			t.Fatalf("TypingStatus.%s 缺少 json tag", rt.Field(i).Name)
		}
		want[name] = true
	}
	for name := range want {
		if !got[name] {
			t.Errorf("types.ts 的 TypingStatus 缺少字段 %q (Go 端有这个 JSON 键)", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("types.ts 的 TypingStatus 多出字段 %q (Go 端没有这个 JSON 键)", name)
		}
	}

	// 字段的**类型**也要钉: 只钉键名的话 `progress: string` 一样能通过, 而前端拿它
	// 做 `progress >= 0` 比较, 进度条永远不显示(vue-tsc 只管前端那份, 后端仍发数字)
	wantTypes := map[string]string{
		"phase":        "TypingPhase",
		"message":      "string",
		"progress":     "number",
		"secondsLeft":  "number",
		"targetWindow": "string",
	}
	for name, ty := range wantTypes {
		re := regexp.MustCompile(`(?m)^\s*` + name + `\??\s*:\s*([^;\n]+?)\s*;`)
		m := re.FindStringSubmatch(body[1])
		if m == nil {
			t.Errorf("types.ts 的 TypingStatus 里读不出字段 %q 的类型", name)
			continue
		}
		if fieldType := strings.TrimSpace(m[1]); fieldType != ty {
			t.Errorf("types.ts 的 %s 类型 = %q, want %q", name, fieldType, ty)
		}
	}
}

// phase 取值对着 internal/typing 的常量声明核。前端多一个取值, 界面会进入
// 一个后端永不产生的分支; 少一个取值, 该 phase 的样式与终止判断全部失效
func TestFrontendTypesPhaseValues(t *testing.T) {
	body := tsPhaseBodyRE.FindStringSubmatch(repoFile(t, "frontend", "src", "types.ts"))
	if body == nil {
		t.Fatal("frontend/src/types.ts 里找不到 export type TypingPhase")
	}
	got := map[string]bool{}
	for _, p := range tsPhaseLitRE.FindAllStringSubmatch(body[1], -1) {
		got[p[1]] = true
	}

	want := map[string]bool{}
	for _, p := range goPhaseDeclRE.FindAllStringSubmatch(packageGoSource(t, "internal", "typing"), -1) {
		want[p[1]] = true
	}
	if len(want) == 0 {
		t.Fatal("internal/typing 包里找不到 TypingPhase 常量声明")
	}
	for p := range want {
		if !got[p] {
			t.Errorf("types.ts 的 TypingPhase 缺少取值 %q (Go 端有这个 phase)", p)
		}
	}
	for p := range got {
		if !want[p] {
			t.Errorf("types.ts 的 TypingPhase 多出取值 %q (Go 端没有这个 phase)", p)
		}
	}
}

// 倒计时那句在仓库里有三份(后端 msgCountdownFormat、typing/contract_test.go 的字面量、
// 前端为了不等第一次轮询自己拼的这份); 前端这份此前只有弱断言, 长破折号换成短横线
// 或删掉"秒"后的空格照样绿。现在插值归一成 %d 后与后端格式串逐字比较, 差一个字符就红。
// 见 docs/behavior-contract.md「行为契约（冻结，改动需双端同步）」
func TestFrontendCountdownMessageMirrorsGo(t *testing.T) {
	src := repoFile(t, "frontend", "src", "composables", "useTypingTask.ts")
	lit := tsCountdownLitRE.FindStringSubmatch(src)
	if lit == nil {
		t.Fatal("useTypingTask.ts 里找不到含倒计时文案的反引号模板串")
	}
	wants := goCountdownFormatRE.FindAllStringSubmatch(packageGoSource(t, "internal", "typing"), -1)
	if len(wants) != 1 {
		t.Fatalf("internal/typing 包里 msgCountdownFormat 的字面量应恰好 1 处, 实际 %d 处: 取到的值不可信", len(wants))
	}
	want := wants[0]
	if got := tsInterpRE.ReplaceAllString(lit[1], "%d"); got != want[1] {
		t.Errorf("前端倒计时文案与后端不一致:\n  前端(插值归一后): %q\n  后端: %q", got, want[1])
	}
}

// 主题的存储键写在两处(首帧内联脚本读、useTheme 写), 走散症状是"切换过主题、重启又
// 变回系统主题"; 内联脚本还必须排在入口脚本之前, 挪进 Vue 就等于首帧闪白。判定只许
// 有一处: useTheme 再算一遍(读存储 + 跟随系统)就多出一份会走散的逻辑。
// 见 docs/invariants.md「其它不变量」
func TestThemeBootScriptKeepsKeyAndOrder(t *testing.T) {
	ts := repoFile(t, "frontend", "src", "composables", "useTheme.ts")
	m := regexp.MustCompile(`THEME_KEY = '([^']+)'`).FindStringSubmatch(ts)
	if m == nil {
		t.Fatal("useTheme.ts 里找不到 THEME_KEY 字面量")
	}
	if strings.Contains(ts, "localStorage.getItem") || strings.Contains(ts, "matchMedia") {
		t.Error("useTheme.ts 又自己判了一遍主题: 判定只许留在 index.html 的内联脚本里")
	}

	html := repoFile(t, "frontend", "index.html")
	boot := strings.Index(html, "localStorage.getItem('"+m[1]+"')")
	if boot < 0 {
		t.Fatalf("index.html 的防闪烁脚本没有读存储键 %q (与 useTheme.ts 不一致?)", m[1])
	}
	if !strings.Contains(html[boot:], "classList.add('dark')") {
		t.Error("index.html 的防闪烁脚本没有把 .dark 挂到 <html> 上")
	}
	entry := strings.Index(html, `<script type="module"`)
	if entry < 0 {
		t.Fatal("index.html 里找不到入口 module script")
	}
	if boot > entry {
		t.Error("防闪烁脚本必须排在入口 module script 之前, 否则首帧会闪一下")
	}
}

// ─── 前端镜像: --ui-scale 的基准宽度 ───────────────────

// 前端 --ui-scale 的基准宽度必须等于宿主窗口的宽度下限: 宿主把窗口下限定在 540
// (internal/win32 的 MinWindowW), 前端说到 540 为止不缩放; 只改一处时两边都不报错,
// 界面要么开始缩放要么不再缩放(这类"跨端同一个数字"此前漂移过: 旧的 480/720)。
// 见 docs/invariants.md「其它不变量」
func TestUiScaleBaseMatchesWindowFloor(t *testing.T) {
	ts := repoFile(t, "frontend", "src", "composables", "useUiScale.ts")
	m := regexp.MustCompile(`BASE_WIDTH\s*=\s*(\d+)`).FindStringSubmatch(ts)
	if m == nil {
		t.Fatal("useUiScale.ts 里找不到 BASE_WIDTH 的字面量")
	}
	goSrc := packageGoSource(t, "internal", "win32")
	gs := regexp.MustCompile(`MinWindowW\s*=\s*(\d+)`).FindAllStringSubmatch(goSrc, -1)
	if len(gs) != 1 {
		t.Fatalf("internal/win32 包里 MinWindowW 的字面量应恰好 1 处, 实际 %d 处: 取到的值不可信", len(gs))
	}
	g := gs[0]
	if m[1] != g[1] {
		t.Errorf("前端缩放基准 BASE_WIDTH = %s, 宿主窗口宽度下限 MinWindowW = %s: 两者必须一致",
			m[1], g[1])
	}
}
