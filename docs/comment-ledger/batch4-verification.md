# 第 4 批独立核验报告：internal/typing + cmd/type 注释重写

- 核验人：verifier-infra（独立于本批两名改写者）
- 核验对象（相对 HEAD）：`internal/typing/typing.go`、`internal/typing/taskrun.go`、
  `cmd/type/main.go`、`cmd/type/contract_test.go`、`cmd/type/build_contract_test.go`、
  `cmd/type/devserver_dev.go`、`cmd/type/devserver_prod.go`
- 结论：**通过**（代码/常量/冻结文案零改动，信息零丢失，命令全绿）；1 处指针空指向待处理，
  不影响代码信息（详见第五节）。

## 一、只动注释（通过）

方法一（词法级）：按 Go 词法规则剥掉 `//` 与 `/* */` 后逐 token 比对 HEAD↔工作区，
字符串（含转义）、原始字符串、字符字面量**原文参与**；另单独比对 `//go:` 指令行
（它们是注释 token，词法比对看不见）。

| 文件 | token 数 | 结果 |
|---|---|---|
| internal/typing/typing.go | 768 | 全等 |
| internal/typing/taskrun.go | 641 | 全等 |
| cmd/type/main.go | 245 | 全等 |
| cmd/type/contract_test.go | 1218 | 全等 |
| cmd/type/build_contract_test.go | 541 | 全等 |
| cmd/type/devserver_dev.go | 47 | 全等 |
| cmd/type/devserver_prod.go | 16 | 全等 |

`//go:build` 指令 7/7 未变（`... && dev` / `&& !dev` 二选一关系完好）。

方法二（行级）：`git diff -U0` 中唯一的"非整行注释"改动是
`internal/typing/typing.go:35` 的 `SendText` **行尾**注释改写
（`见文本直投` → `见 docs/invariants.md「文本直投」`）；其余被标记的行是 3 行纯空行
（拆块所致）。行尾注释那行代码逐 token 相同 —— 按 `progress.md` 的要求在此单列说明。

→ 代码、常量、字面量零改动。

## 二、冻结面专项（通过）

1. **17 条冻结文案**：逐条在源码里检索，16 条在 `internal/typing` / `cmd/type` 中逐字命中；
   第 17 条 `请输入要模拟键入的文本` 属前端（`frontend/src/composables/useTypingTask.ts:80`），
   本批未触碰。
2. `internal/typing/contract_test.go` **未被改动**（git status 无此文件），字面量表原样。
3. `cmd/type/contract_test.go` 仅注释改动（token 全等），其字面量断言（Bind 名、
   `wantStartArgs`、`wantTypes`、倒计时格式串）未变。
4. `msgCountdownFormat` 仍在 `internal/typing/typing.go:134`，并新增原地警示（:131）。
5. 六个 phase 常量仍以 `Phase` 开头（typing.go:88-93），取值未变。
6. `startTyping` 四参顺序未受影响：`typing.go:261` 签名
   `Start(text string, delay int, forceSendInput bool, textDirect bool)` 逐 token 相同。
7. `win32_webview2.go` / `win32_instance.go` 的启动提示文案行未被本批（win32）触碰。

## 三、信息零丢失（逐条下落）

