# 阶段 2 第 2 批核验：frontend 全部注释重写（task-21）

- 核验人：agent（verifier-core），日期 2026-10-04。
- 核验对象：相对 `HEAD` 有改动的 10 个文件 —— `frontend/tools/layout-probe.mjs`、
  `frontend/test/typingTask.test.ts`、`frontend/src/composables/useUiScale.ts`、`useTheme.ts`、
  `useTypingTask.ts`、`frontend/src/components/statusBarState.ts`、`StatusBar.vue`、
  `frontend/src/viewportReport.ts`、`frontend/src/App.vue`、`frontend/src/main.ts`。
  任务同时点名但**零改动**的两个：`frontend/src/types.ts`、`frontend/src/ipc.ts`。
- 写权限：本文件（未在仓库内落任何分析器/临时文件；HEAD 版由 `git show` 内存取出）。

## 0. 结论

**通过，没有"必须补"的信息丢失项。**

- 「只动注释」：改动的 10 个文件全部成立，用与改写者不同的方法（TypeScript 词法扫描器的
  token 流）独立证明 **0 差异**；`.vue` 的 `<template>` 再做原始字节比对，逐字节相等。
  `git diff -U0` 的 101 个增删行里 **0 行非注释**（97 注释行 + 4 空行）。
- 「信息零丢失」：任务点名的 4 处重点全部有下落（3 处仍在文件内、1 处转为 docs 指针），
  逐条追了本批删/改的说法，无下落者 0 条；另记 2 类台账措辞与实际不符（Lead 已接受）与
  2 处低危语义压缩（事实另有落点）。
- 契约字面量：`types.ts` / `ipc.ts` 零改动；六绑定名、`startTyping` 四参顺序、
  `BASE_WIDTH = 540` 人工逐字确认，`go test -count=1 ./cmd/type/` 全绿。
- 实跑：`cd frontend; npm test` → **30/30 pass, 0 fail**；`go test -count=1 ./cmd/type/` → `ok`。

## 1. 「只动注释」的独立验证

**方法一（词法层，主证据）**：用 `frontend/node_modules` 自带的 TypeScript 5.9.3
`ts.createScanner(ScriptTarget.Latest, skipTrivia = true)` 分别扫 `HEAD` 版（`git show HEAD:<路径>`）
与工作区版，收取 `(token 种类 + token 原文)` 序列。注释与空白被 `skipTrivia` 丢弃，
而**字符串 / 模板串 / 正则 / 数字字面量的原文参与比对**，所以改字符串、改正则、改常量必然暴露。

**方法二（`.vue` 结构层）**：`<script>` 块走上面的 token 流；`<template>` / `<style>` 块把
HEAD 与工作区的**原始字节直接比对**（连注释都不剥，最严）—— 只动注释时它必须完全相等。

| 文件 | token 差异 | `.vue` template 原始字节 |
|---|---|---|
| `frontend/tools/layout-probe.mjs` | 0 | — |
| `frontend/test/typingTask.test.ts` | 0 | — |
| `frontend/src/composables/useUiScale.ts` | 0 | — |
| `frontend/src/composables/useTheme.ts` | 0 | — |
| `frontend/src/composables/useTypingTask.ts` | 0 | — |
| `frontend/src/components/statusBarState.ts` | 0 | — |
| `frontend/src/components/StatusBar.vue` | 0 | rawEqual = true |
| `frontend/src/viewportReport.ts` | 0 | — |
| `frontend/src/App.vue` | 0 | rawEqual = true |
| `frontend/src/main.ts` | 0 | — |
| `frontend/src/types.ts`（零改动） | 0 | — |
| `frontend/src/ipc.ts`（零改动） | 0 | — |

**方法三（交叉印证，git 层）**：`git diff -U0` 把每个增删行按 trimmed 首字符分类 ——
**comment-ish 97 / blank 4 / 非注释 0**，合计 101 == 31 insertions + 70 deletions。

**方法边界**：token 流看不见构建约束行（本批 10 个文件都没有 `//go:build` 类指令），
也看不见纯空白重排（JS/TS 无语义）；前者由方法三的逐行分类覆盖，后者由 `.vue` 原始字节比对覆盖。

**稳定性**：核验前后两次跑方法一，结论一致；报告末尾记了 12 个文件的 SHA256 前 12 位。

## 2. 信息下落清单（重点）

判定四处：**仍在文件里**（给行号）/ **已进 docs 小节**（read 确认）/ **台账已声明** / **无下落**。

### 2.1 任务点名的四项

