# 阶段 0+1 闸门与台账的独立核验（task-6）

- 核验人：agent（verifier-gate），日期 2026-10-04。
- 核验对象：`cmd/type/comment_contract_test.go`（5 条检查）、`docs/comment-style.md`、
  `docs/comment-ledger/{typing,win32,app}.md`（共 126 条）。
- 工作方式：只读 + 实跑 `go test -count=1 ./cmd/type/`（未跑 `-race`，未跑 `scripts/build.ps1`）；
  本核验除本文件外没有改动任何仓库文件（`git status` 可证：仅 `AGENTS.md` 有阶段 0 的 1 行改动）。
- 结论的用法：**本文件是"闸门可用性"的判定，不是"注释重写"的判定。**

## 0. 结论

**不通过（闸门当前不足以守阶段 2 的重写，需最小补强后再开工）。**

理由一句话：5 条检查里 3 条有效但覆盖面有明确缺口，其中两处**已经在现仓库里存在活的反例**
（不是假想），而缺的正是规范自己排在第一、第二位的重写区域（`tools/`、`frontend/src/`）。

- B（实跑）、C（抽样）、D（遗漏）、E（与既有闸门的关系）**均通过**，问题全部集中在 A（闸门有效性）。
- 台账质量本身没问题：126 条、ID 唯一、前缀正确、抽样的 27 条事实与行号全部对得上。
  台账的弱点是**闸门只能验"行内格式"，验不了"行有没有少"**。

## 1. B 项：实跑

```
go test -count=1 ./cmd/type/
ok  	github.com/LeuJasYoh/type/cmd/type	0.610s

go test -count=1 -v -run 'TestNoBlockCommentsInGo|TestCommentBlockBudget|TestDocRefFormatFrozen|TestCommentLedgerHasDisposition|TestCommentStyleSpecRegistered' ./cmd/type/
--- PASS: TestNoBlockCommentsInGo (0.01s)
--- PASS: TestCommentBlockBudget (0.00s)
--- PASS: TestDocRefFormatFrozen (0.00s)
--- PASS: TestCommentLedgerHasDisposition (0.00s)
--- PASS: TestCommentStyleSpecRegistered (0.00s)
```

- 全绿；整套 `cmd/type` 也全绿（含 `build_contract_test.go` 的既有断言与 `contract_test.go`）。
- **没有任何 `t.Logf` 提示基线可下调**：8 个 Go 文件的实际超额块数**恰好等于**基线（合计 13，
  与 `docs/comment-style.md:91` 的"共 13 块"一致），3 条裸引用也恰好各 1 条。
  即基线是精确标定的，没有"文件已降到基线以下却没人调低"的放水迹象。
- 逐文件实测（复核用，来自只读扫描，与闸门口径一致）：
  `build_contract_test.go:3-16`、`main.go:60-78`/`164-175`、`typing.go:270-286`/`400-414`、
  `win32_clipboard.go:145-155`/`221-235`、`win32_instance.go:45-57`、`win32_keyboard.go:136-148`、
  `win32_test.go:18-28`/`630-647`、`win32_window.go:90-102`/`290-301`。
- 顺带复核规范里的一个数字：前端 `/** */` 多行块实测**正好 4 处、最长 5 行**
  （`statusBarState.ts:9-13`、`useUiScale.ts:25-26`、`viewportReport.ts:15-19`、`viewportReport.ts:31-35`），
  与 `docs/comment-style.md:64` 一致。

## 2. A 项：逐条检查的有效性判定

判定口径：这条检查能不能拦住**它自己声称**要拦的失败模式。只列"大到一个具体反例"的洞。

### A1 `TestNoBlockCommentsInGo`（comment_contract_test.go:71-90）——能拦，但只管一半的 Go 代码

- **有效**：用 `go/parser` + `parser.ParseComments` 判定，字符串字面量里的 `/*` 不会误报
  （文件注释自陈的第一版坑确实已修）；`//` 与 `/*` 混写也躲不过 parser。对 `cmd/`、`internal/` 的
  Go 文件，这条是硬的。
- **洞（大）**：`commentScanDirs`（:24-26）只有 `cmd`、`internal`、`frontend/src`。
  **`tools/` 下的 Go 文件完全不在扫描范围**，而 `docs/comment-style.md:101` 把 `tools/` 列为
  重写顺序第 1 步、`docs/comment-style.md:119` 把"不引入块注释"写成全仓红线。
  具体反例：在 `tools/equivcheck/main.go` 顶部插入 80 行 `/* … */`，本检查与 A2 都不会红
  （A2 也不扫 `tools/`），闸门 100% 静默。当前 `tools/*.go` 尚未出现块注释，所以这是"未爆的洞"。
- 次要说明：文件注释（:65-70）已解释"前端不在此列"，但没有一句解释"tools 不在此列"，
  读者会以为 Go 已被全覆盖 —— 规范的状态表也写作"Go：8 个文件共 13 块"，同样不含 `tools/`。

### A2 `TestCommentBlockBudget`（:156-183）——能拦"再加一块"，拦不住"换个形态继续长"

先回答任务点名的几种情形：

| 构造 | 结果 | 依据 |
|---|---|---|
| 往已达基线的文件**再加**一个 >10 行块 | **拦住** | `got > want` → `t.Errorf`（:171-173）。实测计数是"每文件超额块数"，加一块就 +1，逃不掉 |
| 把一个长块**拆成两个**都 ≤10 行 | 不拦 | 非 `//` 行（含空行）会重置 run（:115-118）；拆块本来就是规范允许的补救 |
| 把长块改成 `/* */` | Go（cmd/internal）拦住；**tools 不拦** | 前者由 A1 兜住；tools 不在扫描范围，两条都不管 |
| 用连续空行分隔成长块 | 不拦 | 同"拆块" |
| 把长注释**搬进 `.md`** | 不拦 | 这恰恰是规范指定的归宿（上移 docs），不算洞 |
| 在 `.ts`/`.vue` 里写长 `//` 块 | **不拦** | `longBlockComments` 只数 `/* */`（:163-164），`.ts` 分支从不调用 `longLineComments` |

