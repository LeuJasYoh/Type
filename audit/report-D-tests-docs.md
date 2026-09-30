# 审计 D：测试质量、重构安全网与文档同步

审计对象：`internal/typing/*_test.go`、`internal/win32/win32_test.go`、`AGENTS.md`、`README.md`、
`frontend/src/types.ts` 与 `frontend/src/ipc.ts`（只看与 Go 侧的契约面，不审 UI 样式）。
本次审计为只读，除本报告外未修改仓库任何文件（`git status --porcelain` 只多出未跟踪的 `audit/`）。

前提：用户即将对前端 UI 做一次大重构。因此本报告把"重构后会不会静默失配"放在第一位，
第 2 节给出可直接落地的测试文件名、断言内容与失配后果。

## 本次实测命令与结果

| 命令 | 结果 |
|---|---|
| `go test -count=1 -cover ./internal/...` | `internal/typing` **94.2% of statements**；`internal/win32` **44.9%**；`internal/web` no test files |
| `go test -count=1 -coverprofile=... ./internal/typing ./internal/win32` + `go tool cover -func` | typing 94.2%、win32 **43.7%**、两包合计 **63.0%**（win32 两次跑出 44.9%/43.7%，原因是两条用例可 `t.Skip`，见 1.3） |
| `go test -count=1 -race ./...` | 全部 ok（typing 3.7s、win32 1.7s），无 race 报告；`cmd/type`、`internal/web`、`tools/*` 无测试文件 |
| `go test -count=1 -v ./internal/typing` | 29 个 PASS（27 + 2 个纯函数用例），0 FAIL，0 SKIP |
| `go test -count=1 -v ./internal/win32` | 8 个 PASS，0 SKIP（本机剪贴板不忙；CI 上可能变 SKIP） |
| `cd frontend && npx tsc --noEmit` | **失败**：`src/main.ts(2,17): error TS2307: Cannot find module './App.vue'`（exit 2） |
| `cd frontend && npx vue-tsc -p tsconfig.json --noEmit` | 通过（exit 0）。项目真正的类型检查是 `vue-tsc`，见 `frontend/package.json` 的 build 脚本 |
| `gofmt -l ./cmd ./internal ./tools` | 输出为空（与 AGENTS.md:24 的承诺一致） |
| `go vet ./...` | 无输出（exit 0） |
| `git status --porcelain` | 只有未跟踪的 `audit/`（即本报告），无任何已跟踪文件被改动 |

## 结论摘要

1. **[阻塞] Go 与 TS 之间的四条契约（Bind 名、startTyping 参数序、TypingStatus JSON 键、phase 取值）没有任何自动守护。** 四处契约面全部靠手写与人工同步：`cmd/type/main.go:64-77` 的 Bind、`internal/typing/typing.go:84-90` 的 JSON tag、`typing.go:75-82` 的六个 phase、`frontend/src/types.ts:5-23` 的镜像。仓库里没有任何测试或工具断言过 JSON 键名（全仓 `json:` tag 只出现在 `typing.go`，`grep` 无测试引用），`cmd/type` 连一个测试文件都没有。前端重构改字段名的后果是静默的，详见第 2 节。
2. **[阻塞] typing 94.2% 的行覆盖率恰好漏掉了"启动衔接"的三个早退分支。** `typing.go:249-250`、`258-259`、`262-263` 的覆盖计数都是 0，而这三处都是"不写任何终态就直接 return"。`Start` 在 `typing.go:210-216` 同步写入 countdown 状态并返回 `"started"`（`typing.go:218`），所以一旦这些早退命中，前端会永久停在倒计时（countdown 不是终止态，`useTypingTask.ts:40` 不会停止轮询）。Lead 的 task-1 已确定性复现该路径下的真实缺陷，本次覆盖率数据独立指向同一处代码。高行覆盖率与关键路径被覆盖不是一回事。
3. **[阻塞] 平台层注入路径的自动覆盖为零。** `internal/win32` 里 `SendRune` / `SendText` / `SendEscape` / `SendEnter` / `SendPaste` / `sendInput` / `sendChar16` / `sendCharUnitsViaWMChar` / `sendVK` 全部 0.0%（命令见上表）。也就是说"注入到底成没成功"这条 AGENTS.md 反复强调的主线（`AGENTS.md:267-269`）没有一条自动用例，现有 8 条 win32 用例集中在剪贴板快照、UTF-16、单实例与图标索引上。
4. **[重要] `-race` 不在 CI，也不在文档里，且没有任何并发交错用例。** 全仓 grep `race` 为空（ci.yml / release.yml / AGENTS.md / README.md / build.ps1 都没有）。`go test -race` 今天在本机是绿的，但那是我手动跑出来的，不是 CI 保证的；而测试里也没有两个 goroutine 同时 Start/Cancel 的用例，`-race` 即便接进 CI 也咬不到东西。
5. **[重要] 冻结文案在测试里大量"常量自比"。** 多处断言写成 `st.Message != msgPasteRejected` 这种"用产出该文案的同名常量去比对"的形式（`typing_test.go:470`、`601`、`626`、`694`、`988`、`1055`、`1087`），改了常量文本测试照样绿；而发布语言里最常被误伤、且已经出过一次事故的倒计时文案 `剩余 N 秒 — 请聚焦目标窗口...`（`typing.go:212`、`288`）在 Go 侧没有任何逐字断言，它同时还有第三份拷贝在 `frontend/src/composables/useTypingTask.ts:75`。

---

# 第 1 节 测试覆盖真实数据与缺口

覆盖率数字命令：`go test -count=1 -cover ./internal/...`（typing 94.2% / win32 44.9% / web 无测试），
以及带 profile 的 `go test -count=1 -coverprofile=cover.out ./internal/typing ./internal/win32`
加 `go tool cover -func=cover.out`（合计 63.0%）。

## 1.1 断言强度：有测试但对不上要守的东西

