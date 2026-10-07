//go:build windows && (amd64 || arm64)

package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ─── 注释契约的闸门 ───────────────────────────────────
// 规范在 docs/comment-style.md。这组检查是"注释重写"期间的安全网: 重写不动行为,
// 所以没有一条行为测试会发现它变差 —— 最可能的静默降级是长块悄悄加长、指针换个
// 格式(旧闸门认不出就不查了)、台账漏行。
//
// 基线**只减不增**: 某个文件的实际数降到基线以下时顺手调低, 否则等于闸门放水。
// 覆盖范围是"重写顺序里的全部区域": tools、frontend/src、frontend/test、
// frontend/tools、cmd、internal —— 第一版漏掉 tools 与前端 test/tools,
// 独立核验当场拿现仓库的反例指出来了(useUiScale.ts 的 21 行 // 块、
// layout-probe.mjs 的 37 行块)。

// commentScanDirs 要扫的源码目录
var commentScanDirs = [][]string{
	{"cmd"}, {"internal"}, {"tools"},
	{"frontend", "src"}, {"frontend", "test"}, {"frontend", "tools"},
}

// commentScanExts 认得出来的扩展名(前端与工具的 .mjs 也算, 那是全仓最长的注释块所在)
var commentScanExts = []string{".go", ".ts", ".vue", ".mjs"}

// commentFiles 返回 相对路径(斜杠形式) -> 文件内容
func commentFiles(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..")
	out := make(map[string]string)
	for _, parts := range commentScanDirs {
		base := filepath.Join(append([]string{root}, parts...)...)
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil // 目录不存在或读不动都跳过
			}
			if !containsString(commentScanExts, filepath.Ext(path)) {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return nil
			}
			out[filepath.ToSlash(rel)] = string(data)
			return nil
		})
		if err != nil {
			t.Fatalf("遍历 %s 失败: %v", base, err)
		}
	}
	if len(out) == 0 {
		t.Fatal("一个源码文件都没扫到: 目录列表过期了?")
	}
	return out
}

// TestNoBlockCommentsInGo Go 源码里不许有块注释。
//
// 理由不是审美: cmd/type/build_contract_test.go 的 codeLines 只剥整行 `//`,
// 块注释会被当成代码参与版本号与构建命令行的 Contains 断言。判定走 go/parser,
// 别用字符串找 "/*": 那会连**字符串字面量里的**斜杠星号一起命中 —— 本闸门第一版
// 就是这么把自己也报了一遍。前端不在此列, /** */ 是 TS 的生态惯例
func TestNoBlockCommentsInGo(t *testing.T) {
	for rel, src := range commentFiles(t) {
		if filepath.Ext(rel) != ".go" {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, rel, src, parser.ParseComments)
		if err != nil {
			t.Errorf("%s 解析失败: %v", rel, err)
			continue
		}
		for _, cg := range f.Comments {
			if !strings.HasPrefix(cg.List[0].Text, "/*") {
				continue
			}
			t.Errorf("%s:%d 出现块注释: 改用整行 //（docs/comment-style.md「长度预算」）",
				rel, fset.Position(cg.Pos()).Line)
		}
	}
}

// longCommentMax 单块注释的行数上限; 超过即违反预算
const longCommentMax = 10

// longCommentBudget 允许超额的块数, 现状基线, 只减不增。
// Go 数连续 `//`; 前端与 .mjs 数 `//` 与 `/* */` 之和(两种都能承载长叙事)。
// **阶段 2 全部四批完成后已全数为 0**: 键保留是为了保住"基线里的文件必须被扫到"这条检查
var longCommentBudget = map[string]int{
	"cmd/type/build_contract_test.go":        0,
	"cmd/type/main.go":                       0,
	"internal/typing/typing.go":              0,
	"internal/win32/win32_clipboard.go":      0,
	"internal/win32/win32_instance.go":       0,
	"internal/win32/win32_keyboard.go":       0,
	"internal/win32/win32_test.go":           0,
	"internal/win32/win32_window.go":         0,
	"tools/equivcheck/main.go":               0,
	"tools/mkres/main.go":                    0,
	"tools/pecheck/main.go":                  0,
	"tools/wmcharprobe/main.go":              0,
	"frontend/src/composables/useUiScale.ts": 0,
	"frontend/test/typingTask.test.ts":       0,
	"frontend/tools/layout-probe.mjs":        0,
}