- **洞 1（大，已有活反例）**：前端只数 `/* */`，`.ts`/`.vue` 的 `//` 长块无人管。
  实仓库里 `frontend/src/composables/useUiScale.ts:1-21` 就是一段 **21 行**的纯 `//` 注释块
  —— 比所有 Go 基线块都长（Go 最长 19），而闸门报的是"前端：0"。
  与 `docs/comment-style.md:59`"单块连续 `//` ≤ 10 行"（该条没有语言限定）直接冲突。
- **洞 2（大，已有活反例）**：`tools/` 不在 `commentScanDirs`，且预算表里 `tools/` 也没有 key
  → 实际数无从比较。现存超标块（只读扫描）：
  `tools/equivcheck/main.go:1-17`（17）、`tools/mkres/main.go:1-21`（21）、
  `tools/pecheck/main.go:1-21`（21）、`tools/wmcharprobe/main.go:3-28`（26）。
  重写第 1 步就是 `tools/`，闸门对它零覆盖。
- **洞 3（中）**：`frontend/test`、`frontend/tools` 不在扫描目录；扩展名只认 `.go`/`.ts`/`.vue`
  （`.mjs` 被排除，:39-43）。实仓库里 `frontend/tools/layout-probe.mjs:1-37` 是**全仓最长的
  单块注释（37 行）**，`frontend/test/typingTask.test.ts:1-12` 是 12 行 —— 两条都在台账里有
  条目（A-034/A-039/A-040），重写时却没有任何预算保护。
- **洞 4（中）**：预算单位是"超额块的**个数**"，不是注释总量。用 100 个 10 行块（或每行末尾
  接 `//` 的长尾注释）可以在 `got == want == 0` 的情况下把注释体量翻几倍。这不是绕过明文规则，
  但它使闸门对"信息密度被稀释/注释膨胀"这一半失败模式完全失明。
- **洞 5（小，逻辑级）**：`longBlockComments` 只把**行首**是 `/*` 的行当作块开始（:133）。
  `const x = 1; /* …` 起头、后续 100 行的块不会被计数。
  当前 `frontend/src` 无此类实例（已扫描确认），属未爆的洞。

### A3 `TestDocRefFormatFrozen`（:204-225）——能拦"多写一条裸引用"，拦不住"少写""写错目标"

任务问"裸引用基线会不会把'格式没写全'和'少写了一条'混为一谈"。答案：**不会混淆，因为两者它都只数个数**：

- "格式没写全"（严格引用被改成裸引用）→ `all - strict` 的 bare +1 → 超基线即红 ✓。
  **能拦。**
- "少写了一条"（删掉一条指针）→ `all` 与 `strict` 同减，bare 不变 → **永远绿**。
  `build_contract_test.go` 的 `TestDocSectionReferencesResolve` 也补不上：它只在全局 `checked == 0`
  时报错（:219-221）。删掉 `statusBarState.ts` 唯一那条指针，两条闸门都不响。
- **洞 1（大）**：**裸引用从不做"目标存在"校验**。本检查只数出现次数，从不 `os.ReadFile` 目标。
  具体反例：把 `frontend/src/components/statusBarState.ts:3` 里的
  `见 docs/invariants.md` 改成 `见 docs/does-not-exist.md` —— `all=1, strict=0, bare=1 == 基线`，
  A3 绿；`docRefRE` 要求带「」所以 resolve 也看不见它。一条指向不存在文档的指针可以静默存活。
- **洞 2（中）**：正则的字符类是 `docs/[a-z-]+\.md`（:188），不含数字、下划线、斜杠。
  `见 docs/ci-2026.md`、`见 docs/sub/x.md`、`见 docs/my_notes.md` 这类引用**两个闸门都完全看不到**
  （resolve 用的是同一个口径），既不计数也不校验。当前 `docs/` 下没有这种文件名，属未爆。
- **洞 3（中）**：只有 `见 ` 这一种措辞会被识别。写成"详见"能被前缀命中，但写成
  `参考 docs/invariants.md`、`见docs/invariants.md`、`见  docs/…`（全角/双空格）就整条逃检。
  与洞 1 叠加后，等于"指针格式自由化"这条红线实际上靠人盯。
- 有效面：文件缺 key 时报错（:214-215 反向为 Errorf 只在 A2；本条的"基线里已无裸引用"只是 `Logf`，
  :222，不红）；在同一文件里 `git mv` 之后 `seen` 检查会兜住 —— 这一条比 A2 弱（A2 对基线里的
  消失文件是 `t.Errorf`，:180）。

### A4 `TestCommentLedgerHasDisposition`（:240-280）——能拦"行内非法"，拦不住"整行消失"

先在只读侧复核台账本身：`typing.md` 43 行、`win32.md` 43 行、`app.md` 40 行，合计 **126**；
ID 无重复、无越界前缀（`T-`/`W-`/`A-` 全部合规）—— 这是人写得好，不是闸门查出来的。

任务点名的三种 Markdown 写法：

| 写法 | 结果 |
|---|---|
| 数据行 `\|ID\|...\|`（无空格） | 表头不被 `Contains(line,"\| ID \|")` 认出 → 当数据行 → `cells[3]="类别"` → **Errorf（假红）** |
| 表头用 `\| id \|` | 同上，**假红** |
| 分隔行用 `\|:---\|` / `\| --- \|` | 不匹配 `HasPrefix(line,"\|---")`（:251） → 当数据行 → `cells[3]="---"` → **假红** |

即这三种不是"绕过"，而是**合法的 Markdown 会让闸门无理由变红**（对重写期随手重排版的人不友好）。

真正的绕过与缺口：

- **洞 1（大，与检查自陈矛盾）**：注释（:238-239）声称"只保证**没有漏行**"，但代码里没有任何
  期望行数或期望 ID 集合 —— 它只校验**已存在的行**。具体反例：删掉 `typing.md:38` 的 T-029
  （规范点名"删了必然回归"的 SendPaste 那条），剩 42 行全部合法 → 测试仍然全绿。
  同理可以把整张表换成 1 行占位表（`:276` 的 `rows == 0` 只防"一行都没有"）。