**[重要] 条款级文案用同名常量比对，等于没断言。**
位置：`internal/typing/typing_test.go:470`（vs `msgPartialSendInput`）、`:601`（vs `msgPasteRejected`）、
`:626`（vs `msgClipboardNotRestored`）、`:694`（vs `msgClipboardFailed`）、`:988`（vs `msgTargetSwitchedTyped(2)`）、
`:1055`（vs `msgTargetSwitchedIdle`）、`:1087`（vs `msgTargetSwitchedPasted`）。
证据：常量定义在 `typing.go:94-118`，测试引用的是同一批标识符。把 `msgPasteRejected` 的文本改成
`"粘贴失败"`，`TestSendPasteRejected` 仍然通过。
影响：`AGENTS.md:152-162` 把这些字符串定义为冻结的发布语言，但回归网是空的；v1.5.1 的
"专用文案不可达"（`AGENTS.md:170-172`）和去 AI 味脚本误伤倒计时文案（`AGENTS.md:349-350`）
两次事故都属于这一类。
建议：在 `internal/typing/contract_test.go` 里加一张"文案清单表"，用字面量逐个核对，例如
`{name: "msgPasteRejected", got: msgPasteRejected, want: "粘贴未生效：目标窗口拒绝了模拟按键，可能其权限高于 Type"}`；
已有字面量断言的那几条（`typing_test.go:335`、`368`、`413`、`490`、`657`）保持不动。

**[重要] 倒计时文案在 Go 测试里一次都没被断言。**
位置：产出点 `typing.go:212`（Start 初态）、`typing.go:288`（每拍秒边界）。
证据：`TestAsciiTyping`（`typing_test.go:406-415`）只断言了 `SecondsLeft`、`TargetWindow`；
`TestCountdownTicksAndSecondBoundaries`（`typing_test.go:879-887`）同样只查 phase/秒数/标题；
全仓测试文件里 grep `剩余` 只命中一条注释（`typing_test.go:406`）。
影响：这是用户盯得最久的一句话，也是 `AGENTS.md:343-345` 记录的唯一超标标记清理事故现场。
建议：并入上一条的文案清单表；同时把 `typing.go:212` 与 `288` 的两处重复字面量提取成一个
`func countdownMsg(sec int) string`，让 TS 侧那份拷贝有对照物（见第 2 节）。

**[次要] 若干用例只做否定断言，不锁定期望值。**
位置：`typing_test.go:577`（只断言不是 `输入完成` 也不是 `输入失败`）、`:739`（只断言不是 `输入完成`）、
`:991`（只断言 `Progress == -1`）。
影响：`TestEmptyTextDoesNotReportSuccess` 没有锁定 `无内容可输入`，`TestSendInputRejectedDuringTyping`
没有锁定 `输入中断：目标窗口拒绝了模拟按键…`。前者正好是 `AGENTS.md:156` 里"允许新增"的文案之一。
建议：把否定断言补成等值断言。

**[次要] `Start` / `Cancel` 的返回值从未被断言。**
位置：`typing.go:218` 返回 `"started"`、`typing.go:231` 返回 `"cancelled"`；
测试里 27 处调用全是 `if _, err := svc.Start(...)`。
影响：这两个字符串经 webview 序列化后到前端是 `Promise<string>`（`frontend/src/ipc.ts:9-11`），
目前前端丢弃不用，属于"没人守也没人用"的接口面。重构时若把它改成有意义的值（比如错误码），
没有测试会拦住。
建议：要么在契约测试里锁死，要么把它降级成不返回值的接口，别留中间状态。

## 1.2 未覆盖代码块逐条清单

数据来源：`go test -count=1 -coverprofile=cover.out ./internal/typing ./internal/win32` 后，
从 `cover.out` 里筛出计数为 0 的块（`typing.go` 全部 13 条如下）。

| 位置 | 代码 | 属于哪条路径 |
|---|---|---|
| `typing.go:249-250` | 等待上一任务让出 `runningFlag` 时发现已被新操作接管，直接 return | 启动衔接（不写终态） |
| `typing.go:258-259` | `CompareAndSwap(false,true)` 失败，直接 return | 启动衔接 |
| `typing.go:262-263` | 认领成功后再次比对代数不符，直接 return | 启动衔接 |
| `typing.go:297-298` | 倒计时循环结束后取消检查 | 倒计时末尾取消 |
| `typing.go:356` | 逐字符循环内的 `break` | **键入中途取消** |
| `typing.go:400-404` | `r > 127` 时的 `isCJKPunct` 分流与两档间隔 | 非 ASCII 逐字符路径 |
| `typing.go:409-410` | 循环结束后 `if cancelled() { success = false }` | **键入中途取消** |
| `typing.go:439-440` | `msg = "输入失败"` 兜底 | 通用兜底 |
| `typing.go:469-470` | `typeTextViaClipboard` 入口处的漂移守卫（还没碰剪贴板） | 剪贴板路径漂移 |
| `typing.go:479-480` | 写入剪贴板后、粘贴前检测到取消，恢复快照后返回 | **剪贴板中途取消** |

**[重要] 取消路径只覆盖了"倒计时中取消"，注入中途与剪贴板中途的取消全未覆盖。**
证据：`TestCancelDuringCountdown`（`typing_test.go:349-374`）、
`TestCancelDuringCountdownDetectedWithinOneTick`（`:936-963`）、
`TestRestartAfterCancelSupersedesOldTask`（`:509-537`）都发生在倒计时阶段；
`typing.go:356`、`409-410`、`479-480` 三块计数为 0 是这三条路径从未被执行的直接证据。
影响：取消是用户点得最随意的按钮，也是唯一会与注入、剪贴板恢复同时发生的操作；
`typeTextViaClipboard` 在取消时返回空 `failMsg`（`typing.go:479`）依赖调用侧 `cancelled()`
兜住（`typing.go:425`），这条依赖没有任何测试。
建议：用 `blockSleep` 把任务停在字符间隔和 `clipboardSettleWait` 上，再调 `Cancel()`
后放行，断言终态是 `cancel`、注入序列停在对应位置、剪贴板已恢复。

**[重要] 非 ASCII 逐字符路径（`forceSendInput=true` 或 `textDirect=true` 配中文）没有用例。**
证据：`typing.go:399-405` 的三块分支全 0。现有逐字符用例的输入全是 ASCII
（`typing_test.go:388`、`424`、`443`、`462`、`570`、`981`、`1023`），含中文的用例都走剪贴板。
`isCJKPunct` 本身有纯函数用例（`helpers_test.go:28-49`），但它与状态机的接线断了。
影响：README:85 承诺"要让中文也走文本层，需同时勾选绕过粘贴检测"，这条组合路径没有任何自动验证；
字符间隔三档（`typing.go:146-148`）实际只有 ASCII 档被走过。
建议：加一条 `Start("中，a", 1, true, false)` 与一条 `Start("中，a", 1, false, true)`，
断言注入序列与所用的等待时长（可让假 sleep 记录 `time.Duration` 来锁档位）。

