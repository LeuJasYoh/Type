# 阶段 2 进度与销账记录（临时产物）

注释重写的**四批已全部完成**。本文件记录每批的改动、销账、独立核验结论与已知偏离；
重写收尾后本目录（含本文件）整体删除，仍有效的长期内容已沉进 `docs/`。

台账（不可推导事实清单）：`typing.md` 43 条 / `win32.md` 43 条 / `app.md` 42 条 = **128 条**。
独立核验报告：`batch1-verification.md`、`batch2-verification.md`、`batch3-verification.md`、
`batch4-verification.md`、`gate-verification.md`（闸门三轮）与 `batch1-verification.md`。

## 总览（2026-10-04）

| 批 | 范围 | 文件 | 注释行 前→后 | >10 行块 前→后 | 独立核验结论 |
|---|---|---|---|---|---|
| 1 | `tools/` | 4 | 182 → 140 | 4 → 0 | 通过；1 条信息无下落 → 已补进 `docs/invariants.md` |
| 2 | `frontend/src` + `test` + `tools` | 12 | 199 → 175 | 3 → 0 | 通过；无丢失项，3 条低优先级建议 |
| 3 | `internal/win32/` | 8 | 481 → 379 | 8 → 0 | 通过；1 条指针过度声明（U-1）→ 已补 docs |
| 4 | `internal/typing` + `cmd/type` | 7 | 512 → 380 | 5 → 0 | 通过；1 条空指向 → 已补 docs |
| | **合计** | **31** | **1374 → 1074（−22%）** | **20 → 0** | 四批均证明代码零改动 |

全仓口径（含测试与脚本、按同一计数法）：注释行 **1628 → 1447**，占比 **19% → 17%**。
闸门基线：四批完成后 `longCommentBudget` 与 `bareDocRefBudget` **全数为 0**。

## 只动注释的证明（每批一份独立方法）

- 第 1、4 批：`go/scanner` 去掉 COMMENT 后逐 token 比对（字面量原文参与），全文件 TOKENS-IDENTICAL。
- 第 2 批：TypeScript 词法扫描器（skipTrivia）逐 token 比对，12 个文件 0 差异；`.vue` 模板逐字节相等。
- 第 3 批：字符级状态机剥 `//` 与 `/* */`（正确跳过字符串/原始串/转义），8 文件残行全等。
- 每批都另核了 `//go:build` 指令（真 `go/scanner` 会把它当注释而漏掉）与契约字面量。

## 每批要点

### 第 1 批 `tools/`
`equivcheck` 的 `A-037` 留原地、`A-038`（新侧清单现取、按 token 而非删空白比对）上移
`docs/invariants.md`；`wmcharprobe` 文件头拆成职责/用法/取焦点窗口/判据四块，操作性内容留在原地。
两处行尾注释变动：`@路径` 那条去重（信息在文件头用法块）、`case "direct"` 那条**纠错**
（原文写"换行/Tab 前 Esc 先行"，实现只在 `\n` 前发 Esc）。

### 第 2 批 `frontend`
`layout-probe` 37 行文件头拆成 9/9/7/7；`useUiScale` 21 行头块 → 6；`statusBarState` 的裸引用改成
冻结格式；`viewportReport`/`useTheme`/`useTypingTask` 的实测与历史改指针。改完按铁律 2 跑了
`scripts/build.ps1` 核对产物（见下）。

### 第 3 批 `internal/win32`
遵守 `docs/invariants.md`「其它不变量」的既有决定：**Win32 怪癖注释留在实现内**，A 类 28 条一条没搬走，
只做压缩与拆块；7 条新指针全部解析成功。核验确认一处**注释纠错**是对的：`initialWindowSize` 的
注释原写"workH 用不上"，而代码是 `_ = workW`（workH 参与计算）。

### 第 4 批 `internal/typing` + `cmd/type`
22 条 C 类重复压成 1–3 行 + 冻结指针（`任务槽与并发启动`/`焦点锁定与漂移防护`/`目标窗口与轮询`/
`文本直投`/`其它不变量`/`行为契约（冻结，改动需双端同步）`）；`main.go` 的四条红线（不响应
WM_DPICHANGED / 不放宽窗口样式 / `SetSize` 先于 `SetWindowClientRect` / lw-lh 是设计逻辑尺寸）
拆成 ≤10 行块、信息密度未降；`taskrun.go` 的"40 条用例"过时数字已删。

## 已知偏离（全程只此一类，信息均未丢）

台账去向写"上移 docs"、但目标文档**没有等价段落**的条目，按"不凭空上移"原则**留在代码里压缩保留**，
并在核验时逐条确认事实仍在：

| 台账 | 事实 | 现状 |
|---|---|---|
| `A-007` | 置顶状态必须读回（置顶不随页面重载复位，按钮与窗口会相反） | `main.go` 3 行 |
| `A-016` / `A-031` | 主题存储键两处、内联脚本先行、"判定只许一处" | 已补进 `docs/invariants.md`「其它不变量」，指针变真 |
| `A-032` | `iconSize` 两位小数与 `charCount` 码点口径 | `App.vue` 原地 |
| `T-002`/`T-022`/`T-025`/`T-032`/`T-042` | 快照 nil/空切片、终态判排在清标志之后、`Cancel` 旧实现等 2 秒、`clipboardKept` 零值陷阱、拆分史 | `internal/typing` 原地压缩 |
| `W-037` | 模态框挂住 CI 这一句 | `win32_instance.go` 原地 |

另有三处**因重写而变的注释口径**，均由核验确认：
① `wmcharprobe` 的 `case "direct"`（纠错）；② `win32_window.go` 的 `workH`→`workW`（纠错）；
③ `dpiForWindow` 删去"本机实测就是 100"——`docs/invariants.md` 同一主题写的是"本机实测 DPI 为 120"，
两处本就矛盾，删掉的是可疑值，留档口径仍以 docs 为准。

## 收尾待办（Lead）

1. 四批完成后把闸门基线全部下调为 0（已完成：`longCommentBudget` / `bareDocRefBudget`）。
2. 两处遗留裸引用改成冻结格式（`win32_webview2.go`、`internal/typing/contract_test.go`）——已完成。
3. 按铁律 2 跑 `scripts/build.ps1` 核对 `internal/web/dist/index.html` 是否漂移——见下方记录。
4. 台账、核验报告与本文件在本轮收尾后删除；`docs/comment-style.md` 保留（规范长期有效）。