- **洞 2（中）**：ID 唯一性与区域前缀都不校验。在 `typing.md` 里复制一行 T-001（重复 ID），
  或在 `typing.md` 里塞一行 `W-999`，闸门都绿。规范的 :77 明写"ID 前缀按区域"。
- **洞 3（小）**：`len(cells) < 5` 只设下限，6 列及以上的行被静默接受（多余的列不校验）。
- **洞 4（小）**：任何 `Contains(line,"| ID |")` 的**数据行**会被跳过（:251）；若某条"事实"里
  引用了表头格式，那一行就整行不校验。属构造性反例。
- 有效面：列数、类别 ∈ {A,B,C,D}、去向以四者之一开头、整份文件为空 —— 这几条是真的硬的。

### A5 `TestCommentStyleSpecRegistered`（:293-308）——弱，但符合它的自我声明

- 只做两件事：`docs/comment-style.md` 非空、`AGENTS.md` 的**任意位置**包含字符串
  `docs/comment-style.md`。它不验证这行落在「文档地图」表里，也不验证链接目标。
- 具体反例：把 `AGENTS.md:24` 那行从表里删掉、把路径挪进任何一段正文，
  或写成一个死链 `[x](docs/comment-style.md)` 指向不存在的锚点，这条都绿。
- 判定：**有洞但小**（它本来就是登记冒烟测试）。补强成本几乎为零（要求该行同时含 `](docs/comment-style.md)`）。

### A6 检查之外的共性缺口

- **台账"来源"行号从不校验**（`cells[2]` 在 :254-275 里从未被读取；写多列时也只用到 `cells[3]`/`cells[4]`）。
  所以 126 条里任一条把行号指向别处，闸门**不可能发现**。判断：这是已知缺口，
  **可以低成本补上一部分**（见第 5 节 R5）——但注意重写期行号必然漂移，只能做"文件存在 + 行号不越界 +
  路径属于本区域"，内容级比对不是低成本，且会制造假红。
- **"上移 docs/invariants.md"只校验字符串前缀**：没有任何环节确认那段事实真的落进了目标文档。
  这是设计上留给人的（:238-239 有自陈），可接受，但应在规范里写明"上移不是自动验证"。

## 3. C 项：台账抽样核对（27 条，每条都 read 了来源行号处）

结论：**抽到的 27 条事实与来源一致，行号准确，类别与去向合理；0 处事实性错误。**
只发现 2 处引用范围的小瑕疵（不影响事实）。

### typing.md（9 条）

| ID | 来源（台账写的） | 实读结果 | 判定 |
|---|---|---|---|
| T-001 | typing.go:17-20; docs/invariants.md:295-297 | 17-20 正是"五个方法均返回是否被系统接受/UIPI/锁屏"；docs:295-297 正是"注入失败必须可见" | ✓ 事实与行号精确 |
| T-002 | typing.go:35-49 | 35-39 讲 nil/空切片/Complete，45-49 是 `UnsafeToRestore` | ✓ |
| T-016 | typing.go:276-279 | 276-281 讲"判定与占领同一临界区"、300 轮/246 轮 | ✓ |
| T-018 | typing.go:297-303 | 297-303 正是取消标志前后两次读 | ✓ |
| T-019 | typing.go:316-318 | 316-318 正是"标志一律不在这里清" | ✓ |
| T-026 | typing.go:270-274; typing.go:388-391 | 270-274 讲 textDirect/文本层/Esc；388-391 是 sendEscaped 状态无关设计 | ✓（唯一措辞差异：代码写"回车/Tab"，台账写"换行"，同指一事） |
| T-029 | typing.go:440-445 | 红线三条之一，逐字对上 | ✓ 精确 |
| T-035 | typing.go:283-291 | 283-286 讲空文本当场拒绝，289-291 是代码分支 | ✓ |
| T-041 | typing_test.go:667-672 | 实测 `time.Sleep(800ms)`，注释写 800ms、CI/-race 余量理由 | ✓；且台账自曝 `docs/invariants.md:109` 仍写"晚 300ms"——**实读确认该文档确实过时** |
| T-042 | taskrun.go:1-10 | 1-10 含"256 行/9 个形参/6 个局部变量"与"40 条用例" | ✓；台账说"当前实际 47"，实测 `internal/typing/*_test.go` 的 `^func Test` 正好 **47** 个，数字准确 |

### win32.md（9 条）

| ID | 来源 | 实读结果 | 判定 |
|---|---|---|---|
| W-001 | win32.go:3-5 | 3-5 正是"Win32 清单页"文件头 | ✓ |
| W-005 | win32_window.go:275-279 | 275-277 讲 5 个参数/dpi 第 5/1540×941，278-279 是调用 | ✓ |
| W-013 | win32_window.go:321-323 | 321-322 注释 + 323 `const wsOverlappedWindow = 0x00CF0000` | ✓ |
| W-015 | win32_window.go:358,385-387,469-475 | 358 是 `GWL_STYLE = ^uintptr(15)`；469-475 是 GCLP_HICON/HICONSM；派生值 ^15=-16、^13=-14、^33=-34、^0=-1、^1=-2 全部自洽 | ✓ |
| W-018 | win32_window.go:525-546; win32_keyboard.go:136-152 | 525-530 注释 + 531-546 `focusedHWND` | ✓ |
| W-024 | win32_clipboard.go:75-84 | 75-84 正是 `encodedText`（UTF-16LE + 结尾 NUL） | ✓ |
| W-028 | win32_keyboard.go:45-52 | 45-46 注释 + 47-52 手工 40 字节 `input` | ✓ |
| W-033 | win32_instance.go:16-22 | 17-20 注释（不带 PID、不用 Global）+ 21 `Local\Type-…` | ✓ |
| W-036 | win32_instance.go:75-78 | 红线三条之一，逐字对上（LazyProc.Call 的 err vs 被清零的 GetLastError） | ✓ 精确 |