**[次要] `输入失败` 兜底疑似不可达。**
位置：`typing.go:437-440`。
证据：该块的覆盖计数为 0。把所有能走到 `default`（`typing.go:435`）的分支读一遍：
剪贴板路径的每条失败返回都带非空 `failMsg`（`typing.go:469`、`474`、`496`、`499`），
唯一返回空串的一处在取消路径（`typing.go:479`），而取消会被 `typing.go:425` 的
`case cancelled()` 提前接走；逐字符路径的 `failMsg` 在 `typing.go:412`、`416` 赋值。
结论是当前没有输入能让 `msg` 为空。**待确认**：这可能是刻意的防御性兜底。
影响：`AGENTS.md:153` 把 `输入失败` 列在冻结清单里，实际上它今天不可达；
真正的"通用失败"如果哪天出现，也没人知道这条文案还对不对。
建议：补一条直接构造场景的测试（例如用一个返回 false 的注入器 + 空文本组合去逼近），
或者把兜底改成 `msg = failMsg + "（未分类）"` 之类的可观测形式。

**[次要] 剪贴板入口处的漂移守卫（`typing.go:468-470`）不可测。**
证据：从锁定采样（`typing.go:305`）到 `typeTextViaClipboard` 的第一条语句之间没有 sleep 注入点，
测试无法在这段里插入切窗。
影响：这条守卫在真机上有效（存在毫秒级真实间隙），但在测试里永远是死代码，
将来有人"顺手删掉"不会变红。
建议：把 `targetHeld` 的调用点通过 `s.sleep` 之前的一次采样暴露出来，
或者接受它不可测并在注释里注明（现在是注释没写、覆盖率为 0，容易被误读成遗漏）。

## 1.3 平台层（internal/win32）覆盖结构

**[重要] 注入层零覆盖。**
命令与产物：`go tool cover -func=cover.out` 给出
`win32_keyboard.go` 的 `SendRune`(61)、`SendText`(81)、`SendEscape`(85)、`SendEnter`(87)、
`SendPaste`(90)、`sendInput`(103)、`sendChar16`(116)、`sendCharUnitsViaWMChar`(144)、`sendVK`(168)
全部 0.0%；
`win32_window.go` 的 `ScaledForDPI`(31)、`SetTopmost`(50)、`RetrySetIcon`(151)、`focusedHWND`(181)、
`Foreground.Sample`(207) 与 `win32_webview2.go`、`win32_msgbox.go` 全部 0.0%。
未被覆盖的还有 `win32_clipboard.go` 的 `clipboardClear`(173)。
影响：`AGENTS.md:265-266` 声明 Win32 怪癖注释是"本代码库最有价值的文档"，
其中"全角标点 WM_CHAR 绕行"就在 `win32_keyboard.go`，而这段代码一条用例都没有。
`AGENTS.md:297-299` 已经定下"新增同类 Win32 常量照 `TestApplyWindowIconClassIndex` 补实测用例"
的规矩，注入层应该照办。
建议（按性价比排序）：① 给 `Foreground.Sample` 补一条真实建窗 + `SetForegroundWindow` 的用例
（`win32_test.go` 已有 `newTestWindow` 脚手架可直接复用）；② 给 `ScaledForDPI` 补一条
对已知显示器取值的合理性断言；③ `SendRune` 的全角标点分支需要真实目标窗口，
可考虑把"选路逻辑"从"真发按键"里拆出来（纯函数选路可测，真发按键留给 tools/wmcharprobe 手工验证）。

**[次要] 两条剪贴板用例会静默跳过。**
位置：`win32_test.go:51`（`t.Skip("剪贴板被占用, 无法保存原始状态")`）、`win32_test.go:139`（同款）。
证据：这两条是 win32 包覆盖率波动的原因（两次运行 44.9% 与 43.7%）。
影响：CI 上若剪贴板被占，用例静默跳过、构建仍然全绿，覆盖率悄悄下降；
而这两条恰好是唯一验证"真剪贴板全格式快照"的用例。
建议：跳过时至少 `t.Log` 出原因，或在 CI 里把"跳过数"当失败看；
若担心真实剪贴板干扰，可考虑在用例开头记录并断言恢复结果（现在是 `defer` 恢复但不校验）。

## 1.4 假时钟与并发

**[次要] `sleep` 注入点覆盖完整，但"取消衔接"刻意走真实时钟，带来一条 2 秒的用例。**
位置：`typing.go:247-256`（`time.Now()` + `time.Sleep(yieldPollInterval)`），
注释在 `typing.go:173-175` 说明是刻意的。
证据：`TestStartDeadlineWhenPreviousTaskStuck`（`typing_test.go:640-681`）耗时 2.03s（verbose 输出）。
影响：这条用例占整个 typing 包测试时间的一半以上（包总耗时 2.5s），
且它是唯一依赖真实时钟走满 2 秒的用例；CI 上没问题，本地快速迭代时会烦。
建议：保留行为，但把 `yieldDeadline` / `yieldPollInterval` 做成可注入字段（与 `sleep` 同款），
错误文案与分支不变，用例就能瞬间走完。这是纯测试性重构，不改变生产行为。

**[次要] 两处 `time.Sleep(20ms)` 是仅有的竞态式断言。**
位置：`typing_test.go:676`（"给过代任务的收尾留出窗口"）、`typing_test.go:1002`（"终态之后不得再补注入"）。
影响：慢机器或高负载下这两条可能偶发失败；`676` 那条的断言对象还是"过代任务不得覆盖状态"，
而 `setStatus` 的代数守卫（`typing.go:239-243`）本身是确定性的，用睡眠去等它属于不必要的赌。
建议：改成通道同步（任务退出时 close 一个 done channel）或轮询到稳定，而不是固定睡眠。

**[重要] 无并发交错用例，且 `-race` 不在 CI。**
证据：全仓 grep `race` 为空；`typing_test.go` 里没有任何 `go func` 驱动的并发 Start/Cancel；
`useTypingTask.ts` 的轮询与 `Start` 之间是"先 await 再开轮询"的约定（`typing.go:204-207` 注释）。
影响：这个包的设计核心是"原子标志 + 任务代数"（`typing.go:168-171`），
正确性完全建立在交错顺序上，而测试全是"一条 goroutine 走到底 + 主 goroutine 轮询状态"。
第 1.2 节那三块 0 覆盖的早退分支正是并发交错点。
建议：① `ci.yml` 的 verify 作业加一步 `go test -race -count=1 ./...`（Windows runner 支持，
本机实测 3.7s，成本可接受）；② 同步写进 `AGENTS.md:23-26` 的"Go 验证三件套"，
现在那里只有 gofmt / vet / test，与"与 CI 同款"的说法会在加了 -race 之后失真；
③ 补一条 `TestConcurrentCancelAndRestart`（一个 goroutine 反复 Start/Cancel，主 goroutine 收终态），
让 `-race` 有东西可咬。

