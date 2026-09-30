# 审计 A：业务层 internal/typing（并发与状态机）

## 0. 范围与基线

| 项 | 值 |
|---|---|
| 审计对象 | `internal/typing/typing.go`（544 行）、`internal/typing/typing_test.go`（1098 行）、`internal/typing/helpers_test.go`（49 行） |
| 基线 | git HEAD `19b22d6`，审计期间该包无工作区改动 |
| 方式 | 通读代码 + 隔离探针（在 `%TEMP%\typingprobe` 用与 HEAD blob 哈希一致的副本构造并发时序）+ 真实包测试三件套 |
| 写入 | 本报告是本次审计唯一写入仓库的文件；仓库其余文件未动 |

任务描述里写的行数（425 / 939）与实际不符，已按实际文件全量审计。涉及的用户可见文案全部在 AGENTS.md 的冻结清单里，本报告的建议一律不动文案，只改控制流。

## 1. 结论摘要

1. **取消后立刻重启并不会静默丢弃新任务**（lead 转述的复现不成立，见 §2）。那条路径实测 300/300 轮全部正常跑完，旧任务让出 `runningFlag` 后新任务照常倒计时并注入。看起来像"永久停在倒计时"是因为旧任务被探针的阻塞式假睡眠卡住不让位，而新任务在等它，这个等待有 2 秒上限，超时报 `启动失败：上一任务未能及时退出`。
2. **[重要，潜在] `Start` 的防重入判定与 goroutine 认领 `runningFlag` 不是原子操作**（`typing.go:201` 判定，`typing.go:257` 认领），两次并发 `Start` 可以都通过判定。实测两路并发 500 轮里 215 轮（43%）把同一段文本注入了两遍，8 路并发 1500 轮里 161 轮注入了 8 遍。
3. **[重要，潜在] `Start` 写倒计时初态是全文唯一没有代数守卫的状态写入**（`typing.go:210`，对比 `typing.go:240` 的 `setStatus`）。过代 `Start` 会覆盖新任务的终态；取消恰好落在 `Start` 的采样窗口内时，可以确定性地做出"没有任何任务在跑、界面却停在倒计时"的终局（探针 R1）。
4. **[次要，待确认] 取消标志在认领后被无条件清零**（`typing.go:265`）。落在 `typing.go:261` 与 `265` 之间的 `Cancel` 会被这次清零抹掉，用户看到"已取消"而文本仍被注入。窗口只有两条指令，3000 轮并发探针未能可靠构造，列为理论风险。
5. **[次要] 两处边界没有处理，加上测试缺口**：无前台窗口（`TargetID == 0`）会被当成合法目标继续注入（`typing.go:305`、`507`）；语句覆盖 94.2%，但"注入途中取消"（`typing.go:356`、`409`）、"剪贴板写入后取消"（`479`）、"进入粘贴前的漂移守卫"（`469`）、"非 ASCII 走逐字符路径的字符间隔档位"（`400-404`）四处零覆盖，三处并发认领分支（`249`、`258`、`262`）也零覆盖，CI 目前不跑 `-race`。

## 2. 核实记录：lead 转述的"取消后立刻重启会静默丢弃新任务"结论不成立

复刻时序（探针 `TestProbeLeadCancelThenRestart`，阻塞式假睡眠，与 lead 描述一致）：旧任务认领 `runningFlag` 后卡在倒计时等待点；`Cancel()`；立刻 `Start()`；观察。

实测输出：

```
A 旧任务持有 runningFlag 且卡在等待点: phase=countdown msg="剩余 3 秒 — 请聚焦目标窗口..." runningFlag=true 等待次数=1
B 立刻重启后: start2=("started",<nil>) phase=countdown msg="剩余 3 秒 — ..." runningFlag=true 注入数=0 等待次数=1
C 300ms 后(旧任务仍卡着, 新任务只能等): phase=countdown ... 注入数=0 等待次数=1
D 放行旧任务后: phase=success msg="输入完成" runningFlag=false 注入序列=[r:h r:e r:l r:l r:o r:  r:w r:o r:r r:l r:d] 等待次数=43
```

D 行说明新任务把 11 个字符完整注入了（等待次数从 1 涨到 43，即新任务自己又跑了 30 拍倒计时 + 1 次稳定等待 + 11 个字符间隔）。