### app.md（8 条）

| ID | 来源 | 实读结果 | 判定 |
|---|---|---|---|
| A-001 | main.go:60-75 | 60-78 注释块讲 WM_DPICHANGED/HintFixed/SetSize 顺序 | ✓ 事实精确；**引用只写到 75**，76-78 的 lw/lh 由 A-008（77-79）覆盖，不算漏 |
| A-011 | contract_test.go:30-33 | 30-32 注释 + 33 `reportViewportRE` | ✓ 精确 |
| A-016 | contract_test.go:363-393; index.html:7-11; useTheme.ts:24-27 | index.html:7-10 注释 + 11 内联脚本；useTheme.ts:24-27"读判定结果不重算" | ✓ 精确 |
| A-017 | contract_test.go:397-417; useUiScale.ts:25-27 | 397-417 是 `TestUiScaleBaseMatchesWindowFloor`；useUiScale.ts:27 `BASE_WIDTH = 540` | ✓ |
| A-027 | statusBarState.ts:9-16 | 9-13 jsdoc（空串刻意保留）+ 14-16 函数 | ✓ 精确 |
| A-034 | typingTask.test.ts:1-12 | 1-12 文件头，四条既有行为 + 两条防回归 | ✓ 精确 |
| A-037 | equivcheck/main.go:73-76,198-200,228-234 | 73-76"边界：只比函数体 token"；228-234"旧侧单文件 + 退出" | ✓ |
| A-039 | layout-probe.mjs:24-37 | 24-37 三个坑（事件循环/外框尺寸/screenshot 视口） | ✓ 精确 |

（`env.d.ts` 是单行文件，扫描脚本对它报了类型告警，与台账无关；已用 `@()` 包装后重跑确认。）

## 4. D 项：遗漏抽查

### 4.1 规范点名的"三条删了必回归"（comment-style.md:114-117）

| 红线 | 台账条目 | 引用 | 判定 |
|---|---|---|---|
| main.go 窗口尺寸与样式段（WM_DPICHANGED/HintFixed/SetSize 顺序） | A-001（app.md:9） | main.go:60-75 | ✓ 存在且事实完整 |
| typing.go"SendPaste 返回后立刻复检" | T-029（typing.md:38） | typing.go:440-445 | ✓ 存在，且标注为红线 |
| win32_instance.go"事后 GetLastError 已被运行时清零" | W-036（win32.md:45） | win32_instance.go:75-78 | ✓ 存在，且标注为红线 |

**三条全在，引用精确。**

### 4.2 三个最长注释块（任务点名的 main.go ≈:60、typing.go ≈:270/:400）

实测最长的三个 `//` 块（`run > 10`）：

| 块 | 台账覆盖 | 判定 |
|---|---|---|
| main.go:60-78（19 行） | A-001（60-75）+ A-008（77-79） | ✓ 全覆盖（76 行是空 `//` 分隔） |
| typing.go:270-286（17 行） | T-026（270-274）+ T-016（276-279）+ T-035（283-291） | ✓ 三段各自有主 |
| typing.go:400-414（15 行） | T-028（410-414） | **部分覆盖**：400-409（`typeTextViaClipboard` 的返回值契约、"三种失败必须分开报"、`clipboardOK` 采信条件）没有任何条目引用该范围 |

- 说明：400-409 的**实质内容**在 T-008（失败文案不许合并，typing.go:118-142）、T-031、T-032 里有
  同义条目，所以不算"事实丢失"；缺的是这一处的**引用锚点**（将来重写 `typeTextViaClipboard`
  时，"先读哪条"指不到 T-008）。**判为低危瑕疵，不是遗漏。**

## 5. E 项：与既有闸门的关系

- `git status --short`：`M AGENTS.md`、`?? cmd/type/comment_contract_test.go`、`?? docs/comment-ledger/`、
  `?? docs/comment-style.md`。`cmd/type/build_contract_test.go` **未被改动**（不在 diff 里），
  既有断言（版本号四处逐字同款、发布命令三处同款、BOM、文档指向可解析）逐字未动。
- 实跑 `go test -count=1 ./cmd/type/` 全绿 —— 新测试与 `build_contract_test.go` 的
  `TestDocSectionReferencesResolve` 同处一个包、同一次运行，没有互相触发（新文件里没有一处
  真正匹配 `见 docs/<名字>.md「节」` 的文本；`docs/某文档.md` 因 `[a-z-]+` 不匹配中文而安全）。
- `AGENTS.md` 的改动只有 1 行（`@@ -21,6 +21,7 @@`）：在「文档地图」表里新增
  `| 写代码注释 / 注释重写 | [docs/comment-style.md](docs/comment-style.md)（三种形态 + 归置判据 + 重写台账与闸门基线） |`。
  位置正确（表格内）、链接与 A5 的断言语义一致，**没有写坏文档地图约定**。
- 两个新测试文件用了同一个构建标签 `//go:build windows && (amd64 || arm64)`，口径一致。
- 唯一遗留（非本次引入）：`docs/invariants.md:109` 的"晚 300ms"与代码的 800ms 不一致 —— 台账
  T-041 已如实记录但**尚未订正**；`taskrun.go:9` 的"40 条用例"也仍过时（实际 47，T-042 已记录）。
  这两处是"上移阶段"的待办，不影响本阶段的判定。

## 6. 未通过项清单

