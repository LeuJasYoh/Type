# 阶段 2 第 1 批核验：tools/ 四个文件的注释重写（task-12）

- 核验人：agent（verifier-gate），日期 2026-10-04。
- 核验对象：`tools/equivcheck/main.go`、`tools/mkres/main.go`、`tools/pecheck/main.go`、
  `tools/wmcharprobe/main.go` 相对 `HEAD` 的改动，以及 `docs/comment-ledger/progress.md` 的销账记录。
- 写权限：本文件。另在仓库外 `%TEMP%\batch1-verify\` 放了一次性分析器与 HEAD 副本
  （不触碰仓库；变异用的备份同样在仓库外，见第 7 节）。

## 0. 结论

**通过（有 1 条信息无下落，必须补）。**

- 「只动注释」：**四个文件全部成立**，用与改写者不同的方法（go/scanner token 流）独立证明，
  0 差异；仅 `wmcharprobe` 有两处行尾注释变动，正是 `progress.md` 已声明的那两处。
- 「信息零丢失」：逐条追了 4 个文件删/改的 **48 条说法**（equivcheck 15 + mkres 9 + pecheck 9 +
  wmcharprobe 15），**47 条有明确下落**（仍在文件内 / 已进 docs 小节 / 台账或 progress.md 声明过），
  **1 条无下落**（E2，见第 2.1 节）。其中 wmcharprobe 的 `case "direct"` 那条是**已声明的纠正性改写**
  （旧注释与实现不符），别的说法都是压缩、加指针或搬到别处，没有语义删除。
- 闸门收紧是真的：四个 tools 文件基线已置 0（`comment_contract_test.go:112-115`），
  变异实测被 `TestCommentBlockBudget` 拦住，还原后仓库与基线逐字一致。
- `app.md` 确实**没有** mkres / pecheck 条目（台账缺口成立，`progress.md:39-41` 自己也认了）。

## 1. 「只动注释」的独立验证（方法一：词法流）

改写者用的是「`git diff -U0` 过滤 + 剥整行 `//` 与空行后逐行比对」。我换一种**不看行结构、只看词法结构**
的方法：

- 用 `go/scanner`（`ScanComments`）分别扫 `HEAD` 版与工作区版，**丢掉全部 COMMENT token**，
  逐 token 比对「token 类型 + 字面量原文」。字面量原文参与比对，所以字符串、数字、常量、
  标识符的任何改动都会暴露（不只注释）。
- HEAD 版用 `git show HEAD:<路径> > <仓库外文件>` 取出，并核对**字节数 == `git cat-file -s`**，
  证明取出的副本与 git 对象逐字节相同，不是被 PowerShell 转码过的文本。

| 文件 | HEAD bytes / tokens | 工作区 bytes / tokens | token 差异 | 结论 |
|---|---|---|---|---|
| `tools/equivcheck/main.go` | 11815 / 2109 | 10825 / 2109 | 0 | **TOKENS-IDENTICAL** |
| `tools/mkres/main.go` | 6392 / 826 | 5885 / 826 | 0 | **TOKENS-IDENTICAL** |
| `tools/pecheck/main.go` | 7241 / 1114 | 6479 / 1114 | 0 | **TOKENS-IDENTICAL** |
| `tools/wmcharprobe/main.go` | 14249 / 2438 | 14285 / 2438 | 0 | **TOKENS-IDENTICAL** |

四个文件都是 `scanerr=0`（源码都能正常词法扫描），token 数与序列完全一致 → **代码零改动**。

**方法边界（我自己的洞，已单独补上）**：token 流把注释全丢了，所以**构建约束行也看不见**。
单独比对了 `^//` 开头的 `go:`/`+build` 行：只有 `wmcharprobe:1` 有 `//go:build windows`，
新旧一致；另外三个文件没有构建约束。这条不覆盖的话，"换掉 build tag"会被我的方法漏掉。