逐行核对代码：新任务在 `typing.go:247` 的循环里等旧任务让出 `runningFlag`，循环每 25ms 轮询一次，2 秒后走 `typing.go:251-253` 报错。它的 `gen` 与 `taskGen` 相等（`Cancel` 与本次 `Start` 各 +1），所以 `typing.go:248` 的过代判断为假，不会误判自己过代。lead 描述的"新任务把自己误判成过代任务"在这条时序里不成立。

把旧任务永远卡住（`TestProbeOldTaskNeverLetGo`）可以看到这个等待是有界的：

```
t=2000ms phase=countdown msg="剩余 3 秒 — 请聚焦目标窗口..." runningFlag=true 注入数=0
t=2250ms phase=error     msg="启动失败：上一任务未能及时退出" runningFlag=true 注入数=0
```

把整条序列压成 300 轮（`TestProbeCancelRestartLoop`）：

```
取消后立刻重启 300 轮: 新任务正常跑完=300 永久停在倒计时=0 被拒=0 其他=0
```

结论：lead 观察到的是探针窗口问题（阻塞式假睡眠只放行了一次，旧任务没让位，新任务一直在等），不是产品缺陷。实际使用中旧任务收到取消后最多一拍（倒计时 100ms、字符间隔 8~16ms、剪贴板稳定等待 200ms）就会退出让位。

对 lead 给出的根因分析的评价：`taskGen` 一个计数器同时承担"Cancel 作废旧任务"与"Start 开新一代"两种语义，这个可读性问题确实存在（`typing.go:226` 与 `208`），但它不是上述现象的原因，把两个计数器拆开也不能单独修掉 §3 的 F1、F2，因为那两处的根因分别是"判定与认领非原子"和"初态写入没有代数守卫"。建议按 F1、F2 的建议改，计数器拆分可以作为可读性改进顺带做。

## 3. 逐条发现

### F1 [重要，潜在] `Start` 的防重入判定与任务认领之间存在 TOCTOU，认领失败与过代路径静默返回

位置：`typing.go:201-203`（判定）、`typing.go:247-263`（认领）、`typing.go:249-250`、`258-259`、`262-263`（三处静默 `return`）

证据：

- `Start` 在 `typing.go:201` 读 `runningFlag` 判断"是否有任务在跑"，但 `runningFlag` 是由新开的 goroutine 在 `typing.go:257` 才置位的。判定与实际占领之间没有任何同步，两次几乎同时到达的 `Start` 都会读到 false 从而都放行；`Start` 会立刻返回 `("started", nil)`（`typing.go:218`）。
- 认领失败（`typing.go:258` `CompareAndSwap` 失败）直接 `return`，不写任何状态；此时若它正是最新一代，界面就会永久停在 `Start` 写下的倒计时状态（`typing.go:210`），没有任何任务在跑。`typing.go:249`、`262` 两处过代 `return` 同样不写状态。
- 探针 `TestProbeBurst2`（两路并发 `Start("AB", 0, ...)`，500 轮，`%TEMP%\typingprobe\zzprobe_more_test.go`）：

```
2 路并发 Start 500 轮: 重复注入(4 次)=215 卡死倒计时=0 正常单次=283 其他=2 被拒=168
第 222 轮其他: phase=countdown msg="剩余 0 秒 — 请聚焦目标窗口..." calls=2
第 426 轮其他: phase=countdown msg="剩余 0 秒 — 请聚焦目标窗口..." calls=2
```

215/500（43%）把文本注入了两遍；2 轮最终停在倒计时且没有任务在跑（另见 `TestProbeConcurrentStartBurst`：8 路并发 1500 轮里 161 轮注入 8 遍、55 轮最终停在倒计时）。

影响：这是一段会往用户目标窗口里写字的代码，重复注入会污染用户内容（例如把一段代码或一条消息发两遍），卡死倒计时则表现为"开始按钮点不动、界面永远在倒数"，只能靠再点一次取消恢复。