| # | 严重度 | 问题 | 证据 |
|---|---|---|---|
| F1 | 高 | `tools/` 不在 `commentScanDirs`：A1 的"Go 零块注释"与 A2 的长度预算都不覆盖 `tools/*.go`，而 `tools/` 是规范重写顺序第 1 步 | `comment_contract_test.go:24-26`；`docs/comment-style.md:101,119`；活反例 `tools/{equivcheck:1-17,mkres:1-21,pecheck:1-21,wmcharprobe:3-28}` |
| F2 | 高 | 前端只数 `/* */`，`.ts`/`.vue` 的 `//` 长块不计；实仓库已有 21 行反例，闸门仍报"前端 0" | `comment_contract_test.go:163-164`；`frontend/src/composables/useUiScale.ts:1-21`；`docs/comment-style.md:59,91` |
| F3 | 高 | A4 自陈"保证没有漏行"，但无期望行数/ID 集合，删掉整行仍绿 | `comment_contract_test.go:238-239,248-278`；反例：删 `typing.md:38`(T-029) 仍 42 行合法 |
| F4 | 中 | 裸引用从不校验目标文档存在，改成 `见 docs/does-not-exist.md` 仍绿（resolve 因无「」也看不见） | `comment_contract_test.go:204-225` 只计数；`build_contract_test.go:163,204` |
| F5 | 中 | `frontend/test`、`frontend/tools` 不在扫描目录且 `.mjs` 不在扩展名列表：全仓最长块（37 行）无保护 | `comment_contract_test.go:24-26,39-43`；`frontend/tools/layout-probe.mjs:1-37`、`frontend/test/typingTask.test.ts:1-12` |
| F6 | 中 | `来源` 列（行号）完全未校验（`cells[2]` 从未被读） | `comment_contract_test.go:254-275` |
| F7 | 中 | 指针正则字符类 `[a-z-]+` 排除数字/下划线/斜杠，这类引用两个闸门都失明 | `comment_contract_test.go:188`；`build_contract_test.go:163` |
| F8 | 低 | 台账 ID 唯一性与区域前缀不校验；6 列以上静默接受；数据行含 `\| ID \|` 会被跳过 | `comment_contract_test.go:251,255-258` |
| F9 | 低 | 合法 Markdown 变体（`\|ID\|`、`\| id \|`、`\|:---\|`）会让 A4 **假红** | `comment_contract_test.go:251` |
| F10 | 低 | A5 只查"字符串出现在 AGENTS.md 任意位置"，不查它是否在图里 | `comment_contract_test.go:302-308` |
| F11 | 低 | D 项：`typing.go:400-409` 无台账引用锚点（事实由 T-008/T-031/T-032 覆盖） | `typing.md:37` 只引 410-414/426-438 |

## 7. 建议的最小补强（按性价比排序，均不改规范语义）

- **R1（对应 F1，约 2 行 + 4 条基线）**：`commentScanDirs` 增加 `{"tools"}`，
  并给现有超标块补基线：`tools/equivcheck/main.go:1`、`tools/mkres/main.go:1`、
  `tools/pecheck/main.go:1`、`tools/wmcharprobe/main.go:1`（数字来自本次只读实测）。
  顺带给 A1 的文件注释补一句"`tools/` 是否在扫描范围内"。
- **R2（对应 F2/F5，约 5 行 + 基线）**：`.ts`/`.vue` 分支改为 `max(longBlockComments, longLineComments)`，
  扩展名补 `.mjs`，目录补 `frontend/test`、`frontend/tools`；
  基线至少补 `frontend/src/composables/useUiScale.ts:1`、`frontend/test/typingTask.test.ts:1`、
  `frontend/tools/layout-probe.mjs:1`。规范 :91 的"前端：0"要同步改成真实数。
- **R3（对应 F3/F8，约 10 行）**：台账检查加"每份文件的行数下限"（43/43/40，可写成与规范同源的常量）
  并对 ID 做 `^[TWA]-\d{3}$` + 唯一性 + 区域前缀校验。这一步直接补上"漏行"这条自陈能力。
- **R4（对应 F4/F7，约 10 行）**：裸引用也走一次"目标文件存在"；正则字符类放宽到
  `[A-Za-z0-9._/-]+`，并把 `见\s+` 归一（顺带容忍全角空格）。成本低、直接堵住"指针指向不存在文档"。
- **R5（对应 F6，判断：可低成本补一部分）**：`来源` 列拆 `;` 后校验每段匹配
  `^([A-Za-z0-9_./-]+)(:\d+(-\d+)?)?$`，再做两件事 —— ① 文件存在；
  ② 每条路径落在本区域的目录前缀内（`typing.md`→`internal/typing/`+`docs/`，`win32.md`→`internal/win32/`+`docs/`，
  `app.md`→`cmd/`+`frontend/`+`tools/`+`docs/`）。
  **不要**做"行号 ≤ 文件长度"以上的校验：重写期行号必然漂移，内容比对会制造假红；
  事实语义对不对仍只能人查（这一点台账与规范已经写明）。
- **R6（对应 F10，1 行）**：A5 的断言改成要求出现 `](docs/comment-style.md)` 且该行含 `|`。
- **R7（顺手）**：`docs/invariants.md:109` 的 300ms 订正为 800ms；`taskrun.go:9` 的"40 条用例"
  随重写删除（T-041/T-042 已登记，属上移阶段待办）。

## 8. 判定为"可接受、不列为未通过"的缺口（记录在案）

- 检查 2 只限"单块 ≤10 行"、不设注释总量上限：拆块是规范认可的补救，膨胀问题只能靠人评审。
  （若要自动化，需要按文件统计注释字数/行数总量，属新规则，不在阶段 0 的范围。）
- `// ─── 小节 ───` 导航线保留、文件头 ≤5 行这两条规范要求没有对应闸门
  （导航线天然是 1 行，文件头长度未设检查）—— 规范未声明有闸门，不算缺失。
- "上移 docs/invariants.md"的落地确认、冻结文案逐字不变，分别由既有台账人工销账与
  `contract_test.go`/`TestDocSectionReferencesResolve` 承担；本次未发现新缺口。

---

# 第 2 轮：补强后的闸门（变异实测，task-7）

- 核验人：agent（verifier-gate），日期 2026-10-04。
- 对象：补强后的 `cmd/type/comment_contract_test.go`（392 行）与同步后的 `docs/comment-style.md`（127 行）。
- 结论：**通过（有条件）**。6 条必做变异**全部被拦住**，无一漏检；F1–F5、F7 已封，
  F6/F8/F10 部分封（残余具体且部分为规范明示的取舍），F9 未封（合法 Markdown 假红）。
  另发现规范基线表一处数字错误（"19 块"应为 **20 块**）。