---

# 第 2 节 前端重构契约安全网（重点）

## 2.0 契约现状逐项核对

结论：**四项全部对得上，但全部靠人工维持，零自动守护。**

| 契约项 | Go 侧 | TS 侧 | 是否一致 | 守护者 |
|---|---|---|---|---|
| Bind 函数名 | `cmd/type/main.go:64` `startTyping`、`:65` `cancelTyping`、`:67` `toggleTopmost`、`:77` `getTypingStatus` | `frontend/src/ipc.ts:9`、`11`、`13`、`15` 的 `Window` 声明 | 一致 | 无（`cmd/type` 无测试文件） |
| `startTyping` 参数 | `typing.go:199` `(text string, delay int, forceSendInput bool, textDirect bool)` | `ipc.ts:9`、`26-27` 同序 4 参 | 顺序与类型一致，**名字不一致**（TS 叫 `forceRaw`） | 无 |
| `TypingStatus` JSON 键 | `typing.go:85-89` `phase`、`message`、`progress`、`secondsLeft`、`targetWindow`（无 `omitempty`，字段恒存在） | `types.ts:15-22` 同 5 个字段 | 逐字段一致 | 无 |
| phase 取值 | `typing.go:76-81` `idle`/`countdown`/`typing`/`success`/`error`/`cancel` | `types.ts:5-11` 同 6 个字符串字面量 | 逐值一致 | 无 |

机理补充（为什么"没有守护"就等于"静默"）：`go-webview2` 的调用分发在
模块缓存里的 `webview.go`（`github.com/jchv/go-webview2@v0.0.0-20260205173254-56598839c808`，
不在本仓库内）第 170-188 行，参数按 `d.Params[i]` 与 `v.Type().In(i)` **按位置**反序列化，
返回值在第 151 行走 `json.Marshal`。也就是说函数名、参数顺序、JSON 键名三者
都是**运行期字符串/位置约定**，Go 编译器和 TypeScript 编译器都不会看一眼：
`tsc` / `vue-tsc` 校验的是 `ipc.ts` 里手写的那份声明（它自己就是镜像，不是来源），
`go test` 校验的是 Go 内部行为，两边都不引用对方。

另外一个容易忽略的细节：同一个文件第 164-168 行在找不到绑定方法时返回 `nil, nil`（不报错），
所以在 JS 侧"调用一个没绑定的名字"表现为 `window.xxx is not a function`，
普通 `Error`，会被 `useTypingTask.ts:46-48` 的空 `catch` 吃掉。

**[吹毛求疵] 四处 `w.Bind` 的返回值都被丢弃。**
位置：`cmd/type/main.go:64`、`65`、`67`、`77`。
证据：`Bind` 的签名返回 `error`（模块缓存里的 `webview.go:450`），只有"传入的不是函数"
或"返回值多于两个"才会失败，当前的四个被绑定项都不满足，所以现实里不会触发。
影响：低。但绑定失败与绑定成功在代码上长得一样，属于"静默失败"家族的一员。
建议：`if err := w.Bind(...); err != nil { ... }` 打个日志或直接退出，成本一行。

## 2.1 不加安全网会怎样静默失配（逐场景）

| 重构动作 | 当场报错？ | 后果 |
|---|---|---|
| 改 Go 的 JSON tag，如 `targetWindow` → `targetWin` | 否 | Go 测试全绿；`vue-tsc` 全绿；运行时 `status.targetWindow` 恒为 `undefined`，`App.vue:130` 的预览永远显示 `—`，`App.vue:134` 传给状态栏的 `message` 正常、但 `phase`/`progress` 若同批改了就一起失效。全程无任何日志 |
| 改 `types.ts` 的字段名（后端不动） | 否 | 同上，只是方向相反。`useTypingTask.ts:14-15` 的默认值会把 `undefined` 悄悄补上，症状更隐蔽 |
| 改 Bind 名，如 `getTypingStatus` → `getStatus`（任一侧） | 否 | `window.getTypingStatus` 为 `undefined`，`tick()` 抛错被 `catch {}` 吞掉（`useTypingTask.ts:46-48`），轮询链条不再推进。界面停在 `useTypingTask.ts:73-78` 本地写入的倒计时状态，后端其实在正常注入。这一条与 `AGENTS.md:186-190` 记录的 v1.4.0 事故症状一模一样，但成因不同，排障时极易被误判成老病复发 |
| **交换两个同为 bool 的参数**（`forceSendInput` ↔ `textDirect`），任一侧 | 否 | 最坏的一种：`json.Unmarshal` 对两个 bool 都接受，不报任何错，行为静默互换（勾"绕过粘贴检测"变成"文本直投"） |
| 参数个数/类型不匹配（如删掉一个参数） | 是，但信息难看 | 同一个模块缓存文件第 173-175 行返回英文 `"function arguments mismatch"`，经 `ipc.ts:20-24` 的 `errMsg` 原样显示在状态栏。属于"响但难懂"，优先级低于上面三条 |
| 新增一个 phase（如 `paused`），TS 不跟进 | 否 | `useTypingTask.ts:8-11` 的 `isTerminal` 判否，轮询永不停止（10 次/秒的空转），`StatusBar.vue:12` 的 `barClass` 拿到未知 class |
| 改 phase 取值，如 `cancel` → `cancelled` | 否 | `isTerminal` 永远为假，取消后轮询不停；`syncFromBackend`（`useTypingTask.ts:118`）只认 `countdown`/`typing`，页面重载后接不回任务，README:31 的"自动接回正在进行的任务"承诺失效 |
| 删掉 `secondsLeft`（认为是死字段） | 否 | 它今天确实没有被任何界面读取（`App.vue`、`StatusBar.vue` 都不用它，只有 `useTypingTask.ts:14`、`77` 写入），所以删了看不出问题；但它属于发布契约的一部分，删之前应该先决定是留还是正式废弃 |

## 2.2 建议补的测试（可执行清单）

原则：先用今天已有的工具链（`go test`）把跨语言契约钉住，不引入新依赖；
前端测试设施可以后补，因为 CI 现在跑 `go test ./...` 就能覆盖 Go 侧的全部断言。