可达性（重要，决定这条能不能排期修）：**当前发布版 UI 触发不到**。前端 `frontend/src/composables/useTypingTask.ts:64` 在同步代码里就检查 `isRunning`，`:71` 在 `await startTyping(...)`（`:83`）之前同步置位，单 JS 上下文内两次 `start()` 无法交错，按钮本身也绑了 `:disabled="isRunning"`。所以这条是**潜在缺陷**：它破坏的是后端自己文档化的不变量（`typing.go:169` 注释"运行中互斥, 防止重复启动"），以及 README「运行中防重入，不会叠加启动」的承诺，目前完全靠前端一道同步守卫兜着。一旦出现第二个调用方（多窗口、开发工具、绑定层被并发调用），或前端改成先 `await` 再置位，立刻变成用户可见的重复注入。

建议：

1. 让"判定 + 占领 + 写初态"落在同一个临界区内。最省事的是给 `TypingService` 加一把 `sync.Mutex`，`Cancel` 与认领段都进这把锁；或用一次 CAS 把状态从"空闲"直接改成"启动中（第 N 代）"，`Start` 返回值以这次 CAS 结果为准。
2. 需要保留"取消后立刻重启"的顺滑体验时，让 `Start` 同步登记"本代预留"（这样第二个 `Start` 会被拒），再由 `runTypingTask` 等旧任务让位后接管。判定改由预留决定，"started" 才名实相符。
3. `typing.go:249`、`258`、`262` 三处静默 `return` 改成"按当前代数写一条终态"，至少保证不存在"状态停在运行 phase 而没有任何任务"的组合。
4. 回归用例见附录 A 的 `TestConcurrentStartNeverStacks`（当前实现下会失败，这正是它的用途）。

### F2 [重要，潜在] `Start` 的倒计时初态写入没有代数守卫，过代写入可以覆盖新任务终态

位置：`typing.go:208-217`（`gen := taskGen.Add(1)` 之后直接 `Store`），对比 `typing.go:239-243`（`setStatus` 的代数守卫）

证据：

- 文件里所有其他状态写入都走 `setStatus`，过代就静默丢弃；只有 `Start` 这一处是无条件 `Store`。`Start` 在 `typing.go:209` 还要先做一次真实的前台窗口采样（`Sample()` 是 Win32 调用），所以"已递增代数、尚未写状态"的窗口不是零长度。
- 探针 `TestProbeCancelInsideStartWindow`（确定性复现，`%TEMP%\typingprobe\zzprobe_lead_test.go`）：让 `Start` 卡在 `baseline := s.foreground.Sample()` 里，此刻调 `Cancel()`（代数再 +1，状态写"已取消"），再放行采样。`Start` 随后把倒计时状态盖在"已取消"上面，而它的任务在认领 `runningFlag` 后因 `typing.go:261` 代数不符直接退出。终局：

```
R1 终局: phase=countdown msg="剩余 3 秒 — 请聚焦目标窗口..." runningFlag=false 注入数=0
R1 命中: 没有任何任务在运行, 状态却停在倒计时, 只能靠再点一次取消恢复
```

影响：界面显示"剩余 3 秒"且永远不前进，开始按钮因为 `phase` 不是终止态而一直禁用（前端只在终止 phase 停止轮询），用户必须点一次取消才能恢复。这也解释了 F1 探针里那几轮"卡死倒计时"的终局。

可达性：需要 `Cancel` 落在 `Start` 的 `Sample()` 窗口里（微秒级）。前端在 `await startTyping` 期间是允许点取消的（`isRunning` 已置位、按钮已切换），但要人在这条窗口内完成第二次点击，现实中做不到；与 F1 一样属于潜在缺陷，修法一致。

建议：把 `Start` 的初态写入也交给同一套代数守卫（把 `baseline` 与 `gen` 一起传进一个带 `gen == taskGen.Load()` 判断的写函数）；如果发现代数已被顶掉，就直接返回错误，不要留下倒计时状态。这条与 F1 的第 1 条建议一起做最干净：临界区里同时解决判定、占领与初态写入。

### F3 [次要，待确认] 取消标志在认领后被无条件清零，落在认领段内的 `Cancel` 会被抹掉

位置：`typing.go:261-265`

证据：认领成功后执行 `s.cancelFlag.Store(false)`（`typing.go:265`），注释说明这是为了清掉上一轮取消。但如果 `Cancel` 恰好落在 `typing.go:261` 的代数检查与 `265` 之间，`Cancel` 刚置位的取消标志会被这里清掉，接着 `cancelled := s.cancelFlag.Load`（`typing.go:267`）读到 false，任务会跑完整个倒计时并注入。用户看到的是"已取消"（`Cancel` 已写入终态、前端停止轮询），文本却照样打进目标窗口。