## R2-1 变异纪律的执行记录

- 备份目录：`%TEMP%\gate-mut-verify`（仓库外），每轮变异前 `Copy-Item` 原文件 + 记 SHA256。
- 每轮流程：备份 → 记哈希 → 变异 → 跑 `go test -count=1 ./cmd/type/` → 立刻从副本还原 →
  核对还原哈希 == 变异前哈希 **且** `git status --short`（含未跟踪项）与基线逐字相同。
- 8 次变异（M1–M8）**逐次都满足**：还原哈希相等 = True，git status 逐字相等 = True。
- 最终全局复核：5 个被变异文件的 SHA256 全部等于基线值，`git status --short` 与基线逐字相同，
  `git diff --stat` 只有阶段 0 原有的 `AGENTS.md | 1 +`，全包 `ok`。
  **仓库没有留下变异痕迹。**

## R2-2 M1–M6 结果（必做）

| 变异 | 期望 | 实测 | 触发的测试 | 报错摘要 | 还原 |
|---|---|---|---|---|---|
| M1 往 `frontend/src/types.ts`（基线 0）追加 12 行 `//` | 红 | **拦住** | `TestCommentBlockBudget` | `comment_contract_test.go:185: frontend/src/types.ts 有 1 个超过 10 行的注释块, 基线 0` | ✓ |
| M2 往 `tools/pecheck/main.go` 插 3 行 `/* */` | 红 | **拦住** | `TestNoBlockCommentsInGo` | `comment_contract_test.go:92: tools/pecheck/main.go:24 出现块注释` | ✓ |
| M3 删 `typing.md` 的 T-029 整行 | 红 | **拦住** | `TestCommentLedgerHasDisposition` | `comment_contract_test.go:348: typing.md 只有 42 条, 少于 43: 整行被删掉了?` | ✓ |
| M4 `statusBarState.ts` 裸引用改成 `见 docs/does-not-exist.md` | 红 | **拦住** | `TestDocRefFormatFrozen` | `comment_contract_test.go:227: ... 指向 docs/does-not-exist.md, 但该文件不存在` | ✓ |
| M5 `app.md` 某来源改成 `scripts/build.ps1:1` | 红 | **拦住** | `TestCommentLedgerHasDisposition` | `comment_contract_test.go:340: app.md:9 来源 scripts/build.ps1 不属于本区域(cmd/ frontend/ tools/ docs/)` | ✓ |
| M6 复制 `app.md` 的 A-001 整行 | 红 | **拦住** | `TestCommentLedgerHasDisposition` | `:316: app.md:10 ID "A-001" 与 app.md 里的重复`（另 `:351` Logf 41 条） | ✓ |

- 每条变异的 `mutated-hash` 都与 `backup-hash` 不同（证明变异真的落盘），测试输出都含 `--- FAIL`。
- M4 额外确认 `见 docs/invariants.md` 在该文件里**恰好出现 1 次**，替换后裸引用条数不变 ——
  即这次红的唯一原因就是"目标不存在"，正是要证明的那条。

## R2-3 两条探针（任务没要求，用来量化残余）

| 探针 | 构造 | 期望 | 实测 | 含义 |
|---|---|---|---|---|
| M7 | `app.md` 来源改成 `cmd/type/main.go:99999`（越界行号） | 不拦 | **全绿** | F6 残余：行号仍不校验。属规范 :104 明示的取舍 |
| M8 | `typing.md` 分隔行改成 `\|:---\|`（合法 Markdown 对齐符） | — | **红，5 条假错** | F9 未封：`:310` ID ":---" 形状非法、`:322` 类别 "---"、`:326` 去向 "---"、`:340` 来源 "---" 不属本区域、`:343` 来源 "---" 不存在 |

## R2-4 A 项：规范基线表 vs 闸门常量

- **文件清单**：规范 :95 写"15 个文件"；闸门 `longCommentBudget`（:103-119）正好 15 个 key，
  逐个名称一致 ✓。
- **每文件块数**：规范 :95 的逐项（`build_contract_test.go` 1、`main.go` 2、`typing.go` 2、
  `win32_clipboard.go` 2、`win32_instance.go` 1、`win32_keyboard.go` 1、`win32_test.go` 2、
  `win32_window.go` 2；`tools/` 4 个各 1；前端 3 个各 1）与 map 值**逐项相同** ✓。
- **合计不一致（未通过项 N1）**：规范写"15 个文件共 19 块超额"，但同一个单元格里自己的分解
  是"13（Go）+ 4（tools）+ 3（前端）= **20**"；闸门 map 求和实测也是 **20**；
  且 `-v` 跑闸门**没有任何一条"可以把基线调下来"的 Logf**（等价于每个文件 `got == want`），
  所以现仓库实测合计就是 **20**。规范里的"19"是笔误，应改成 **20**。
- **台账条数**：闸门 `minRows` 43/43/40（:262-264）与规范 :97 的"typing 43 / win32 43 / app 40"
  一致 ✓（M3 证明下限真的生效）。
- **裸引用**：`bareDocRefBudget` 3 个 key 各 1（:207-211）与规范 :97 一致；无 Logf → 实测各 1 ✓。
- "Go 零块注释 0 处"（规范 :94）✓，且现在含 `tools/`（M2 证明）。

## R2-5 B 项：规范"已知不设闸门的三件事"与代码实际一致

| 规范自陈（:103-105） | 代码实际 | 判定 |
|---|---|---|
| 注释总量没有上限（只管单块长度） | `TestCommentBlockBudget` 只数 `run > 10` 的块数，无总量/字数上限 | 一致 |
| 台账来源的行号内容不比对（只查文件存在与区域前缀） | `:333-344` 只做 `ledgerSourceRE` 形状 + `hasAnyPrefix` + `os.Stat`，从不读行号内容 | 一致，且 M7 实测坐实 |
| "上移 docs"是否真落进目标文档无验证 | 代码从不读取 `docs/invariants.md` 等目标文档的正文来核对迁移 | 一致 |