// longLineComments 数一段源码里超过 max 行的连续整行 // 注释块
func longLineComments(src string, max int) int {
	run, count := 0, 0
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			run++
			continue
		}
		if run > max {
			count++
		}
		run = 0
	}
	if run > max {
		count++
	}
	return count
}

// longBlockComments 数超过 max 行的 /* */ 块(前端用; Go 里它们已被上面那条禁用)。
// 只认行首起头的块: 行中起头的那种(代码后跟 /*) 结构上就写不长, 暂不计
func longBlockComments(src string, max int) int {
	run, count := 0, 0
	in := false
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case !in && strings.HasPrefix(trimmed, "/*"):
			if strings.Contains(trimmed[2:], "*/") {
				continue // 单行块, 不可能超预算
			}
			in, run = true, 1
		case in:
			run++
			if strings.Contains(trimmed, "*/") {
				in = false
				if run > max {
					count++
				}
			}
		}
	}
	if in && run > max {
		count++
	}
	return count
}

// TestCommentBlockBudget 注释块长度预算, 超额数不许超过基线。
// 实际数低于基线时打印出来: 那是把基线调下去的时机, 基线不降等于放水
func TestCommentBlockBudget(t *testing.T) {
	seen := make(map[string]bool)
	for rel, src := range commentFiles(t) {
		var got int
		switch filepath.Ext(rel) {
		case ".go":
			got = longLineComments(src, longCommentMax)
		default:
			got = longLineComments(src, longCommentMax) + longBlockComments(src, longCommentMax)
		}
		want := longCommentBudget[rel]
		seen[rel] = true
		switch {
		case got > want:
			t.Errorf("%s 有 %d 个超过 %d 行的注释块, 基线 %d: 上移或拆成多块（docs/comment-style.md「归置判据」）",
				rel, got, longCommentMax, want)
		case got < want:
			t.Logf("%s 现在只有 %d 个超额块(基线 %d): 可以把基线调下来", rel, got, want)
		}
	}
	for rel := range longCommentBudget {
		if !seen[rel] {
			t.Errorf("基线里的 %s 没被扫到: 文件改名或移走了? 基线要跟着改", rel)
		}
	}
}

// docRefAnyRE 任意形式的跨文件指向; docRefStrictRE 是冻结格式。
// 字符类放宽到字母数字与 `_./-`: 窄成 [a-z-]+ 时 `docs/ci-2026.md` 这类引用
// 两个闸门都看不见; `见` 后的空白也做归一(半角、Tab、全角空格都算)
var (
	docRefAnyRE    = regexp.MustCompile(`见[ \t\x{3000}]+(docs/[A-Za-z0-9._/-]+\.md)`)
	docRefStrictRE = regexp.MustCompile(`见[ \t\x{3000}]+(docs/[A-Za-z0-9._/-]+\.md)「([^」]+)」`)
)

// bareDocRefBudget 允许"不带「小节」"的裸引用数, 现状基线, 只减不增。
// 阶段 2 完成后全数为 0: 四条裸引用要么改成了冻结格式, 要么随文件重写消失
var bareDocRefBudget = map[string]int{
	"internal/typing/contract_test.go":          0,
	"internal/win32/win32_webview2.go":          0,
	"frontend/src/components/statusBarState.ts": 0,
}