我构造的 3000 轮 `Start`/`Cancel` 并发探针（`TestProbeCancelWipe`）报出 3000/3000，经逐条核对**判据不成立，结果作废**：绝大多数轮次里 `Cancel` 整体发生在 `Start` 之前（`Start` 的 goroutine 还没被调度），此时清标志是"取消后重启"的正常语义，不是抹掉取消。真正的窗口只有 `typing.go:261` 到 `265` 两条指令，探针无法可靠命中，故这条列为理论风险而非已复现缺陷。

建议：把 `Cancel` 的 `(taskGen.Add(1) + cancelFlag.Store(true))` 与认领段的 `(代数检查 + cancelFlag.Store(false))` 放进同一把锁；或者把取消标志做成带代数的结构，只有"取消针对的是本代之前"时才允许清零。修 F1 时顺手一起做，成本几乎为零。

### F4 [次要，待确认] 无前台窗口（`TargetID == 0`）没有被当成异常

位置：`typing.go:305-314`（锁定）、`typing.go:507-509`（漂移守卫）、契约见 `typing.go:57`

证据：`ForegroundSample` 的契约写明"无前台窗口时为 0"（`typing.go:57`），win32 侧确认会返回零值样本（`internal/win32/win32_window.go:209-211`：`GetForegroundWindow` 返回 0 时返回 `typing.ForegroundSample{}`）。业务层锁定目标时只检查 `locked.Self`（`typing.go:306`），不检查 `locked.ID == 0`，于是"没有可注入的窗口"会被当成合法目标：`target` 为空标题，注入照常发出，`targetHeld(0)`（`typing.go:508`）在仍然没有前台窗口时返回 true，不触发漂移中止。

影响：这类情形下按键/粘贴其实发不出去（SendInput 不产生按键），终态会把原因说成 `输入中断：目标窗口拒绝了模拟按键，可能其权限高于 Type` 或 `粘贴未生效：...`，与真实原因（没有前台窗口）不符，用户会往权限方向排查。属于误导性错误信息，不会误投内容。

待确认与验证方法：真机上在倒计时结束那一刻制造"无前台窗口"（最容易的是锁屏，或切到安全桌面/UAC 提权框），用 `internal/win32` 的 `Foreground.Sample()` 打印 `ID`。如果确实会返回 0，建议在 `typing.go:306` 旁边补一条 `locked.ID == 0` 的分支，报错并零注入；新增文案需要按 AGENTS「行为契约」登记（铁律 1 允许新增，但必须登记），不要复用"未切换到目标窗口"那条冻结文案，它说的是焦点还在 Type 自身。

### F5 [次要] 测试覆盖缺口与 CI 的 `-race` 缺位

位置：`typing_test.go` 全篇、`.github/workflows/ci.yml`

证据（`go test -count=1 -cover ./internal/typing` = 94.2%，未覆盖语句块）：

| 位置 | 未覆盖的分支 | 说明 |
|---|---|---|
| `typing.go:356`、`409-410` | 逐字符注入途中被取消 | 用户点取消最常见的时机之一，只有倒计时中取消有测试 |
| `typing.go:479-480` | 剪贴板写入完成后、粘贴前被取消 | 取消与剪贴板的交互没有用例 |
| `typing.go:469-470` | 剪贴板路径"快照前"的漂移守卫 | 现有用例只覆盖了"粘贴前"那次（`483`） |
| `typing.go:400-404` | 非 ASCII 走逐字符路径的字符间隔档位（CJK / 标点） | 中文只走过剪贴板路径；`isCJKPunct` 本身由 `helpers_test.go` 单独覆盖，但三档延迟从没被执行过 |
| `typing.go:249`、`258`、`262` | 三处并发认领分支 | 正是 F1/F2 的所在，零覆盖 |
| `typing.go:297-298` | 最后一拍之后才发现取消 | 边界时序 |
| `typing.go:439-440` | `输入失败` 兜底 | 见 F7，属安全网 |