### 文件一：`internal/typing/contract_test.go`（新文件，无构建约束，任何系统都能跑）

只锁 Go 侧，不读前端文件，符合 `AGENTS.md:73-74` 对业务层的定位。

1. `TestTypingStatusJSONShape`
   构造一个字段全非零的 `TypingStatus{Phase: PhaseCountdown, Message: "剩余 3 秒 — 请聚焦目标窗口...", Progress: -1, SecondsLeft: 3, TargetWindow: "记事本"}`，
   `json.Marshal` 后与字面量 JSON 字符串逐字节比对（键名与顺序都要对，顺序由结构体字段序决定，顺带锁住 `omitempty` 不被误加）。
   再 `json.Unmarshal` 到 `map[string]any`，断言键集合恰好是
   `{phase, message, progress, secondsLeft, targetWindow}`。
   断言不到的后果：JSON tag 被改名，全仓没有第二处会红。
2. `TestPhaseLiterals`
   断言六个常量值逐字面量等于 `"idle"`、`"countdown"`、`"typing"`、`"success"`、`"error"`、`"cancel"`，
   并断言 `len([]TypingPhase{...})` 与显式列表长度一致（防将来新增 phase 时忘了同步这个测试）。
3. `TestFrozenMessages`
   上一节 1.1 提到的文案清单表，含 `typing.go:97`、`99`、`101`、`104`、`107`、`111`、`202`、`229`、`252`、`309`、`329`、`347`、`391`、`413`、`431`、`434` 以及
   倒计时文案（建议同时把 `typing.go:212`、`288` 的重复字面量提成 `countdownMsg(sec)` 后核对它）。
4. `TestInjectorInterfaceIsFiveBoolMethods`
   用 `reflect` 断言 `TextInjector` 恰好 5 个方法且都返回 `bool`。
   这条对应 `AGENTS.md:267-269` 的"不要把返回值改成 void"，那是静默失败的源头。

### 文件二：`cmd/type/contract_test.go`（新文件，`//go:build windows && (amd64 || arm64)`）

装配层是契约的落点，跨语言检查放这里；`cmd/type` 目前没有测试文件，加了这个文件后 `go test ./...` 就会带上它。

1. `TestBindNames`：读同目录的 `main.go` 源码文本，用正则取出全部 `w.Bind("名字"` 的名字，
   断言集合恰好等于 `{startTyping, cancelTyping, toggleTopmost, getTypingStatus}`（多一个少一个都失败）。
   再读 `../../frontend/src/ipc.ts`，断言四个名字都能在 `Window` 声明里找到（例如按 `\b名字\s*\(` 匹配）。
   两者任一改名即红，报错信息里直接写出缺的那个名字。
2. `TestStartTypingSignature`：不需要启动 WebView2。
   `svc := typing.NewTypingService(nil, nil, nil)`（构造函数只存接口与写一次 idle 状态，不会解引用 nil），
   然后对 `reflect.TypeOf(svc.Start)` 断言 `NumIn()==4`、`In(0)==string`、`In(1)==int`、
   `In(2)==bool`、`In(3)==bool`、`NumOut()==2` 且 `Out(1)==error`。
   同法断言 `Cancel` 无参、`Status` 返回单个 `*typing.TypingStatus`。
   这一条专门拦"交换两个 bool 参数"和"增删参数"，是第 2.1 节里唯一会静默的一类。
3. `TestTypeScriptMirrorFields`：读 `../../frontend/src/types.ts`，对 5 个 JSON 键名与 6 个 phase 字面量
   逐个断言出现在文件里；`types.ts` 里任一改名即红。
   说明：把跨语言检查放在装配层是有意的，业务层不该知道前端目录结构；
   代价是这个用例在 `frontend/` 被挪走时会报"文件不存在"，那正是想要的信号（而不是静默跳过）。

### 文件三（可选，成本更高）：前端测试设施

现状：`frontend/package.json` 没有 test 脚本，也没有 vitest/jest（已核对）。
所以前端今天没有任何自动测试，"UI 大重构"这四个字目前对应的是零回归网。
建议分两步：

1. **零成本先做**：给 `package.json` 加 `"typecheck": "vue-tsc -p tsconfig.json --noEmit"`，
   并在 `AGENTS.md`「常用命令」与 README「自行编译」里写明前端检查用这条。
   证据：本次实测 `npx tsc --noEmit` 直接失败在 `src/main.ts(2,17): error TS2307: Cannot find module './App.vue'`，
   因为裸 `tsc` 不认识 `.vue`（`tsconfig.json` 的 `include` 含 `src/**/*.vue`），
   而项目实际用的是 `vue-tsc`（`package.json` 的 build 脚本）。命令写错会让人以为前端是坏的。
2. **真正需要的那条**：引入 vitest + `@vue/test-utils`（或更轻的纯函数测试），
   加 `frontend/src/contract.test.ts`，桩掉四个 `window` 绑定后断言：
   - `ipc.startTyping('t', 3, true, false)` 恰好以 `('t', 3, true, false)` 四个位置参数调用
     `window.startTyping`（顺序断言，拦"交换两个 bool"）；
   - `useTypingTask.start()` 之后 `status.secondsLeft === delay` 且 `status.phase === 'countdown'`；
   - 六种 phase 的 `isTerminal` 真值表，含一个未知 phase 必须为假；
   - 状态对象用一个与第 2.2 节文件一里同样的 JSON 字面量构造，喂给渲染层，
     断言倒计时文案被原样显示（把"冻结文案"这条链从 Go 一直贯到界面）。

### 文件四（备选方案）：共享 fixture 作为单一来源

如果希望"键名只有一处定义"，可以在 `frontend/src/__fixtures__/typing-status.json` 放一份
Go 产出的样例 JSON，让 `internal/typing` 的 marshal 结果与它比对、让 TS 侧导入它构造桩数据。
代价是 TS 需要开 `resolveJsonModule`（现在 `tsconfig.json` 没开），
收益是"字段名"从"两处手写"变成"一处 fixture + 两侧引用"。
取舍建议：先落文件一、文件二（纯检查、零依赖、只加一个测试文件），
fixture 方案等前端真的加测试设施时一起做。

## 2.3 重构期间的顺序建议