**方法二（交叉印证）：逐 hunk 行分类**（`git diff -U0`，把每个增删行的 trimmed 首字符分类）：

| 文件 | 删除行 | 其中含代码的行 | 新增行 | 其中含代码的行 |
|---|---|---|---|---|
| `tools/equivcheck/main.go` | 32 | 0 | 15 | 0 |
| `tools/mkres/main.go` | 26 | 0 | 19 | 0 |
| `tools/pecheck/main.go` | 17 | 0 | 5 | 0 |
| `tools/wmcharprobe/main.go` | 39 | **2** | 34 | **2** |

`wmcharprobe` 那 2 行就是下面第 3 节验真的两处行尾注释；token 流已证明这两行的**代码部分逐 token 未变**。
除这两行外，四个文件的增删行全部是纯注释行。

**`progress.md` 数字复核**：注释行数与"剥注释+空行后的行数"我独立数了一遍 ——
equivcheck 51→34 / 296 行、mkres 42→35 / 125 行、pecheck 34→22 / 159 行，**与 `progress.md:10-12` 完全一致**；
wmcharprobe 55→47 / 339 行中，有 337 行逐行全等、恰好 2 行是它声明的那两处行尾注释
（`progress.md:13` 写"339 行有效代码全等；仅两处行尾注释变动"，措辞略松但结论不假）。

## 2. 逐文件信息下落清单（重点）

判定四处：**仍在文件里**（给行号）/ **已进 docs 小节**（read 确认）/ **台账或 progress.md 声明过** / **无下落**。
`docs/architecture.md:70-81`（小节「布局与单一来源」）与 `docs/invariants.md:365-373`（新增不变量）
是本批两个主要去处，均已 read 确认。

### 2.1 `tools/equivcheck/main.go`

| # | HEAD 的说法 | 下落 |
|---|---|---|
| E1 | 用 `go/parser` 取顶层函数做逐函数比对 | 文件 `:1-3`（职责）+ 代码实现 |
| **E2** | **"旧实现用正则扫行 + 找行首 `}` 收尾, 遇到函数内的行首大括号就提前截断, 历史 rev 上只能抽出 2 个函数, 结论不可用"** | **无下落** —— 全仓 grep `正则扫行\|提前截断\|2 个函数\|两个函数` = 0 命中；新不变量（`invariants.md:365-373`）不含；`progress.md` 未声明；`app.md` A-037/A-038 也不含 |
| E3 | 函数体按 token 序列比对（字面量保留原文） | 文件 `normalize` 注释 + `invariants.md:365-369` |
| E4 | 纯搬移一致 / 机械变换列差异清单 | 文件 `:1-3` |
| E5 | `--old-file` / `--renames` / `--renamed` 用法 | 文件 `:5-6`、usage 常量 `:173`、代码分支 `:188-204` |
| E6 | `--old-file` 默认 `main.go`；**结构整理前的历史 rev 用根路径** | 文件 `:181-182`（改写后更准确：写 `"main.go"` 会让默认用法死在 `git show` 上）+ `architecture.md:80-81` |
| E7 | `--renames` 默认与本工具同目录的 `renames.json` | 文件 `:6` + `defaultRenamesPath` 注释 `:329-330`（用 `runtime.Caller(0)`，确实与本工具同目录） |
| E8 | `--renamed` 启用改名映射（接口化之后的提交） | 代码 `:188`、`:268-269` 的警告文案 → 可推导 |
| E9 | 新侧清单不写死，用 `git ls-tree` 现取（跳过 `_test.go` 与 `tools/`） | `invariants.md:370-373` + 文件 `gitGoFiles` 注释 `:38` |
| E10 | 函数体不含签名，方法化不产生差异 | 文件 `extract` 注释 |
| E11 | 边界：签名/参数/常量/字段/tag 在视野外；改时序常量值照样"一致"；要靠字面量断言 | `invariants.md:365-371` + 文件 `:8` 指针 |
| E12 | `funcKey` 用 `Receiver.名字`；**旧实现只保留最后一个** | 文件 `:87-90` 保留"被覆盖的那个永远不参与比对"；历史那半句由 `architecture.md:77-78`"不同结构体的同名方法不再互相覆盖"覆盖 → 判为落地 |
| E13 | 构建标签互斥的同名文件仍判重名，末尾提示 | 文件 `:89-90` |
| E14 | 旧实现删空白会把 `"剩余 N 秒"` 与 `"剩余N秒"` 判成一致（冻结文案） | `invariants.md:367-369` 逐字含该例 |
| E15 | 注释仍按旧规则忽略空白；分号也当空白 | 文件 `normalize` 注释 + `app.md` A-038 保留全句 |