另外，`-race` 不在 CI 里（AGENTS「常用命令」的 Go 验证三件套也只有 `go test -count=1 ./...`），而这个包恰好是竞态风险最集中的地方。本机实测 `go test -count=1 -race ./internal/typing` 通过（3.5s，`go1.27.1` + mingw64 的 gcc）。

建议：补上面四类用例（尤其是"注入途中取消"和"剪贴板路径的两处取消"）；把 `go test -race ./internal/typing` 加进 `ci.yml` 的 verify 作业。

待确认与验证方法：GitHub 的 `windows-latest` 运行器是否自带可用的 gcc（`-race` 在 windows/amd64 上需要 cgo 工具链）。在本机已确认可行，CI 侧需要实际跑一次 `go test -race ./internal/typing` 才能下结论；若 CI 无 gcc，退一步的做法是保留一个独立的 `-race` 作业并显式安装 mingw。

### F6 [次要] `Status()` 返回的是内部指针，不是值拷贝

位置：`typing.go:190-192`、`:185`、`:210`、`:228`、`:241`

证据：`Status()` 返回 `s.typingStatus.Load().(*TypingStatus)`，即存进去的同一个对象。当前是安全的：所有写入方都 `Store` 新分配的对象，任何人都没有在存下来之后再改字段，`cmd/type/main.go:77` 把它绑定给 `getTypingStatus`，只做 JSON 序列化。

影响：方法注释（`typing.go:189`）没有声明所有权约定，调用方拿到指针后改写字段不会有编译错误，会直接造成数据竞争，而且只有在跑 `-race` 时才看得见（CI 不跑，见 F5）。属于"现在没坏，但约定没写清"。

建议：二选一。要么把返回值改成 `TypingStatus` 值类型（前端每秒 10 次轮询，4 个字段的拷贝成本可忽略），要么在方法注释里写明"返回的指针只读，禁止改写"。前者更稳。

### F7 [吹毛求疵] 冗余与不可达分支（都不是缺陷，不建议改文案）

1. `typing.go:439-440` 的 `输入失败` 兜底在当前控制流下不可达：逐字符路径的三种失败都会设置 `failMsg`（`412`、`416`），剪贴板路径唯一返回空 `failMsg` 的是取消分支（`479`），而取消会被 `typing.go:425` 的 `case cancelled()` 先接住。结论：这条兜底是安全网，文案本身冻结，**保留**；可以补一条直接构造该组合的用例，但不必为它改控制流。
2. `typing.go:336-341` 与 `435-441` 对同一个剪贴板失败写了两次状态，第二次立即覆盖第一次，中间没有任何可观测的差别（失败路径的剪贴板恢复在 `typeTextViaClipboard` 内部就已经做完）。两次 `Store` 无害，属冗余。
3. `typing.go:406` 在最后一个字符之后仍然 `sleep(charDelay)`，让终态最多晚 8~16ms 出现。无害。
4. `Clipboard.GetText()`（`typing.go:38`）业务层从未调用，唯一使用者是 `internal/win32/win32_test.go:95` 与测试里的 fake。消费方定义的接口里带一个自己不用、只给实现方测试用的方法，属多余表面；删掉它需要同步改 win32 的测试。影响极小。

## 4. 已核实正常（不构成问题）