// TestDocRefFormatFrozen 指针必须是冻结格式, 且指向的文档真的存在。
//
// 旧闸门只认冻结格式的引用, 所以裸引用不是"宽松"而是**整条逃检**; 而"只数个数"
// 还漏掉另一头: 把裸引用改成指向不存在的文档, 计数不变、照样绿 —— 所以这里对
// 每一条指向都做一次目标存在性检查(与 build_contract_test.go 的 resolve 用例
// 同一个判据, 只是那条看不见裸引用)。
// 本文件的注释与字符串里刻意不出现"见 + 空格 + docs/路径"这种组合, 否则会命中
// 自己; 示例用中文占位(与 build_contract_test.go 同一个坑)
func TestDocRefFormatFrozen(t *testing.T) {
	root := filepath.Join("..", "..")
	seen := make(map[string]bool)
	for rel, src := range commentFiles(t) {
		for _, m := range docRefAnyRE.FindAllStringSubmatch(src, -1) {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(m[1]))); err != nil {
				t.Errorf("%s 指向 %s, 但该文件不存在", rel, m[1])
			}
		}
		all := len(docRefAnyRE.FindAllString(src, -1))
		strict := len(docRefStrictRE.FindAllString(src, -1))
		if all == strict {
			continue
		}
		bare := all - strict
		seen[rel] = true
		if want := bareDocRefBudget[rel]; bare > want {
			t.Errorf("%s 有 %d 条裸引用(基线 %d): 改写成「见 docs/某文档.md「某小节」」这种格式", rel, bare, want)
		} else if bare < want {
			t.Logf("%s 裸引用降到 %d(基线 %d): 可以把基线调下来", rel, bare, want)
		}
	}
	for rel := range bareDocRefBudget {
		if !seen[rel] {
			t.Logf("基线里的 %s 已经没有裸引用了: 从裸引用基线里删除", rel)
		}
	}
}

// ledgerArea 一份台账碎片的约束: 文件名、ID 前缀、应有条数、来源允许的目录前缀
type ledgerArea struct {
	file          string
	idPrefix      string
	minRows       int
	allowedSource []string
}

// commentLedgerAreas 台账碎片(用完不必删, 这几条常量是条数下限)。
// minRows 是**下限**: 整行被删掉时这里会红 —— 第一版只校验"已存在的行",
// 删掉规范点名的那条(T-029)照样全绿, 独立核验用一个反例就拆穿了
var commentLedgerAreas = []ledgerArea{
	{"typing.md", "T-", 43, []string{"internal/typing/", "docs/"}},
	{"win32.md", "W-", 43, []string{"internal/win32/", "docs/"}},
	{"app.md", "A-", 42, []string{"cmd/", "frontend/", "tools/", "docs/"}},
}

// commentLedgerCategories 类别取值
var commentLedgerCategories = []string{"A", "B", "C", "D"}

// commentLedgerDispositions 去向的合法开头
var commentLedgerDispositions = []string{"留原地", "上移", "转测试", "删"}

// ledgerIDRE 台账 ID 的形状: 一个区域字母 + 三位序号
var ledgerIDRE = regexp.MustCompile(`^[TWA]-\d{3}$`)

// ledgerSourceRE 来源列里单个引用: 路径, 可带 `:行号` 或 `:行号-行号`,
// 一个路径下可以跟多个行段(逗号分隔), 多个来源之间用 `;` 分隔
var ledgerSourceRE = regexp.MustCompile(`^([A-Za-z0-9_./-]+)(?::\d+(?:-\d+)?(?:,\d+(?:-\d+)?)*)?$`)

// isLedgerSeparator 判断 Markdown 表的分隔行: 单元格只由 `-`、`:`、空白组成。
// 归一它是因为合法写法不止 `|---|` 一种(`|:---|`、`| --- |`), 不归一就会把分隔行
// 当成数据行报出 5 条假错 —— 独立核验用 `|:---|` 实测过
func isLedgerSeparator(line string) bool {
	for _, cell := range strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|") {
		if strings.Trim(strings.TrimSpace(cell), "-: ") != "" {
			return false
		}
	}
	return true
}