### 2.2 `tools/mkres/main.go` —— 无丢失

| # | HEAD 的说法 | 下落 |
|---|---|---|
| M1 | 取代 windres + `assets/version.rc`，**不再需要 MinGW/binutils** | 文件 `:2`"不含 C 编译器" + 指针 `architecture.md「工具链分界（勿混用）」`；`architecture.md:153-155` 确有该结论（含 MinGW 只用于 `-race` 的例外） |
| M2 | 图标组沿用 `IDI_ICON1`；该符号未被头文件定义、windres 按名字记录 | 文件 `:36-37`（`iconName` 注释） |
| M3 | 图标图像直通 `.ico` 原始字节（含 PNG 压缩 256），不重编码 | 文件 `:119`（`LoadICO` 前） |
| M4 | 图像条目 neutral(0)、图标组 0x0409、Windows 语言回退 | 文件 `:127-128` |
| M5 | 版本资源 id=1 / 0x0409 / FileOS/FileFlags/FILETYPE 由 winres 按 VOS_NT_WINDOWS32、0x3f、VFT_APP 填充 | 文件 `:92-93` |
| M6 | 唯一新增 manifest 声明 DPI 感知；**旧 webview 库运行时调 `SetProcessDpiAwarenessContext`，换绑定后要靠 manifest 补回** | 文件 `:120-124`"而非运行时 API" + 指针；`invariants.md:282-286` 逐字含"旧库是在运行时调 `SetProcessDpiAwarenessContext`…" |
| M7 | permonitorv2 带 system 回退；asInvoker 必须（提权改变 UIPI 判定、提示失真） | 文件 `:125-127` |
| M8 | winres 默认 supportedOS(win7~win10)，不影响可运行范围 | 文件 `:128-129` |
| M9 | 产出 `_amd64`/`_arm64.syso`，按架构各取所需；`.syso` 不入库见 `.gitignore` | 文件 `:7`（含"不入库"）；"arm64 能拿到同架构资源"与 `.gitignore` 指向均可从代码/仓库推出 → 可接受 |

### 2.3 `tools/pecheck/main.go` —— 无丢失（旧头部 4 条校验项全部有落点）

| # | HEAD 的说法 | 下落 |
|---|---|---|
| P1 | `.syso` 不入库；缺失/架构不匹配时 `go build` 不失败，症状是空白图标/属性页版本号空/高分屏发虚 | 文件 `:4-6` |
| P2 | 本工具直接读 exe 的 PE 头与资源节，把"该在的东西在不在"变成会失败的检查 | 文件 `:1-2` 的结论 + 代码 `debug/pe` / `winres.LoadFromEXE` → 机制可推导 |
| P3 | CI 构建矩阵与本地发版验收共用同一份判据 | 文件 `:2` + `architecture.md:72` |
| P4 | `build.ps1` 末尾的版本读回是它的 PowerShell 前身，只查 FileVersion | 文件 `:5-6` |
| P5 | 校验项① PE 机器类型与 `-arch` 一致（0x8664 / 0xAA64） | 代码 `:40-43` 用 `pe.IMAGE_FILE_MACHINE_AMD64/ARM64`；hex 值由 stdlib 定义、可查 → 可推导 |
| P6 | 校验项② 版本资源 FileVersion 数字段补足四位；字符串表 FileVersion/ProductVersion 一致 | 代码 `:113-141`，含 `:133` 原注释 |
| P7 | 校验项③ manifest `permonitorv2,system`、`asInvoker` | 代码 `:154-156`（连"提权改变 UIPI 判定"的理由都在） |
| P8 | 校验项④ `RT_GROUP_ICON` 存在且非空；**不数图标帧数——那是 `assets/icon.ico` 的属性，重新生成图标就会变** | 代码 `:165-169` + **`architecture.md:73` 逐字含该理由** |
| P9 | 用法一行 | 文件 `:9` |