三条都准确，没有把"没设闸门"说成"已设闸门"。

## R2-6 C 项：第一轮 F1–F10 逐条状态

| # | 第 1 轮问题 | 状态 | 证据 / 残余 |
|---|---|---|---|
| F1 | `tools/` 不扫 | **已封** | `commentScanDirs` 含 `tools`（:29）；4 条 tools 预算与实测一致；M2 红 |
| F2 | 前端 `//` 长块不计 | **已封** | 非 `.go` 分支改为 `longLineComments + longBlockComments`（:179）；M1 红 |
| F3 | 台账漏行不可检 | **已封** | `minRows` 43/43/40（:262-264，:347）；M3 红 |
| F4 | 裸引用不查目标存在 | **已封** | 每条 `docRefAnyRE` 命中都 `os.Stat`（:225-229）；M4 红 |
| F5 | `frontend/test`、`frontend/tools`、`.mjs` 不扫 | **已封** | 目录与 `commentScanExts` 补全（:28-34）；`typingTask.test.ts`、`layout-probe.mjs` 各 1 条预算且无 Logf → 实测各 1 |
| F6 | 来源行号完全不校验 | **部分封** | 新增"形状 + 文件存在 + 区域前缀"（:328-345，M5 红）；**残余**：越界行号仍绿（M7），同区域内张冠李戴仍绿。规范 :104 已明示此取舍，可接受 |
| F7 | 指针正则字符类过窄 | **已封** | `见[ \t\x{3000}]+(docs/[A-Za-z0-9._/-]+\.md)`（:202-203），数字/下划线/子目录名可识别 |
| F8 | ID 唯一性/前缀、列数、行跳过 | **部分封** | ID 形状 + 前缀 + 唯一性已加（:309-319，M6 红）；**残余**：6 列以上仍静默接受（`len(cells) < 5` 只设下限），数据行里含"竖线 ID 竖线"时整行仍被跳过（:299） |
| F9 | 合法 Markdown 假红 | **未封** | 仍是 `HasPrefix(line,"\|---")` + `Contains(line,"\| ID \|")`（:299）；M8 用 `\|:---\|` 触发 5 条假错。属脆性而非漏检，但重写期重排版会误伤 |
| F10 | A5 只查子串 | **部分封** | 改查 `](docs/comment-style.md)`（:389）；**残余**：仍不要求它出现在「文档地图」表内，写在 AGENTS.md 任意一段的 markdown 链接都算过 |

第 1 轮 A 节里没进 F 表、本轮**仍未封**的两条残余（如实记录）：

- **只认 `见` 这一种措辞**：`参考 docs/invariants.md`、`见docs/x.md`（`见` 后无空白）两个闸门都
  完全看不见（`docRefAnyRE` 要求 `见` + 至少一个空白）。本轮正则只做了空白归一与字符类放宽，
  没有解决措辞问题。
- **`longBlockComments` 只认行首 `/*`**：`.ts`/`.mjs` 里 `code; /*` 起头再跨 50 行的块不计入预算
  （第 1 轮 A2 洞 5）。补强后的代码注释 :141 已把这条限制写明（"行中起头的那种……暂不计"），
  即从"未意识到的洞"变成"已知取舍"。

## R2-7 第 2 轮未通过项清单

| # | 严重度 | 问题 | 证据 |
|---|---|---|---|
| N1 | 低（文档错） | 规范 :95"15 个文件共 **19** 块超额"与同格的分解（13+4+3）及闸门 map 求和（20）、实测（20）都不符 | `docs/comment-style.md:95`；`comment_contract_test.go:103-119`；无 Logf 的 `-v` 输出 |
| N2 | 低（脆性） | F9 未封：合法 `:---` 对齐符触发 5 条假错 | M8：`comment_contract_test.go:310,322,326,340,343` |
| N3 | 低（残余） | F6 越界行号、F8 超列/行跳过、F10 非表内链接三处残余未收紧 | M7 全绿；`comment_contract_test.go:299,303,389` |

## R2-8 判定

- **M1–M6 六条必做变异全部被对应测试拦住，0 漏检**；每条的报错信息都指到了正确的文件与原因
  （M1 指到 `types.ts`、M2 指到 `tools/pecheck/main.go`、M3 报条数下限、M4 报目标不存在、
  M5 报区域前缀、M6 报 ID 重复），说明补强不是"碰巧整体变红"，而是每条检查各自生效。
- 变异纪律 8/8 次通过：还原哈希与 `git status` 全程与基线逐字一致，仓库无脏痕迹。
- **通过（有条件）**：修 N1（19 → 20）；建议顺手把 F9 的 `|:---` 归一（识别"只由 `-`/`:`/空白组成
  的分隔行"即可，约 2 行），并把 F8 的两条残余补进规范"已知不设闸门的三件事"。
- 阶段 2 重写可以开工：闸门对"防长块、防块注释、防假指针、防漏行"这四条主目标已具备实测有效的拦截力；
  F6/F8/F10 的残余都不影响这四条。

---

# 第 3 轮：N1–N3 的定向复核（task-8）

- 核验人：agent（verifier-gate），日期 2026-10-04。对象：`cmd/type/comment_contract_test.go`（409 行）、
  `docs/comment-style.md`（129 行）的第三轮改动。
- 结论：**通过**。N1（规范 19→20）、N2（F9 假红）、N3 的两条残余都按预期修好/写清，
  4 条必做探针 + 1 条附加探针全部符合预期，**未发现新引入的拦截缺口**；只发现一个与本轮改动
  无关的小文档缺陷（见 R3-4 的 O1）。
- 纪律沿用第 2 轮：备份在仓库外（`%TEMP%\gate-mut-verify3`），逐次核对还原 SHA256 与
  `git status --short` 逐字一致。7 次变异**全部** `restore-hash-equal=True`；
  最终 4 个相关文件哈希全部等于基线、`git status` 与基线逐字相同、全包 `ok`、仓库无脏痕迹。