1. 先加 `internal/typing/contract_test.go` 与 `cmd/type/contract_test.go`，跑一次确认全绿（此时契约是对的，测试应当一次通过）。
2. 再删掉一处镜像做验证：例如把 `types.ts` 的 `targetWindow` 改个错名，确认 `cmd/type` 的用例变红，然后改回来。这一步是给安全网做的安全网，请务必做，否则无法区分"测试通过"与"测试没生效"。
3. 再动前端 UI。改动期间 `go test ./...` 就是契约门禁，不需要等前端构建。
4. 前端重构完成后：`npx vue-tsc -p tsconfig.json --noEmit` 加 `scripts/build.ps1`，
   并按 `AGENTS.md:9` 提交重新生成的 `internal/web/dist/index.html`（CI 的漂移检查会拦）。

---

# 第 3 节 文档同步断链

抽查范围：AGENTS.md 与 README.md 里可验证的断言（命令、文件路径、行数内容、常量数值、CI 步骤、已知限制）与代码实际状态。

## 3.1 已失效或表述错误的

**[次要] `types.ts` 把镜像来源指向了错误的文件。**
位置：`frontend/src/types.ts:2`：「main.go 中 TypingPhase / TypingStatus 的镜像, 修改须双端同步」。
证据：`cmd/type/main.go` 只有 88 行，里面没有任何类型定义；
`TypingPhase` 在 `internal/typing/typing.go:73-82`、`TypingStatus` 在 `typing.go:84-90`。
v1.5.5 的分层拆包（提交 `25e9271`）把它们从 main.go 移到了业务层，这行注释没跟上。
影响：这行注释就在契约面的第一屏，重构的人按它去 main.go 找"来源"会扑空；
而 `AGENTS.md:150` 与 README:140 的说法是笼统的"Go 端"，没有错但也没纠正这里。
建议：改成 `internal/typing/typing.go:84-90`，并顺手写清"以 Go 的 json tag 为准"。
（lead 提示的另一条：`frontend/src/ipc.ts:2` 说"Go 端通过 webview.Bind 注入 window 的全局函数"，
这句仍然成立，`w.Bind` 确实在 `cmd/type/main.go:64-77`。**不构成漂移，无需改**。）

**[次要] `startTyping` 第 3 个参数在 TS 侧叫 `forceRaw`，与契约文档不一致。**
位置：`frontend/src/ipc.ts:8`、`:9`、`:26-27`，`frontend/src/composables/useTypingTask.ts:63`、`:83`，
`frontend/src/App.vue:14`（`const forceRaw = ref(false)`）与 `:82`-`89`（`chkForceRaw`）。
对照：`AGENTS.md:149` 写 `（startTyping 参数：text, delay, forceSendInput, textDirect）`，
Go 侧 `typing.go:199` 也是 `forceSendInput`。
证据：全仓 grep `forceRaw` 只命中前端 8 处，Go 侧 0 处；`forceSendInput` 只命中 Go 与 AGENTS.md。
影响：按位置传参，今天不会出错（已核对顺序 `text, delay, forceRaw, textDirect` 与 Go 一致）。
但同一个概念在仓库里有三个名字：界面文案叫"绕过粘贴检测"（`App.vue:92`）、TS 叫 `forceRaw`、
Go 与文档叫 `forceSendInput`。这是"契约没有单一来源"的直接证据：
一旦有人按名字去核对参数，或者重构时把参数按名字重排，就会踩到第 2.1 节里"两个 bool 交换"的坑。
建议：把 TS 侧统一改成 `forceSendInput`（纯改名，零行为变化，`vue-tsc` 会兜住漏改的地方），
`App.vue` 的 `ref` 名与元素 id 可保留 `forceRaw` 以免动 DOM 契约，但要在注释里点明二者同义。

**[次要] README 手工构建步骤里的版本号是硬编码的，且没有检查守护。**
位置：`README.md:195`（`go run ./tools/mkres -version 1.5.5 ...`）与 `README.md:200`
（`go run ./tools/pecheck -exe Type.exe -version 1.5.5 -arch amd64`）。
证据：当前 `cmd/type/main.go:18` 是 `1.5.5`，`frontend/package.json` 是 `1.5.5`，所以今天是对的。
CI 只校验 `package.json` 与 main.go 一致（`ci.yml:86-95`），release.yml 校验标签与 main.go、
发布说明、package.json（`AGENTS.md:56`），**没有任何一步看 README**。
历史证据：提交 `25e9271`（分层拆包）把 README 的这两处写成了 1.5.5，而当时的
`cmd/type/main.go` 还是 `1.5.4`（`git show 25e9271:cmd/type/main.go` 可查）；版本真正涨到 1.5.5
是下一个提交 `19b22d6`。也就是说 README 曾先于代码指向 1.5.5，两个提交之间 README 的手工步骤
与 main.go 的版本是不一致的，而这种情况没有任何检查会报。
影响：每个补丁版本都必须记得回来改这两行，漏了就留下一条跑不通的手工步骤。
建议：把命令写成 `-version <版本>`（与 `AGENTS.md:30` 的写法一致，那里就是占位符），
或让 README 明确指向 build.ps1；成本最低的是照 AGENTS.md 用占位符。

**[吹毛求疵] `AGENTS.md` 里同一个数字类的表述有轻微版本滞后。**
位置：`AGENTS.md:51` 的示例 `git tag v1.5.4 && git push origin v1.5.4`，当前版本已是 1.5.5。
影响：示例而已，不影响执行，但它是"复制粘贴即用"的地方，写成 `v1.5.x` 或 `<新版本>` 更稳。

**[吹毛求疵] README 的 CI 描述漏了 arm64 的两步。**
位置：`README.md:99`（CI 行）只写了"另按 amd64/arm64 矩阵做发布构建，并用 tools/pecheck 读回校验"。
对照：`ci.yml:142-148` 在 arm64 上还做 `go vet ./...` 与 `go test -c`（编译不运行），
`AGENTS.md:113-116` 写全了这两步。README 是面向用户的概览，可以接受，
但"测试真跑只在 verify 作业"这个事实（`ci.yml:139-141` 的注释）值得在 README 提一句，
否则读者会以为两种架构都跑了测试。

## 3.2 抽查通过、无需改动的断言

逐条列出，供其他审计员复用（这些我实测过，不是照抄）：