### 2.4 `tools/wmcharprobe/main.go`

| # | HEAD 的说法 | 下落 |
|---|---|---|
| W1 | 靶子是 `testdata/completion-guard.html`；那些行为挂在 keydown 层；WM_CHAR 不产生按键事件 | 文件 `:4-5` |
| W2 | "WM_CHAR 直投**不需要目标窗口处于前台**，与焦点无关" | 文件 `:6` 改写为更准确的一句："按窗口句柄直达、与焦点无关（char/direct 仍要先激活目标才能解析焦点子窗口）"——后半句经代码 `:341`、`:363` 的 `activate()` 验证为真 |
| W3 | char 模式（含换行、需激活） | 文件 `:12` |
| W4 | direct 模式（镜像产品算法、Tab 走 WM_CHAR、换行前 Esc+回车） | 文件 `:13` |
| W5 | keys 模式（SendInput KEYEVENTF_UNICODE、**与 Type 逐字符路径同款**、需前台、倒计时 2 秒） | 文件 `:14-15` **保留了"同款"** + `sendKeys` 注释 `:238` + 代码 `:388-398`（前台不是匹配窗口就退出） |
| W6 | close 模式 | 文件 `:16` |
| W7 | `@路径` 说明 + 两个示例 | 文件 `:17`（说明）+ `:9`（示例一）；**示例二 `wmcharprobe completion-guard "(" keys` 被删**（trivial，用法签名已含 `keys`） |
| W8 | 成功判据 c/k/p/a、keys 的 p 应增、两者对照证明绕过按键层 | 文件 `:29-31`（并扩充 L/h 与"只有真按键才落 L"；键义经 `completion-guard.html:334-336,348` 核对**准确**） |
| W9 | `findTargets` 注释（枚举可见顶层窗口、子串命中、查渲染子窗口、**找不到退回顶层窗口**） | 删除；代码 `:156-179`（`sendTo: hwnd` 默认 + 命中才换）**完全可推导** |
| W10 | `sendText` 注释（逐 UTF-16 码元、返回失败个数） | 删除；代码 `:196-206` 可推导 |
| W11 | `sendVK` 注释（按下+抬起、返回是否被接受） | 删除；代码 `:208-215` 可推导 |
| W12 | `sendDirect`（镜像产品算法、与 `sendEscaped` 同序 Esc+20ms+本键） | 文件 `:217-219` |
| W13 | `focusedTarget`（不退回顶层窗口；产品 v1.5.6 退化；顶层容器丢 WM_CHAR 而 `SendMessageTimeout` 照样成功；本探针只测 WM_CHAR；docs 恰恰让人拿这工具输出当判断依据） | 文件 `:19-24` + `:280-286` 压缩版 + 指针 `invariants.md「文本直投」`（`:146-149` 有该退化规则）；只有最后那句"docs 让人拿它当判断依据"的元叙述被删 → 低危 |
| W14 | `@路径` 的行尾注释 | 删除；信息在文件 `:17` ✅（任务点名，验真通过） |
| W15 | `case "direct"` 旧行尾注释"字符 WM_CHAR + 换行/**Tab 前** Esc 先行" | 改为 `:360`"字符(含 Tab)走 WM_CHAR, 换行前先 Esc 再发真回车" ✅（任务点名，见第 3 节） |