## R3-1 P1–P4 结果

| 探针 | 构造 | 期望 | 实测 | 证据 |
|---|---|---|---|---|
| P1a | `typing.md:9` 分隔行 → `\|:---\|---\|---\|---\|`（5 格对齐式） | 绿 | **GREEN** | 第 2 轮同一行是 5 条假错，现在 `TestCommentLedgerHasDisposition` 全过 |
| P1b | `typing.md:9` 分隔行 → 字面 `\|:---\|`（1 格） | 绿 | **GREEN** | `isLedgerSeparator` 对两种写法都返回 true（:283-290） |
| P2 | `app.md:8` 表头 → `\| id \| 事实 \| 来源 \| 类别 \| 去向 \|` | 绿 | **GREEN** | 表头按首格 `EqualFold(cells[0],"ID")` 判定（:317） |
| P3 | `app.md:10`（A-002）行尾加一格 → 6 列 | 红 | **RED** | `:321 app.md:10 有 6 列, 要 5 列`（另 `:365` 因该行不再计数而报 39 条，符合预期） |
| P4a | 删 `typing.md` 的 T-029 整行 | 红 | **RED** | `:365 typing.md 只有 42 条, 少于 43` |
| P4b | `app.md` 来源 → `scripts/build.ps1:1` | 红 | **RED** | `:357 app.md:9 来源 scripts/build.ps1 不属于本区域(cmd/ frontend/ tools/ docs/)` |
| P5（附加） | `app.md` 来源 → `cmd/type/main.go:99999` | 绿 | **GREEN** | 越界行号仍不校验，与规范 :104-105 自陈一致 |

- 每条探针都打印了替换命中数（`occ`）再判定。**P1 第一次跑在 `app.md` 上 `occ=0`、变异没落盘、
  结果无效**，已改到 `typing.md` 重跑（原因见 R3-4 O1）；表中 P1a/P1b 是 `occ=1` 的有效结果。
- P3 的连锁反应（`:365` 报 39 条）是对的：6 列行被 `continue` 跳过后不计入 `rows`，
  两条错误同时出现正说明"列数"与"条数下限"两道都在生效。

## R3-2 A 项：规范基线表与闸门常量一致

- `docs/comment-style.md:95` 现在写"15 个文件共 **20** 块超额"，同格分解"Go 13 + tools 4 + 前端 3"
  合计 20，闸门 `longCommentBudget` 15 个 key 求和实测也是 **20** —— 三处一致，N1 已修好。
- 其余项复核无变化：`minRows` 43/43/40 与 :97 一致；3 条裸引用与 :97 一致。

## R3-3 B 项：规范"已知不设闸门"清单与代码逐条一致

规范 :103-107 现列 5 条，逐条对照代码实际行为：

| 规范自陈 | 代码实际 | 一致 |
|---|---|---|
| 注释总量没有上限（只管单块长度） | `TestCommentBlockBudget` 仅数超 10 行的块数，无总量上限 | ✓ |
| 行中起头的 `/* */` 块（`code; /* …`）不计入预算 | `longBlockComments` 只在 `HasPrefix(TrimSpace(line),"/*")` 时进入块（:148） | ✓ |
| 台账来源行号内容不比对（越界、张冠李戴查不出） | 只做 `ledgerSourceRE` 形状 + 区域前缀 + `os.Stat`（:345-361）；P5 实测越界行号全绿 | ✓ |
| "上移 docs"是否真落进目标文档无验证 | 代码不读目标文档正文 | ✓ |
| AGENTS.md 登记只认"链接出现在文件任意位置"，不要求在表内 | `TestCommentStyleSpecRegistered` 只做 `Contains("](docs/comment-style.md)")`（:406） | ✓ |

5 条全部与代码一致：**没有把"没设闸门"写成"已设闸门"，也没有漏写已存在的限制。**

## R3-4 复核发现（不改本轮结论）

| # | 严重度 | 发现 | 证据 |
|---|---|---|---|
| O1 | 低（既有文档缺陷，与本轮改动无关） | **`docs/comment-ledger/app.md` 没有 Markdown 分隔行**：:8 是表头、:9 直接是 A-001；`typing.md`、`win32.md` 的 :9 都有 `\|---\|---\|---\|---\|---\|`。严格 GFM 下 `app.md` 不会渲染成表格（表头与 40 行数据会退化成段落）。闸门对两种形态都容忍，所以不影响拦截力 —— 这也是 P1 最早在 `app.md` 上 `occ=0` 的原因 | `app.md:8-9`；`typing.md:9`；`win32.md:9` |
| O2 | 低（新逻辑的理论边角） | `isLedgerSeparator` 会对"每个单元格都只由 `-`/`:`/空白组成"的行返回 true 并整行跳过。对 T-/W-/A- 开头的台账行不可能触发（首格必须含字母数字），但若将来 ID 规则放宽需重新评估 | `comment_contract_test.go:283-290` |

未发现第三轮改动引入新的拦截缺口：P3/P4a/P4b 与第 2 轮同类变异结果完全一致（没有因为放宽
分隔行/表头识别而变松），P1/P2 按预期由红转绿。

## R3-5 第 3 轮判定

- **通过**：N1 已修（19 → 20，三处一致）；N2 已消（F9 的 `|:---|` 假红不再出现，两种合法写法都绿）；
  N3 的两条残余已按实测写进规范"已知不设闸门"清单，且第 5 条（A5 不要求在表内）与代码一致。
- 未削弱的证明：P3（6 列）、P4a（漏行）、P4b（区域前缀）全部照旧变红，三道关键拦截力完好。
- 建议（都不阻塞阶段 2）：给 `app.md` 补一行 `|---|---|---|---|---|`（O1，顺带让它在 GFM 下真渲染成表）；
  其余两处残余（越界行号、A5 表内位置）保持现状即可，它们已在规范里如实声明。