| 点名项 | 下落 |
|---|---|
| `useUiScale.ts`「挂载时机 + ResizeObserver 校准」 | **仍在文件**：`useUiScale.ts:5-6`（"先写一次免得首帧按 1 渲染再跳，再由 ResizeObserver 持续校准，只读一次不够"）；代码 `:41-50` 与 `:52-56` 未动。三条边界里 ② 由 `docs/invariants.md:250-251`（540→1、648→1.2、675→1.25 才够得着上限）承接，① "只由宽度驱动、不读 devicePixelRatio" 的理由仍在 `frontend/src/style.css:75-84`（`:84` 逐字含"同一台显示器上不该出现两种结果"），新注释正文已明确指向"style.css 的 `--ui-scale` 处" |
| `statusBarState.ts` 的 `-1` 哨兵 | **docs 指针**：`statusBarState.ts:3` → `docs/invariants.md「其它不变量」`，该节 `:254-266` 完整载明 0~100/-1 语义与"空闲态凭空长出一条空进度轨道"的事故；测试 ①「-1 哨兵: 隐藏进度条」钉住 |
| `statusBarState.ts` 的 idle 空串 | **原样保留**：`statusBarState.ts:7-11`（JSDoc："idle 不加相位类…空串是刻意保留的…与改动前逐字一致"，台账 A-027 判 `留原地`）；测试 ④「idle 不加类(空串与改动前逐字一致)」另钉 |
| `viewportReport.ts`「有机器上第一拍读到 1」 | **仍在文件**：`viewportReport.ts:8`（`REPORT_DELAY_MS` 的 JSDoc 逐字保留） |

`useTheme.ts` 的"存储键两处 + 内联脚本先行 + 判定只许一处"同样**仍在文件**：
`useTheme.ts:2-4`（内联脚本必须早于 Vue、初始值读它落下的 `.dark`、"同一判定不许有两处"、
存储键 `type-theme` 写在两处及其走散症状）；另有 `cmd/type/contract_test.go:354-357,374-376`
与 `frontend/index.html:7-11` 兜底。

### 2.2 其余删/改说法的下落

| HEAD 的说法（文件） | 下落 |
|---|---|
| App.vue：置顶不随重载复位、重载后必须回读（否则按钮与窗口相反） | 仍在 `App.vue:50-51`；两条不受控来路（Ctrl+R/F5、渲染进程崩溃自动重载）仍在 `useTypingTask.ts:144-146` |
| StatusBar.vue：三条取值规则在 statusBarState.ts、由 test 钉着 | 该事实即 `statusBarState.ts:2-3` 自身（引用方删掉冗余指针，`StatusBar.vue:4` 的 import 仍在） |
| statusBarState.ts：`progressVisible` 的 0~100/-1 语义 | `docs/invariants.md:254-256` 逐字承接 |
| statusBarState.ts：`progressWidth` 上限 100% 替后端兜越界值 | 台账 A-028 判 `D`（复述代码）→ `删`；测试 ③「越界的进度值被夹在 100%」钉住 |
| useTheme.ts：about:blank / localStorage 抛 SecurityError / 退回内存 | `useTheme.ts:5-6` + 指针 `docs/invariants.md:387-394`（read 确认） |
| useTheme.ts："正式版主题不跨启动保留、每次启动按系统深浅色；刻意取舍=首帧不闪白优先" | 压缩掉，落点：`README.md:87`、`docs/invariants.md:390`、`frontend/index.html:8-9`、`release-notes/v1.5.7.md:41` |
| useTypingTask.ts：chain 为何必要（双链 + 取消被盖回倒计时） | 指针 `useTypingTask.ts:24-25` → `docs/invariants.md「目标窗口与轮询」`，该节 `:20-26` 完整承接（含回归用例指引） |
| useUiScale.ts：取值边界 ②/永不参与项 | `docs/invariants.md:246-253`（read 确认）+ `style.css:75-84`；`useUiScale.ts:12` 保留"1.25 正常路径取不到" |
| main.ts：节拍与"为什么只能从这一侧报" | 指针 `main.ts:8` → `docs/invariants.md:215-217`（立刻一拍 + 400ms，read 确认） |
| viewportReport.ts：实测某用户机 720×600/1.75 → 视口 411×343 → 输入框盖住选项行 | `docs/invariants.md:205-213` 完整载明（台账 A-002 已判 `删`） |
| viewportReport.ts：两拍是刻意的、宿主同一比值只处理一次 | `viewportReport.ts:27-28` + `docs/invariants.md:215-217` |
| layout-probe.mjs：两条取数路口径不同、坑写在「三个坑」一节；到不了的档位如实跳过 | `layout-probe.mjs:8-9`（指针改为"见③"）；该节标题与内容仍在 `:21-35`，逐字未动 |
| typingTask.test.ts：文件头 + 四条既有行为 + 两条防回归 | `typingTask.test.ts:5-12` 全在；被改的只有一条分隔用 `//` → 空行 |