- `AGENTS.md:24-26` gofmt / vet / test 三件套：`gofmt -l ./cmd ./internal ./tools` 输出为空、`go vet ./...` 干净、`go test -count=1 ./...` 全绿，与描述一致。
- `AGENTS.md:78`「业务层不得反向依赖本包（当前无此依赖）」：`internal/typing/typing.go:7-12` 只引入 `fmt`/`strings`/`sync/atomic`/`time`，没有 win32；而 `internal/win32/win32.go:56-60`（`var _ typing.TextInjector = Injector{}` 等三行）使 win32 → typing 成为既有依赖，因此反向引用必然构成 import cycle，注释里"编译器会拒绝"的说法成立。
- `AGENTS.md:74`「internal/typing 刻意不带构建约束」：`internal/typing` 下无 `//go:build`（已 grep 全部 .go）。
- `AGENTS.md:300-304`「产品仅支持 `windows && (amd64 || arm64)`，约束落在 cmd/type 与 internal/win32 上」：`cmd/type/main.go:1`、`internal/win32/` 全部 8 个文件（含 `win32_test.go:1`）都是 `//go:build windows && (amd64 || arm64)`；`tools/wmcharprobe/main.go:1` 只有 `windows`（工具，符合预期）。
- `AGENTS.md:305-307`「Node 主版本只写在 `.node-version` 一处（现为 24）」：文件内容就是 `24`，`ci.yml:22-29` 用 `node-version-file: .node-version` 读取。README:179 另有一处散文重述"Node.js 24"，属于文档拷贝而非构建来源（见 3.1 同类问题）。
- `AGENTS.md:286-288`「恢复与读写的重试档位是分开的（4×50ms 对 8×100ms）」：`internal/win32/win32_clipboard.go:49-54` 是 `clipboardAttempts=4` / `clipboardWait=50ms` / `clipboardRestoreTries=8` / `clipboardRestoreWait=100ms`，`openClipboardWithRetry`(57) 与 `openClipboardRestoreRetry`(60-62) 分开调用，逐字对得上。
- `AGENTS.md:152-162` 冻结/新增文案清单：逐条在 `typing.go` 里找到对应字面量（`202`、`229`、`252`、`309`、`329`、`347`、`391`、`413`、`431`、`434`、`97`、`99`、`101`、`104`、`107`、`111`、`117`），文本逐字一致，包含全角标点与破折号。唯一例外是 `输入失败` 的可达性（见 1.2）。
- `AGENTS.md:163-167` 启动提示框文案：`internal/win32/win32_webview2.go:25`（`Type 无法启动`）、`:28`（`缺少 Microsoft Edge WebView2 运行时…`）、`:32`（`WebView2 初始化失败…`），与文档一致；`AGENTS.md:276-280` 的 `WEBVIEW2_BROWSER_EXECUTABLE_FOLDER` 复现手法与 `win32_webview2.go` 的预检实现不矛盾（该行为本身需要真机执行，本次未验，**待确认**）。
- `AGENTS.md:88-100` README 配图复现方式「1080×900，2 倍像素密度」：`--window-size=540,450` + `--force-device-scale-factor=2` 正好 1080×900。
- `AGENTS.md:39` `uv run scripts/gen_icon.py`：`scripts/gen_icon.py` 存在；`AGENTS.md:140`「pyproject 锁定 pillow==12.3.0」与 `pyproject.toml` 的 `dependencies = ["pillow==12.3.0"]` 一致。
- `AGENTS.md:42` equivcheck 用法：`tools/equivcheck/main.go:154` 的 usage 为 `<旧rev> <新rev> [--old-file <路径>] [--renames <路径>] [--renamed]`，文档漏了可选的 `--renames`（默认 `renames.json`），不算错。
- `AGENTS.md:102`、`README.md:161` `testdata/` 两个手工测试页：`testdata/paste-guard.html`、`testdata/completion-guard.html` 都存在。
- `README.md:95-103` 技术栈表：Go 1.26 与 `go.mod` 的 `go 1.26.5` 一致；`vue-tsc 类型检查 + Vite（vite-plugin-singlefile）` 与 `frontend/package.json` 的 build 脚本、`frontend/vite.config.ts:5` 的 `viteSingleFile()` 一致；Win32 API 行提到的 `EnumClipboardFormats`（`win32.go:30`、`win32_clipboard.go:221`）与 `RtlMoveMemory`（`win32.go:35`、`win32_clipboard.go:106` 等 5 处）都存在。
- `README.md:108-170` 项目结构树：逐个核对，`cmd/type/{main,devserver_prod,devserver_dev}.go`、`internal/web/{web.go,dist/index.html}`（dist 已在库，81842 字节）、`frontend/` 下 9 个源文件（含 `components/StatusBar.vue`、`composables/{useTypingTask,useTheme}.ts`）、`assets/` 4 个文件、`scripts/` 2 个文件、`tools/` 5 个子目录、`release-notes/`、`.node-version`、`pyproject.toml`/`uv.lock`/`.python-version` 全部存在。
- `README.md:31`「界面若被意外重载…会自动接回正在进行的任务」：对应 `useTypingTask.ts:109-126` 的 `syncFromBackend`，实现与承诺一致（前提是 phase 取值不变，见 2.1）。
- `README.md:77` 剪贴板跳过格式清单：与 `internal/win32/win32_clipboard.go` 的 `skippableFormat` 及 `win32_test.go:178-223` 的用例集合一致。
- `README.md:84` PerMonitorV2：`tools/mkres/main.go:149` 是 `DPIAwareness: winres.DPIPerMonitorV2`。

## 3.3 制度层面的观察

`AGENTS.md:310-325` 的「提交前：检查文档同步」表本身是好的，但它全靠人执行，没有任何自动检查。
本次抽查发现的两处失效（3.1 的前两条）都落在表里已覆盖的行：
"命令 / 构建流程 / 目录结构"与"非显而易见的新约定"。
建议在这张表里补一行"跨端契约（Bind 名 / JSON 键 / phase 取值）"，
指向第 2 节新增的契约测试；文档同步的问题最终还是要靠测试来兜，
因为人一定会忘，而 CI 不会。

---

# 第 4 节 可维护性建议

## 4.1 文件组织

**[次要] `typing_test.go` 单文件 1098 行、27 条用例，fakes、辅助函数、用例混在一个文件里。**
位置：`internal/typing/typing_test.go`（fake 区 1-210 行、测试辅助 211-256 行、用例 257-1098 行）；
对照组 `helpers_test.go` 只有 49 行 2 条纯函数用例，拆分风格已经存在。
证据：唯一的分节标记是 `typing_test.go:837` 的「焦点漂移防护」注释。
影响：前端重构期间如果有人要补契约测试，最自然的落点会被 1100 行的大文件劝退；
fakes（约 200 行）与用例混在一起，改动 fake 的行为会波及全部用例，review 时难以判断影响面。
建议：按"文件即分类"拆成 `fakes_test.go`（fake 三件套 + 常量 + `unitsOf`，1-210 行原样搬）、
`helpers_test.go`（保留并接收 `waitRunning`/`waitIdle`/`waitTerminal`/`isTerminal`/`noSleep`/`blockSleep`/`newTestService`/`seedSnapshot`）、
`contract_test.go`（第 2 节新增）、以及若干按主题的用例文件。
Go 不允许跨包共享测试辅助，所以同包内随意拆即可；拆分是纯搬运，可用 `tools/equivcheck` 之外的
普通 `go test` 验证（测试文件不参与产品等价性，逐函数比对工具对 `_test.go` 无意义）。