**新增（非删除）的信息**：`wmcharprobe:26-31` 的遥测键表、`:19-24` 的"取焦点窗口"块 —— 与
`completion-guard.html:334-336`、`:341/:363`、`:388-398` 逐条核对**全部准确**。

## 3. 两处行尾注释的验真（任务第 3 项）

1. **`@路径`（`wmcharprobe` 旧 `if strings.HasPrefix(text, "@") { // @路径: 从 UTF-8 文件读文本`）**
   —— 信息**确实在文件头用法块里**：`tools/wmcharprobe/main.go:17`
   "文本可写 `@路径` 从 UTF-8 文件读取(含中文等非 ASCII 时推荐, 避开命令行编码)"，
   且 `:9` 的用法示例第一行就是 `@D:/tmp/cn.txt` 的用法。删行尾注释不丢信息 ✅。

2. **`case "direct"`（`wmcharprobe:360`）**
   —— 新表述与实现和文档**三方一致**：
   - 实现 `tools/wmcharprobe/main.go:220-236`：只有 `r == '\n'` 才 `sendVK(VK_ESCAPE)` + 20ms + `sendVK(VK_RETURN)`；
     Tab 与普通字符一样走 `sendRuneMsg`（WM_CHAR）。
   - 文档 `docs/invariants.md:131-134`：**Tab 可以走文本层；换行不行**（Chromium 过滤 `\n`/`\r`），
     换行必须走真按键（`sendEscaped(SendEnter)`，Esc + 20ms + 回车）。
   - 因此旧注释"换行/**Tab** 前 Esc 先行"确实是**错的**，这次是纠正而不是信息丢失 ✅。
   （与 `internal/typing` 的 `sendEscaped` 同序这一点也由 `invariants.md:140` 印证。）

## 4. 闸门收紧与实跑（任务第 4、6 项）

- 基线已收紧：`cmd/type/comment_contract_test.go:112-115` 四个 tools 文件均显式为 `0`
  （写成显式 0 而不是删键，顺带保住了"基线里的文件没被扫到"这条检查）。
- 变异（沿用备份+还原纪律）：
  - 目标 `tools/mkres/main.go`，备份到 `%TEMP%\batch1-verify\mkres.bak`，
    `SHA256 before = 7B0A3BC6…B28001`，追加 12 行 `//` 后 `3A11F9D1…E4E240F`（已落盘）。
  - `go test -count=1 ./cmd/type/` → **FAIL**：
    `comment_contract_test.go:185: tools/mkres/main.go 有 1 个超过 10 行的注释块, 基线 0` —— **拦住** ✅
  - 还原后 `restore-hash-equal=True`、`git status --short` 与基线**逐字相同**（含未跟踪项）✅
- 实跑：`go test -count=1 ./cmd/type/` 变异前 `ok 0.703s`、还原后 `ok 0.816s` → **全绿** ✅

## 5. 台账缺口与"留原地"可推导性判断（任务第 5 项）

- **缺口成立**：`docs/comment-ledger/app.md` 里 grep `mkres|pecheck` **0 命中**（只有 envcheck/equivcheck 相关）；
  `progress.md:39-41` 自己也写明"从未登记进台账……'台账保证不漏行'这条覆盖不到它们"。
  后果：`minRows`（app.md 40）对补台账没有约束力，补条目时不会有人被"条数下限"提醒。
- **可推导性判断**（mkres / pecheck 没有台账条目，所以只能评"改写后留在代码里的事实"）：
  - **其实能从代码推出、本可不留（2 处，低危）**：
    ① `pecheck:1-2` 的"直接读 exe 的 PE 头与资源节"机制 —— 由 `debug/pe` 导入与 `loadResources` 可见；
    ② `mkres:7` 的"因此 arm64 构建能拿到同架构的资源对象" —— 由产出两个 `.syso` 的循环可见。
    两处都在文件头/文件头式注释的合理篇幅内，**不建议为它们开工单**。
  - **其实不可推导、但删对了（1 处）**：`pecheck` 旧头部第 4 条校验项"不数图标帧数"的理由，
    看似会被误删，实际 `architecture.md:73` 已逐字承接 → 不构成丢失。
  - **不可推导却没能落地（1 处）**：**E2**（equivcheck 的旧正则实现史），见第 2.1 节。