// TestCommentLedgerHasDisposition 台账每一条都要有合法 ID、类别、去向, 条数不许缩水。
//
// 它不判断"事实写得对不对"(那是人的事), 但保证三件可机检的事: 没有整行消失、
// ID 不重复且前缀对得上区域、来源指向的文件真的存在且属于本区域。
// **来源只校验到"文件存在 + 区域前缀"**: 重写期行号必然漂移, 内容级比对会制造假红,
// 事实语义对不对仍只能人查
func TestCommentLedgerHasDisposition(t *testing.T) {
	root := filepath.Join("..", "..")
	dir := filepath.Join(root, "docs", "comment-ledger")
	ids := make(map[string]string)
	for _, area := range commentLedgerAreas {
		data, err := os.ReadFile(filepath.Join(dir, area.file))
		if err != nil {
			t.Errorf("台账缺 %s: %v", area.file, err)
			continue
		}
		rows := 0
		for i, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "|") || isLedgerSeparator(line) {
				continue
			}
			cells := strings.Split(strings.Trim(line, "|"), "|")
			// 表头靠首格判断(不区分大小写): 用 Contains("| ID |") 会连"事实里引用了
			// 表头格式"的数据行一起跳过, 也会让 `| id |` 这种写法被当成数据行
			if len(cells) > 0 && strings.EqualFold(strings.TrimSpace(cells[0]), "ID") {
				continue
			}
			if len(cells) != 5 {
				t.Errorf("%s:%d 有 %d 列, 要 5 列(格式在 docs/comment-style.md 的台账一节)", area.file, i+1, len(cells))
				continue
			}
			rows++
			id := strings.TrimSpace(cells[0])
			if !ledgerIDRE.MatchString(id) {
				t.Errorf("%s:%d ID %q 不是 区域字母-三位序号 的形式", area.file, i+1, id)
			} else {
				if !strings.HasPrefix(id, area.idPrefix) {
					t.Errorf("%s:%d ID %q 的前缀不是 %s", area.file, i+1, id, area.idPrefix)
				}
				if prev, dup := ids[id]; dup {
					t.Errorf("%s:%d ID %q 与 %s 里的重复", area.file, i+1, id, prev)
				}
				ids[id] = area.file
			}
			cat := strings.TrimSpace(cells[3])
			if !containsString(commentLedgerCategories, cat) {
				t.Errorf("%s:%d 类别 %q 非法, 只认 A/B/C/D", area.file, i+1, cat)
			}
			disp := strings.TrimSpace(cells[4])
			if !hasAnyPrefix(disp, commentLedgerDispositions) {
				t.Errorf("%s:%d 去向 %q 不以 %s 之一开头", area.file, i+1, disp, strings.Join(commentLedgerDispositions, "/"))
			}
			for _, seg := range strings.Split(strings.TrimSpace(cells[2]), ";") {
				seg = strings.TrimSpace(seg)
				if seg == "" {
					continue
				}
				m := ledgerSourceRE.FindStringSubmatch(seg)
				if m == nil {
					t.Errorf("%s:%d 来源段 %q 不是 路径:行号 的形式", area.file, i+1, seg)
					continue
				}
				rel := filepath.ToSlash(m[1])
				if !hasAnyPrefix(rel, area.allowedSource) {
					t.Errorf("%s:%d 来源 %s 不属于本区域(%s)", area.file, i+1, rel, strings.Join(area.allowedSource, " "))
				}
				if _, serr := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); serr != nil {
					t.Errorf("%s:%d 来源 %s 不存在", area.file, i+1, rel)
				}
			}
		}
		if rows < area.minRows {
			t.Errorf("%s 只有 %d 条, 少于 %d: 整行被删掉了?", area.file, rows, area.minRows)
		}
		if rows > area.minRows {
			t.Logf("%s 现在 %d 条(常量 %d): 把常量调上去", area.file, rows, area.minRows)
		}
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// TestCommentStyleSpecRegistered 规范文件存在, 且以链接形式登记进了 AGENTS.md。
// 光有路径字符串不够: 规矩写在 AGENTS.md 之外而不进文档地图, 下场就是没人读得到
func TestCommentStyleSpecRegistered(t *testing.T) {
	root := filepath.Join("..", "..")
	spec, err := os.ReadFile(filepath.Join(root, "docs", "comment-style.md"))
	if err != nil {
		t.Fatalf("读 docs/comment-style.md 失败: %v", err)
	}
	if len(spec) == 0 {
		t.Fatal("docs/comment-style.md 是空的")
	}
	agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("读 AGENTS.md 失败: %v", err)
	}
	if !strings.Contains(string(agents), "](docs/comment-style.md)") {
		t.Error("AGENTS.md 的文档地图里没有指向 docs/comment-style.md 的链接")
	}
}