1. **没有锁，也没有锁内慢操作。** `typing.go` 只有原子变量（`typing.go:168-171`），全文件没有 `sync.Mutex`；不存在"持锁睡眠/注入/IO"的问题。`-race` 在真实包和探针套件上都没有报数据竞争。
2. **没有 goroutine 泄漏。** 全文件只有一处 `go`（`typing.go:217`）；`runTypingTask` 的所有分支都会 `return`，`defer s.runningFlag.Store(false)`（`260`）保证让位；没有 channel、ticker、无限等待，唯一的循环等待有 2 秒上限（`247`）。
3. **sleep 注入点覆盖完整。** 倒计时、锁定稳定等待、字符间隔、Esc 间隔、剪贴板稳定等待、粘贴稳定等待全部走 `s.sleep`（`294`、`300`、`406`、`453`、`476`、`492`），测试用假时钟推进；`yieldPollInterval` 与 `yieldDeadline` 刻意走真实时钟，代码里写明了理由（`typing.go:173-175`），代价是 `TestStartDeadlineWhenPreviousTaskStuck` 实测要花 2.03 秒真实时间。这是有记录的取舍，不是缺陷。
4. **冻结文案逐字一致。** 用脚本把 `typing.go` 里的 15 条用户可见文案与 AGENTS.md「行为契约」清单做逐字比对，全部命中；`剩余 %d 秒 — 请聚焦目标窗口...`、`正在逐字符输入 %d / %d ...` 与清单里的 `N`/`M` 形式一致（含长破折号与三个点的写法）。铁律 1 没有被违反。
5. **前端契约镜像一致。** `frontend/src/types.ts:5-23` 的 phase 枚举与 JSON 字段和 `typing.go:75-90` 完全对应。
6. **`typing.go:474` 在快照为 nil 时仍调用 `RestoreSnapshotRaw` 是无害的。** 业务层文档说 nil 表示"放弃恢复"（`typing.go:35`），这里传了 nil 进去；已核实 win32 实现对 nil 直接返回 false 且不碰剪贴板（`internal/win32/win32_clipboard.go:287-289`），所以不会误清用户剪贴板，只是多一次空调用。
7. **返回值语义没有"多个同义值等于没返回"的问题。** `typeTextViaClipboard` 的三个返回值各自可区分（`typing.go:466`）：`success` 区分是否送达，`failMsg` 区分剪贴板故障/粘贴被拒/目标切换三种原因（`470`、`474`、`484`、`496`、`499`），`clipboardOK` 只在成功路径被采信（`427`）。逐字符路径用 `drifted` 与 `ok` 两个独立布尔区分"切窗"与"被拒"（`363`、`382`），没有回归到 v1.5.1 修过的那类缺陷。

## 5. 验证命令与结果

真实包（`D:\Projects\Type`，HEAD `19b22d6`，工作区干净）：

| 命令 | 结果 |
|---|---|
| `gofmt -l ./internal/typing` | 无输出 |
| `go vet ./internal/typing` | 退出码 0 |
| `go test -count=1 -race ./internal/typing` | ok，3.563s |
| `go test -count=1 -cover ./internal/typing` | ok，coverage: 94.2% of statements |
| `go test -count=1 -run . -v ./internal/typing` | 29 个用例全部 PASS，0 FAIL，2.444s（其中 `TestStartDeadlineWhenPreviousTaskStuck` 2.03s） |
| `go tool cover -func=...` | `runTypingTask` 92.9%、`typeTextViaClipboard` 93.5%，其余函数 100% |

隔离探针（`%TEMP%\typingprobe`，`typing.go` 等三个文件与 HEAD blob 哈希逐个核对一致）：

| 探针 | 结果 |
|---|---|
| `TestProbeLeadCancelThenRestart` | 旧任务让位后新任务正常跑完，注入 11 字符，`phase=success`（lead 的复现不成立） |
| `TestProbeOldTaskNeverLetGo` | 2.25s 时落成 `phase=error / 启动失败：上一任务未能及时退出`，等待有界 |
| `TestProbeCancelRestartLoop` | 300 轮：正常跑完 300、卡死倒计时 0 |
| `TestProbeCancelInsideStartWindow` | 确定性复现"无任务却停在倒计时" |
| `TestProbeBurst2`（2 路并发 × 500 轮） | 重复注入 215、正常单次 283、被拒 168 次调用、终局停在倒计时 2 |
| `TestProbeConcurrentStartBurst`（8 路 × 1500 轮） | 注入 8 遍 161、终局停在倒计时 55、正常终止 1284 |
| `go test -race` 跑上述探针 | 无数据竞争报告 |
| `TestProbeCancelWipe` | 判据有假阳性，结果作废（见 F3） |

## 6. 附录 A：建议补的回归用例（前两条在当前实现下会失败）

放进 `internal/typing/typing_test.go` 即可，风格与现有 fake 一致。