- 额外提醒：`equivcheck` 文件里 `funcRe`（`:28`，`bodyLine` 在 `:345` 用）**仍是活的**，
  而解释"为什么抽取只该用 `go/parser`、正则只配做差异定位"的那段历史被删后，
  读者会看到一处没有来由的 `regexp` —— E2 的信息价值不只是怀旧。

## 6. 未通过项清单

| # | 严重度 | 问题 | 证据 |
|---|---|---|---|
| B1 | **低-中** | **信息无下落（E2）**：equivcheck 旧文件头关于"旧实现用正则扫行取函数、遇到函数内行首 `}` 提前截断、历史 rev 上只能抽出 2 个函数、结论不可用"的记述，在改写后**任何地方都找不到**：不在文件、不在 `docs/invariants.md` 新不变量、不在 `progress.md`、不在 `app.md` A-037/A-038 | `git show HEAD:tools/equivcheck/main.go` 原 `:2-4`；`docs/invariants.md:365-373`；`docs/comment-ledger/progress.md:17-21`；全仓 grep 0 命中 |
| B2 | 低（记录在案，不算丢失） | `wmcharprobe` 用法示例二 `wmcharprobe completion-guard "(" keys` 被删；`focusedTarget` 里"docs 让人拿这个工具的输当下判断的依据"这句元叙述被删 | `tools/wmcharprobe/main.go:9-17`、`:280-286` |
| B3 | 低（台账缺口） | `app.md` 无 mkres / pecheck 条目，`minRows` 对它们的"防漏行"无效 | `progress.md:39-41`；`app.md` grep 0 命中 |

**没有发现的问题（确认项）**：代码/字符串/常量零改动（token 流 0 差异）；构建约束未变；
`progress.md` 的注释行数与剥离行数数字准确；两处行尾注释变动都是正确的（一处去重、一处纠错）；
闸门基线确实收紧且真的拦得住。

## 7. 第 2 批开工前必须补的东西

1. **给 E2 找落点（必须）**：最省事的是在 `docs/invariants.md` 那条 equivcheck 不变量末尾加一句
   （"取函数体必须用 `go/parser`：旧实现用正则扫行 + 行首 `}` 收尾，遇到函数内的行首大括号会提前截断，
   历史 rev 上只抽出 2 个函数；代码里的 `funcRe` 只用于差异定位的 `bodyLine`，不再用于抽取"）。
   这样也顺手解释了那个"孤零零的 `regexp`"。
2. **补 mkres / pecheck 台账条目（必须，或显式豁免）**：`app.md` 至少补两条（mkres 的
   iconName/neutral(0)/语言回退、pecheck 的 `firstResource` 为什么不按 (id,语言) 取 + `fileVersion`
   与 mkres/build.ps1 同规则），并把 `minRows` 从 40 抬到新条数；
   若决定豁免，就把"mkres/pecheck 不进台账"的理由写进 `docs/comment-style.md` 的"已知不设闸门"清单，
   别让它只留在会被删除的 `progress.md` 里。
3. **第 2 批沿用本批的两条好做法**：显式 `0` 基线（而不是删键）；对涉及"行尾注释"的文件
   在 `progress.md` 里单列一节说明（本批就是这么做的，验真成本很低）。
4. 建议第 2 批的独立验证直接用本报告第 1 节的方法（HEAD 副本按字节取 + token 流比对 +
   逐 hunk 行分类）：它能在**不看改写者说明**的情况下抓住"重排代码行""改字符串""改构建标签"三类偷改。