| # | 被删/压缩的说法 | 下落 |
|---|---|---|
| 1 | taskrun.go 头 "拆分前 256 行/9 形参" | `docs/architecture.md:12`（256 行/9 参 + 判定顺序与守卫位置一字未改） |
| 2 | taskrun.go 头 "40 条用例" 过时数字 | **已删**；`typing_test.go:637` 的"当时 40 条"是历史叙述，不在本批、语义正确 |
| 3 | "6 个跨 150 行持续改写的局部变量" | 未单独落文档；判为可从 `architecture.md:11-14` 的拆分说明推出，trivial |
| 4 | runCountdown 采样/写状态分离、响应 1 拍、总时长 delay 秒 | `docs/invariants.md:45-47` |
| 5 | 漂移守卫细节（顶层标识、候选窗/补全弹窗不误触发、毫秒级残余） | `invariants.md:36`、`:53` |
| 6 | 文本直投（含 Tab、Chromium 过滤 \n\r、先 Esc 关弹窗） | `invariants.md`「文本直投」（122 起，W-030） |
| 7 | 快照不完整退回、剪贴板失败不再退逐字符 | `invariants.md:336-350` |
| 8 | HoldsText 两返回值、Snapshot 的 Complete、恢复失败可见 | `invariants.md:322-343` |
| 9 | TargetID：标题会重名/会变 | `invariants.md:35` |
| 10 | Sample 三要素必须同一次读的两种矛盾 | `invariants.md:28-35`（W-019） |
| 11 | Start 重入实测"300 轮里 246 轮" | `invariants.md:67` |
| 12 | Start 采样在锁外、两次读取消标志的区分 | 代码保留 + `invariants.md:73-99` |
| 13 | 空文本"判据必须在后端"的理由 | `docs/behavior-contract.md:36-41` |
| 14 | Cancel 不覆盖已有结局、判据取状态 | `invariants.md:107-113` |
| 15 | typeTextViaClipboard"不必回报碰没碰过" | `invariants.md:344-350` |
| 16 | main.go 四条红线 | **全部保留在代码内**，逐条比对信息密度未降：WM_DPICHANGED（用户明确不要 + 不同步→位图拉伸发虚）、HintFixed（样式位/唯一来源/TestWindowSizeIsFixed/只调 SetWindowClientRect 的反例）、SetSize 顺序（先 HintFixed 再 SetWindowClientRect，反过来非 DPI 版边框再摆一次 + windowRectForClient）、lw/lh（设计逻辑尺寸 + 客户区=设计尺寸×内容缩放） |
| 17 | main.go 内容缩放实测叙事与回落策略（含 style.css 兜底） | `invariants.md:205-240` |
| 18 | createWebView2 的 HRESULT bug 与完整性级别触发条件 | `invariants.md:298-316` |
| 19 | contract_test.go reportViewport"只钉签名是假绿" | `invariants.md:232-240` |
| 20 | contract_test.go UiScale 旧 480/720 漂移史 | `invariants.md:246-253`（代码亦保留） |
| 21 | devserver_prod"右键菜单重载会让前端与后端任务脱钩" | `cmd/type/main.go:43-44` 保留 |
| 22 | A-007（置顶要读回真实状态） | 确认仍在代码内：`cmd/type/main.go:109-112` |

**无 docs 落点但判为 trivial 的 1 条**：Start 里"判定时的预览与锁定后的目标之间存在一次
采样的间隔（倒计时结束还会再采样一次）" —— 可直接从 `runCountdown` 末尾再采样一次读出，
属 D 类复述。

新增指针目标均已读文档确认存在且内容对得上（`architecture.md`「布局与单一来源」覆盖
build 链单一来源与 devserver 二选一；`invariants.md` 各节；`behavior-contract.md`
「行为契约（冻结，改动需双端同步）」）。

## 四、命令结果

- `gofmt -l ./cmd ./internal` → 空，exit 0
- `go vet ./cmd/type/ ./internal/typing/` → exit 0
- `go test -count=1 ./cmd/type/ ./internal/typing/` → 两个包均 ok（一并覆盖
  `TestDocSectionReferencesResolve` 与注释契约四闸门：NoBlockComments / BlockBudget /
  DocRefFormatFrozen / LedgerHasDisposition 全 PASS）

注释行数 前→后（超额块 前→后）：
typing.go 197→147（2→0）、taskrun.go 98→62（0→0）、main.go 75→57（2→0）、
contract_test.go 72→60（0→0）、build_contract_test.go 57→42（1→0）、
devserver_dev.go 5→6（0→0，新增一条指针）、devserver_prod.go 8→6（0→0）。

## 五、未通过项与建议

1. **空指向 1 处（需 Lead 定夺，超出本核验写权限）**：
   `cmd/type/contract_test.go` 的 `TestThemeBootScriptKeepsKeyAndOrder` 新增
   `见 docs/invariants.md「其它不变量」`，但"主题存储键写在两处 / 首帧防闪烁 / 判定只许有
   一处"这条事实**不在该节**：全 docs 检索 `首帧`/`防闪烁`/`type-theme`/`该用哪套主题`
   无命中（`invariants.md:387-393` 只讲 `about:blank` 下 localStorage 不可用；
   `README.md:87` 记了"不跨启动保留 + 首帧要不闪白"的取舍，但仍不是该指针所指）。
   事实本身完整留在该注释里，**不构成信息丢失**，但违反"指针=权威在那边"。
   建议二选一：删掉这条指针（保留原地说明）／把该事实补进
   `docs/invariants.md「其它不变量」`。两者都需动我不被授权的文件。
2. **闸门基线待下调（housekeeping）**：本批 `typing.go`、`main.go`、
   `build_contract_test.go` 及 win32/前端各文件的超额块已降为 0，`comment_contract_test.go`
   的 `longCommentBudget` 需同步下调（测试 `t.Logf` 已逐条列出；我不能改测试文件）。
3. **范围外提醒**：`frontend/src/*` 有改动而 `internal/web/dist/index.html` 未随之更新，
   违反 AGENTS 铁律 2、会触发 `verify.yml:92-114` 的漂移检查 —— 不属本批，但会影响整仓提交。