## 4.2 脚手架重复

**[次要] "数 sleep 次数"的写法在 5 条用例里重复，且每次都要手算节拍索引。**
位置：`typing_test.go:796-811`（`switch sleeps` 分段切窗）、`:849-854`（数拍）、
`:899-907`（第 3 拍切走）、`:940-947`（第 3 拍取消）、`:972-979`（第 13 拍切走）、
`:1043-1049`（第 12 拍切走）。
证据：注释里写着推导过程，例如 `:974-976`「倒计时 10 拍 + 150ms 稳定等待 1 次 = 11 次;
第 12/13 次分别是第一个/第二个字符后的间隔」、`:1045-1046`「第 12 次是写入剪贴板后的 100ms 等待」。
影响：这些索引是"时序常量 × 路径长度"的隐含函数。把 `countdownTick` 从 100ms 改成 50ms
（`typing.go:128`）就会让每一拍翻倍，5 条用例的魔数全部失效，症状是一堆看似无关的失败。
而 `AGENTS.md:121-124` 明确鼓励"调参只改常量区一处"，两者直接冲突。
建议：① 优先改用事件钩子而非数睡眠，`TestClipboardReportsUnconfirmedWhenTargetSwitchesAtPaste`
（`typing_test.go:1074-1078` 用 `inj.onInject` 在粘贴瞬间切窗）已经是正确做法的样板，
逐字符路径也可以用 `onInject` 在第 N 次注入后切窗，与时序常量无关；
② 若确实需要在某个等待点动手，给假 sleep 传一个可读的"阶段名"，例如让服务在
`s.sleep` 之外暴露"当前阶段"的测试钩子，或者至少把索引写成
`countdownTicks(1) + 1` 这样的表达式而不是裸的 12/13。

**[吹毛求疵] fake 的"失败注入"用 `failAfter` 计数表达，含义需要读注释才懂。**
位置：`typing_test.go:23-46`。用法处是 `inj.failAfter = 2 // A B 通过, C 起被拒`（`:567`）。
影响：可读性尚可（注释都写了），但每次要在脑子里映射"第几个事件"。
建议：可选地支持"按事件名失败"（`failOn("V")`），能让"粘贴被拒"这类用例自解释。

## 4.3 用例命名与断言风格

**[次要] 命名普遍不错，但缺少"状态序列"级的断言。**
现状：用例名基本是行为描述（`TestTypingStopsWhenTargetSwitches`、`TestClipboardNotRestoredIsReported`），
这是好习惯。断言则以单点为主（终态 phase + message + 注入序列）。
影响：单点断言对"中途每一拍写了什么状态"几乎没有约束，
而前端重构要吃的恰恰是"状态序列"这碗饭（轮询每拍读到的对象形状与取值）。
建议：加一条表驱动的"状态序列快照"用例：让假 sleep 在每次等待前记录
`(phase, message, progress, secondsLeft, targetWindow)` 五元组，
对一个 3 秒倒计时 + 5 个 ASCII 字符的完整任务，断言整条序列。
它同时是第 2 节契约测试的补充（能抓到"progress 分母变了""秒数跳档"这类肉眼难发现的变化），
并且天然覆盖了 `AGENTS.md:209-212` 里"采样与写状态分离"这条不变量
（现有 `TestCountdownTicksAndSecondBoundaries` 只断言了采样次数与秒数，没有断言"其余拍不写状态"）。

## 4.4 缺测试的清单（按优先级）

1. `cmd/type` 与 `internal/web` 零测试：装配层没有任何自动验证，这正是第 2 节要补的。
2. 键入中途取消、剪贴板中途取消（第 1.2 节三块 0 覆盖）。
3. 非 ASCII 逐字符路径（`forceSendInput=true` / `textDirect=true` 配中文）。
4. 取消后立即重启的并发交错（lead 的 task-1 已复现真实缺陷，见摘要第 2 条）。
5. `Cancel()` 在空闲状态被调用后再 `Start`（读代码是安全的：`typing.go:201` 的重入检查要求
   `runningFlag` 为真，`cancelFlag` 会在 `typing.go:265` 被清掉，但没有任何用例走过）。
6. `internal/win32` 注入层（第 1.3 节），至少把"选路"从"真发按键"里拆出来做成可测的纯函数。
7. 前端：零测试设施（第 2.2 节文件三）。

## 4.5 给这次前端重构的一句话总结

后端的状态机本身写得相当克制（原子标志 + 任务代数 + 注入式时钟，`typing.go:163-176`），
测试也覆盖了绝大多数业务分支；真正的风险不在逻辑，在**边界**：
Go 与 TS 之间的四个契约面没有任何自动守护，
而平台层（注入）与装配层（Bind）是全仓测试的两个空白。
建议在动手重构之前先花半天把第 2.2 节的文件一、文件二落地，
它们只增加测试文件、不改产品代码，落地后 `go test ./...` 就变成了契约门禁。

---

## 附：待确认事项

1. `输入失败`（`typing.go:439`）是否刻意保留的不可达兜底，需要作者确认（本次按只读审计推断为不可达，覆盖计数为 0 是证据之一）。
2. `AGENTS.md:276-280` 的 `WEBVIEW2_BROWSER_EXECUTABLE_FOLDER` 复现手法需要真机跑一次才算验证，本次未执行（会弹模态框，且属于启动路径）。
3. Windows 上 `go test ./...` 的 arm64 测试只编译不运行（`ci.yml:142-148`），
   所以 arm64 上的行为差异没有自动验证；这属于构建链范畴，可能与其他审计员的结论重叠。
4. win32 覆盖率两次跑出 43.7% 与 44.9%，差异来源推断是两条可跳过的剪贴板用例；
   未逐次核对 SKIP 计数（本机两次都是 0 SKIP，故差异可能另有原因，如并行度或系统状态）。