### 2.3 台账"去向"与实际不符（Lead 已接受的偏离，事实均未删）

- **A-007 / A-016 / A-031 / A-032** 台账（`app.md:18,27,42,43`）写"上移 `docs/invariants.md`，
  代码留一行指针"，但 docs 无对应段落、代码里也没加指针；实际是**留原地**：
  A-007 → `App.vue:50-51` + `useTypingTask.ts:144-147`；A-016 → `useTheme.ts:2-4`
  （+`contract_test.go:354-357`）；A-031 → `useTheme.ts:5-6`；A-032 → `App.vue:18-19,29`。
  本次逐条 read 确认**都在**，没有被删。
- **A-029**（`app.md:40`）写"docs/invariants.md:246-253 已载"，但该区间**不含**"① 只由宽度驱动、
  不读 devicePixelRatio"；该事实实际落点是 `style.css:84`，新注释正文也指向 style.css。
  信息未丢，只是台账引用的落点不准。

### 2.4 低危语义压缩（事实另有落点，不算丢失）

1. `useUiScale.ts` 旧"1.25 是防御性上限…留着是为了将来放宽上限时不必回来改这里"的前瞻理由被删；
   现状（`useUiScale.ts:12` + `docs/invariants.md:251`）只保留"1.25 正常路径取不到"。
2. `useTheme.ts` 的"首帧不闪白优先"取舍理由从注释移走，落点在 `index.html:8-9` 与 `README.md:87`。

## 3. 契约字面量未动（任务第 3 条）

- `git diff --name-only HEAD -- frontend` 只列出上述 10 个文件，**不含 `types.ts` / `ipc.ts`**。
- 人工逐字确认：`types.ts:5-11` 六个 phase 取值、`:14-22` 五个字段与 json 键；
  `ipc.ts:9,11,13,15,17,19` 六个绑定名、`:30-31` `startTyping(text, delay, forceRaw, textDirect)`
  的顺序与转发、`:18-19` `reportViewport(dpr: number)`；`useUiScale.ts:11` `BASE_WIDTH = 540`。
- `go test -count=1 ./cmd/type/` 覆盖 `contract_test.go`、`comment_contract_test.go`、
  `build_contract_test.go`，全部通过。

## 4. 实跑结果（任务第 4 条）

- `cd frontend; npm test` → `tests 30 / pass 30 / fail 0`，`duration_ms 267`，exit 0。
- `go test -count=1 ./cmd/type/` → `ok github.com/LeuJasYoh/type/cmd/type 0.714s`，exit 0。

## 5. 未通过项与建议

**未通过项：无。**（无信息无下落项，无代码/字符串/模板/正则改动，无契约字面量变化。）

建议（低优先级，均不阻塞本批收口）：

1. `app.md` 里 A-007 / A-016 / A-031 / A-032 的"去向"改成 `留原地`（或在 `docs/invariants.md`
   补等价段落），否则台账删除后这 4 条的"上移"承诺没有任何记录；Lead 已接受现状，
   此条只是让台账与事实一致。
2. `app.md` A-029 的落点由 `docs/invariants.md:246-253` 改为 `frontend/src/style.css:75-84`。
3. 台账 `progress.md` 缺第 2 批（frontend）的文件级记录表；若要求与第 1 批同款，
   请让改写者补齐（本次核验数据可直接引用本文件第 1 节的 10 行表）。

## 6. 核验时的文件指纹（SHA256 前 12 位，报告落笔时）

| 文件 | SHA256[:12] |
|---|---|
| `frontend/src/App.vue` | `8ABBE891C00E` |
| `frontend/src/components/StatusBar.vue` | `F9BAA634FA6F` |
| `frontend/src/components/statusBarState.ts` | `F72797839785` |
| `frontend/src/composables/useTheme.ts` | `83C8186EDB55` |
| `frontend/src/composables/useTypingTask.ts` | `ADCBC41B9328` |
| `frontend/src/composables/useUiScale.ts` | `06A089C0BC8F` |
| `frontend/src/main.ts` | `08B51265246C` |
| `frontend/src/viewportReport.ts` | `B653B5C31FD1` |
| `frontend/test/typingTask.test.ts` | `94436EB9D6BB` |
| `frontend/tools/layout-probe.mjs` | `695C21CDC96E` |
| `frontend/src/types.ts`（未改） | `EFE907AC43EA` |
| `frontend/src/ipc.ts`（未改） | `F60E6A569838` |