```go
// gatedForeground 只阻塞第一次采样(即 Start 内部的 baseline 采样),
// 用来把 Cancel 精确塞进"代数已递增、初态尚未写入"的窗口。
type gatedForeground struct {
	n       atomic.Int64
	entered chan struct{}
	release chan struct{}
}

func (g *gatedForeground) Sample() ForegroundSample {
	if g.n.Add(1) == 1 {
		close(g.entered)
		<-g.release
	}
	return ForegroundSample{ID: 1, Title: "记事本"}
}

// 取消落在 Start 的采样窗口内时, 不得留下"没有任何任务却在倒计时"的状态。
// 钉住 F2。当前实现下失败: phase=countdown, runningFlag=false, 零注入。
func TestCancelInsideStartWindowLeavesNoRunningPhase(t *testing.T) {
	inj := newFakeInjector()
	fg := &gatedForeground{entered: make(chan struct{}), release: make(chan struct{})}
	svc := NewTypingService(inj, &fakeClipboard{}, fg)
	svc.sleep = noSleep

	done := make(chan struct{})
	go func() { defer close(done); _, _ = svc.Start("AB", 3, false, false) }()
	<-fg.entered // Start 已递增代数, 卡在采样里
	_, _ = svc.Cancel()
	close(fg.release)
	<-done

	deadline := time.Now().Add(time.Second)
	for svc.runningFlag.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if st := svc.Status(); st.Phase == PhaseCountdown || st.Phase == PhaseTyping {
		t.Fatalf("没有任何任务在运行, 状态却停在 %s/%q", st.Phase, st.Message)
	}
}

// 并发 Start 不得叠加: 每轮注入次数不得超过文本长度。钉住 F1。
// 当前实现下失败: 2 路并发 500 轮实测 215 轮注入了两遍。
func TestConcurrentStartNeverStacks(t *testing.T) {
	for round := 0; round < 200; round++ {
		inj := newFakeInjector()
		svc := NewTypingService(inj, &fakeClipboard{}, fakeForeground{title: "记事本"})
		svc.sleep = noSleep

		var wg sync.WaitGroup
		fire := make(chan struct{})
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-fire
				_, _ = svc.Start("AB", 0, false, false)
			}()
		}
		close(fire)
		wg.Wait()

		deadline := time.Now().Add(2 * time.Second)
		for svc.runningFlag.Load() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if got := len(inj.calls()); got > 2 {
			t.Fatalf("第 %d 轮注入了 %d 次, 文本只有 2 个字符: 并发 Start 叠加了", round, got)
		}
	}
}

// 逐字符注入途中取消: 立即停止, 终态为已取消, 注入数等于取消前的实际进度。
// 钉住 typing.go:356 与 409-410 这两处目前零覆盖的分支。
func TestCancelDuringTypingStopsImmediately(t *testing.T) {
	inj := newFakeInjector()
	var svc *TypingService
	var sleeps int
	svc = NewTypingService(inj, &fakeClipboard{}, fakeForeground{title: "记事本"})
	svc.sleep = func(time.Duration) {
		sleeps++
		// 倒计时 1 秒 = 10 拍, 第 11 次是锁定前的稳定等待, 第 12 次是首字后的间隔
		if sleeps == 12 {
			_, _ = svc.Cancel()
		}
	}

	if _, err := svc.Start("ABCDE", 1, false, false); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitTerminal(t, svc)
	if st.Phase != PhaseCancel || st.Message != "已取消" {
		t.Fatalf("终态 = %s/%q, want cancel/已取消", st.Phase, st.Message)
	}
	if got := len(inj.calls()); got > 2 {
		t.Errorf("取消后仍在注入: 共 %d 次 (%v)", got, inj.calls())
	}
}
```

## 7. 附录 B：过程记录与已知干扰

- 探针源码留在 `%TEMP%\typingprobe\`（`zzprobe_lead_test.go`、`zzprobe_more_test.go`），`typing.go`、`typing_test.go`、`helpers_test.go` 三个文件是从 HEAD 用 `git show` 取出的字节一致副本（已用 `git hash-object` 逐个核对：`f06a2473b15e`、`032d0c235c6a`、`1337dd0b17e0`）。仓库内没有留下任何探针文件。
- 审计过程中，`internal/typing/` 目录里曾短暂出现过另一个成员写的未跟踪探针文件 `zz_probe_temp_test.go`，其中一次版本引用了不存在的 `recordingInjector`，导致我的一次 `go test` 编译失败。该文件随后被删除。本报告 §5 的所有结果都取自 `git status` 只剩 `?? audit/` 的干净状态。
- 报告里提到的 `internal/win32` 行为只作为业务层判断的依据做了最小核实（`win32_window.go:207-217`、`win32_clipboard.go:286-292`），平台层的完整审计归审计 B。
