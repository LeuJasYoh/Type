# Type 后端审计总报告

> **后续（2026-09）**：Tier 0 三项与 **Tier 1 四项**已全部修完，改动见 v1.5.6：
> 跨端契约测试（`internal/typing/contract_test.go`、`cmd/type/contract_test.go`）、
> 并发启动缺陷（`Start` 的判定与占领收进同一临界区）、CI 加 `-race` 并把检查抽成
> `verify.yml` 供发版复用、修掉 arm64 测试编译空转、发版闸门补上前端漂移检查；
> T1-1 剪贴板快照不完整（`ClipboardSnapshot.Complete`，拿不全就不碰剪贴板、退回逐字符）、
> T1-2 焦点子窗口取不到时退化为按键注入（`focusedTarget` / `sendCharUnitsViaInput`）。
> 另外按同一批做了两条 Tier 2：单实例守卫认下 `ERROR_ACCESS_DENIED`，CI 加依赖漏洞扫描。
> 本批改动经一轮**对抗性独立复核**，复核提出的 4 条必修全部修完并各自补了回归测试
> （其中两条是我自己引入的真实缺陷：退位任务的超时写入用了自比较的代数守卫；
> 落在 `Start` 采样窗口里的取消被下一次启动抹掉）。仍未处理：快照无大小上限、
> `package-lock.json` 版本漂移、依赖升级（CVE 不可达，决定暂存）、`-trimpath`，
> 以及 Tier 3 的记录项。下文保持审计当时的状态，不追改。

审计范围：Go 侧全部产品代码与构建链路（`cmd/type`、`internal/typing`、`internal/win32`、`internal/web`、
`tools/`、`scripts/`、`.github/workflows/`），以及 Go 与 TS 之间的契约面。
前端 UI 样式与组件结构不在本次范围内（用户即将重构，本次只回答"重构前该先把后端哪些地方定下来"）。

基线：git HEAD `19b22d6`（v1.5.5）。审计期间**没有修改任何产品代码**，
仓库里只新增了未跟踪的 `audit/` 目录（四份分报告 + 本文件）。
所有结论都配有 `文件:行号` 与可复现命令；有争议的结论做了独立复核，见「§5 复核与修正」。

四份分报告的详细证据：`audit/report-A-typing.md`、`report-B-win32.md`、`report-C-build-ci.md`、`report-D-tests-docs.md`。

---

## 1. 结论摘要

后端整体是健康的：`gofmt` / `go vet` / `go test` 全绿，`-race` 无告警，
剪贴板句柄配对、`unsafe.Pointer → uintptr` 转换、`INPUT` 结构体的 64 位布局、注入失败的可见性
这几处最容易出隐蔽错误的地方，逐条核查后都是对的。**没有发现正在线上伤害用户的缺陷。**

真正需要动手的集中在两类：

1. **跨端契约没有任何自动守护。** 这正是前端重构的第一风险，也是最该在动 UI 之前先补的东西。
2. **业务层有两处"状态写入了、任务却没跑"的窗口**（并发 `Start`、`Cancel` 打进 `Start` 的采样窗口），
   当前被前端的一道同步守卫遮住，属于潜在缺陷；而重构动的恰恰就是那道守卫所在的文件。

---

## 2. 排期建议：先做 Tier 0，再动前端

### Tier 0：动 UI 之前必须先做（不加就等于裸奔）

| # | 事项 | 位置 | 为什么必须在重构前做 |
|---|---|---|---|
| T0-1 | 给四条跨端契约补自动断言 | 见 §3.C1 | 前端重构改字段名、换参数顺序、改 Bind 名字，Go 侧**不会报错**，`vue-tsc` 也不会报错，只有运行时界面静默失效 |
| T0-2 | 把 `Start` 的"判定 + 占领 + 写初态"收进一个临界区 | `internal/typing/typing.go:201`、`:210`、`:247-263` | 前端的 `isRunning` 同步守卫（`frontend/src/composables/useTypingTask.ts:64,71`）是当前唯一挡着重复注入的东西。重构这个文件时守卫一旦变成"先 await 再置位"，缺陷立刻变成用户可见 |
| T0-3 | `-race` 进 CI，并补并发交错用例 | `.github/workflows/ci.yml:41-42` | 现有 94.2% 的行覆盖率里，恰好不含任何并发交错路径（`-race` 今天在本机是绿的，但那是手工跑的） |

### Tier 1：尽快修（用户可感知的静默失败）

| # | 事项 | 位置 |
|---|---|---|
| T1-1 | 快照读不到的剪贴板格式被静默丢弃，恢复仍判成功 | `internal/win32/win32_clipboard.go:229-231`、`:259-281` |
| T1-2 | 焦点子窗口取不到时退回顶层容器窗口，`WM_CHAR` 被丢而报成功 | `internal/win32/win32_window.go:190,194` + `win32_keyboard.go:145-154` |
| T1-3 | `release.yml` 不复跑 verify：带过期前端产物也能发出 Release | `.github/workflows/release.yml:73-97` |
| T1-4 | arm64 的「测试可编译」是空转（对无测试文件的包跑 `go test -c`，不产出任何东西） | `.github/workflows/ci.yml:142-148` |

### Tier 2：值得修

- 快照无大小上限（大图会带来几百 MB 峰值）：`win32_clipboard.go:251`
- 单实例守卫在"互斥体已存在但无权打开"时放行，而 README 恰好建议管理员运行：`win32_instance.go:55-58`
- `package-lock.json` 版本停在 1.3.4，是没人同步的第三个版本载体：`frontend/package-lock.json:3,9`
- 依赖含 2 条不可达 CVE 且 CI 无漏洞扫描：`go.mod:13-14`（详见 `report-C` 的 F3，已附 govulncheck 原文）
- 未加 `-trimpath`，exe 里嵌了 9 处构建机绝对路径：`scripts/build.ps1:53`、`ci.yml:134`、`release.yml:87`

### Tier 3：记录即可

`Status()` 返回内部指针而非值拷贝（`typing.go:190-192`）、`输入失败` 兜底当前不可达（`typing.go:437-440`，
文案已冻结、勿改）、`applyWindowIcon` 未防 `hwnd == 0`（`win32_window.go:113-139`）、
图标句柄不显式释放（`win32_window.go:80-106`）、`types.ts:2` 与 `ipc.ts` 的过期注释。

---

## 3. 逐项说明

### C1 [阻塞] 四条跨端契约零守护（前端重构的头号风险）

契约面本身**当前是对得上的**，问题是一件自动化的东西都没有：

| 契约 | Go 侧 | TS 侧 | 守护者 |
|---|---|---|---|
| Bind 函数名 4 个 | `cmd/type/main.go:64-77` | `frontend/src/ipc.ts:9-15` | 无 |
| `startTyping` 四个参数顺序与类型 | `internal/typing/typing.go:199` | `ipc.ts:9,26` | 无（位置传递） |
| `TypingStatus` 五个 JSON 键 | `typing.go:84-90` | `types.ts:14-23` | 无 |
| 六个 phase 取值 | `typing.go:75-82` | `types.ts:5-11` | 无 |
| 冻结文案 | `typing.go:94-118` 等 | `useTypingTask.ts:75` 还有第三份拷贝 | 部分测试用同名常量自比，等于没断言 |

为什么这对重构是关键：`go-webview2` 按**位置**反序列化参数、按 `json.Marshal` 返回结果。
把 `startTyping(text, delay, forceSendInput, textDirect)` 的后两个 bool 交换，Go 侧编译通过，
`vue-tsc` 通过，界面照常渲染，只是"绕过粘贴检测"和"文本直投"两个开关的作用互换；
把 `secondsLeft` 写成 `seconds_left`，前端读到 `undefined`，倒计时数字变成 `NaN`。
这类失配**没有任何环节会失败**。

已在 `report-D` 的 §2 给出可落地的三份测试规格（文件名、断言的字段与取值、失配后果），
推荐顺序：先加测试 → 故意改错 `types.ts` 确认测试会红 → 再动 UI。

### C2 [重要] 并发 `Start` 会重复注入（已独立复现）

`Start` 在 `typing.go:201` 读 `runningFlag` 判断"有没有任务在跑"，而该标志要到新 goroutine 执行到
`typing.go:257` 才被置位。判定与占领之间没有同步，两个几乎同时到达的 `Start` 都会通过判定，
各自写倒计时初态（`:210`）并返回 `"started"`。

我的独立复现（两路并发 `Start("AB", 0, false, false)`，300 轮）：
**重复注入 246 轮，正常单次 54 轮**（同时进行的另一位审计员用 500 轮测到 43%，
8 路并发 1500 轮里 161 轮注入了 8 遍，55 轮终局停在倒计时）。

同时命中 `typing.go:249-250`、`:258-259`、`:262-263` 三处"不写终态直接 return"时，
会留下"没有任何任务在跑，状态却停在 `countdown`"的终局：`Start` 已经同步写下倒计时，
而 `countdown` 不是终止态，前端不会停止轮询（`useTypingTask.ts:40`），
界面就永久显示"剩余 N 秒"，开始按钮一直禁用，只能再点一次取消恢复。

可达性：**当前发布版 UI 触发不到**（`useTypingTask.ts:64` 的 `isRunning` 是同步检查、`:71` 在 `await` 之前同步置位，
单 JS 上下文内两次 `start()` 无法交错，按钮也绑了 `:disabled="isRunning"`）。
所以它破坏的是后端自己文档化的不变量（`typing.go:169` 的 `runningFlag` 注释"运行中互斥"、
README 的防重入承诺），
目前完全靠前端一道同步守卫兜着。**这道守卫就在重构范围内，所以它进了 Tier 0。**
（顺带一提，仓库里已经有 `TestRestartAfterCancelSupersedesOldTask`（`typing_test.go:508-537`）
守着"取消后旧任务不注入、迟到的写入不覆盖新任务"，那条路径是好的；缺的是"并发 Start"这条。）

建议：把"判定 + 占领 + 写初态"放进同一个临界区（给 `TypingService` 加一把 mutex，
或把状态从"空闲"直接 CAS 成"启动中（第 N 代）"，以 CAS 结果决定 `Start` 的返回值）；
三处静默 `return` 改成"按当前代数写一条终态"，保证不出现"状态停在运行 phase 却没有任务"的组合。

### C3 [重要] `Cancel` 落在 `Start` 的采样窗口内会留下卡死的倒计时（已独立复现）

`typing.go:210` 的倒计时初态写入是全文**唯一没有代数守卫**的状态写入（其余都走 `:239-243` 的 `setStatus`）。
而它前面 `:209` 要做一次真实的前台窗口采样（Win32 调用），所以"已递增代数、尚未写状态"的窗口不是零长度。

我的独立复现（把 `Sample()` 卡住，在此期间调 `Cancel()`，再放行采样）：**20/20 轮**留下终局
`phase=countdown / "剩余 3 秒 — 请聚焦目标窗口..." / 零注入`，即没有任何任务在运行、界面却停在倒计时。

可达性：需要 `Cancel` 恰好落在这个微秒级窗口里。前端在 `await startTyping` 期间确实允许点取消，
但要人在这条窗口内完成第二次点击做不到，所以与 C2 一样属于潜在缺陷，修法也一致。

### C4 [重要] 快照读不到的剪贴板格式被静默丢弃，却报"已恢复"

`Snapshot` 对每个格式调 `readClipboardFormat`，`GetClipboardData` / `GlobalSize` / `GlobalLock`
任一失败就只是不 append（`win32_clipboard.go:229-231`），快照里不留痕迹。
恢复时 `RestoreSnapshotRaw` 先 `EmptyClipboard`（`:297`），**这会把原持有方留在剪贴板上的那些格式一并销毁**，
随后只写回快照里保住的格式，全部写成功就返回 true（`:259-280`）。
上层据此判定 `restored`（`internal/typing/typing.go:521-524`），终态报"输入完成"，
而不是"输入完成，但剪贴板未恢复，原内容可能已丢失"。

用户原本复制的东西只要有一个格式本次读不到，就被销毁且没有任何提示。
这正好撞在项目自己定的"失败必须可见"上（`AGENTS.md` 反复强调的那一条）。
建议给快照加"不完整"标记，让已有那条终态文案把它说出来。

### C5 [重要] 焦点子窗口取不到时，文本直投可能一个字都没进去却报成功

`focusedHWND` 在 `GetGUIThreadInfo` 失败或 `hwndFocus == 0` 时退回**顶层前台窗口**
（`win32_window.go:194`），而它自己的注释就写着"前台顶层窗口通常只是容器（如浏览器主窗口），
直接向其发消息会被丢弃"（`:179-180`）。`sendCharUnitsViaWMChar` 只在 `hwnd == 0` 时才退化为
`SendInput`（`win32_keyboard.go:145-154`），拿到容器窗口这条路径不会退化，`WM_CHAR` 直接被丢掉，
而 `SendMessageTimeoutW` 仍返回非零（探针证实：只要消息被窗口过程处理过就返回非零），
于是"一个字都没进去"被报成注入成功。

`AGENTS.md` 写的是"拿不到窗口时 SendText 退化为 SendInput"，与实际分支条件不完全一致。
同样属于项目最忌讳的静默失败。建议把"拿不到焦点子窗口"与"拿不到窗口"同等对待。

### C6 [重要] 发布闸门不复跑 verify，arm64 那步是空转

`release.yml` 的步骤只有 checkout → setup-go → 版本校验 → `go test` → mkres → 双架构构建 + pecheck → 建 Release，
**没有 setup-node、没有 npm、没有任何前端漂移检查**，也没有 `gofmt` / `go vet`。
而 `ci.yml:59-81` 的漂移检查只存在于 CI 的 verify 作业。带着过期 `internal/web/dist/index.html`
的提交一旦打上 `v*` 标签，Release 就会静默带上与 `frontend/src` 不符的界面。
`release.yml:72` 的注释自己写了"打标签的提交未必走过 main 的 CI"，说明风险作者已意识到，但只补了 `go test`。

`ci.yml:142-148` 的 arm64「测试可编译」实测是空转：`GOARCH=arm64 go test -c -o <tmp> ./cmd/type`
输出 `?  .../cmd/type [no test files]`、exit 0、**不产出任何文件**；
而真正有测试的 `internal/typing` 与 `internal/win32` 反而没做 arm64 测试编译
（对照实测：`./internal/typing` 能编出 4.5MB 的 arm64 测试二进制）。

建议：把 verify 抽成 `workflow_call` 可复用工作流供两个 workflow 调用；最小改动则是给
`release.yml` 加 setup-node + `npm ci` + 与 `ci.yml:59-81` 完全相同的漂移检查，
以及不需要 Node 的 `gofmt` / `go vet`；arm64 那步改成编译 `internal/typing` 与 `internal/win32`。

---

## 4. 已核查、确认没问题的部分（重构时不要"顺手改"）

这些是本次审计特意去证伪、结果证不动的，列出来是为了避免重构时被误改：

- **`INPUT` 结构体的 40 字节布局正确**：探针实测 `INPUT` 40 字节、`ki` 偏移 8、`KEYBDINPUT` 24 字节、
  `dwExtraInfo` 偏移 16，与 x64 ABI 一致；`GUITHREADINFO` 72、`SHFILEINFO` 696 也对得上。
- **`GlobalSize` 不改大请求值**：1 到 100000 各档全部等于请求值，
  因此 `HoldsText` 的整块字节比对成立（若会取整，守卫会把大多数文本误判成"用户已改动"而跳过恢复）。
- **`SendMessageTimeoutW` 的失败判据不是假警报**：窗口过程返回 0 时 `ret` 仍为 1；
  跨线程超时时 `ret = 0` + `ERROR_TIMEOUT`，且超时的那条 `WM_CHAR` 之后**不会补投**，不存在迟投递乱序。
- **`unsafe.Pointer → uintptr` 转换全部写在 `Proc.Call` 的参数表达式里**，没有"先存进 uintptr 变量再传"的写法。
- **剪贴板句柄配对正确**：`GlobalAlloc` / `GlobalLock` 的失败路径都配了 `GlobalFree`，
  `SetClipboardData` 成功即交出所有权、失败即自己释放，`GetClipboardData` 拿到的句柄只读不释放、锁后必解锁。
- **`Foreground.Sample` 确实一次读全**（标识、标题、是否自身都从同一次 `GetForegroundWindow` 派生）。
- **单实例守卫不存在 TOCTOU**，异常退出也不残留（句柄随进程关闭、对象随最后一个句柄销毁）。
- **`^uintptr(13)` / `^uintptr(33)` 这类负常量是本仓库的正确写法**（按位取反表达负数），有真建窗口读回的实测用例钉着。
- **平台约束完整**：`GOOS=linux` 与 `GOARCH=386` 下 `internal/win32` 都按预期 `build constraints exclude all Go files`。
- **`go 1.26.5` 是真实存在的稳定版本**（已对 go.dev 的发布列表核实），不是笔误。
- **`build.ps1` 的 UTF-8 BOM 仍在**（前三字节 `EF BB BF`）——但 CI 里没有任何检查守着它，属于回归风险而非当前缺陷。
- **`-race` 在本机全绿**（`go test -count=1 -race ./...`），`internal/typing` 语句覆盖 94.2%。

---

## 5. 复核与修正（含一条被推翻的结论）

本次审计刻意做了交叉复核，下面两条值得记录，因为它们说明"看起来像 bug"与"真的是 bug"之间的差距：

**修正一：我最初报的「取消后立刻重启会静默丢弃新任务」，经独立复核不成立。**
我的第一版探针里，新任务用的假睡眠会在一个有界 channel 上阻塞，加上我从未观察放行之后的终局，
于是把"新任务还在等旧任务让出运行标志"误读成了"新任务永远不会跑"。
改用能区分归属的设计（旧任务注入 `OLDOLD`、新任务注入 `NEWNEW`，看注入序列里出现的是谁）后，5/5 轮结果都是
`phase=success / 输入完成 / 序列 = [N E W N E W]`，**新任务正常跑完，旧任务零注入**；
旧任务永不让位时，2 秒后落成 `启动失败：上一任务未能及时退出`，等待是有界的。
结论：这条不是缺陷。同一位审计员用 300 轮压测得到 300/300 正常。

**修正二：同一片区域确实有真实缺陷，而且是它的两条，不是我的那条。**
复核过程中独立复现了 C2（并发 `Start` 重复注入，我的 300 轮里 246 轮命中，比其 43% 还高）
与 C3（`Cancel` 打进 `Start` 采样窗口，20/20 轮留下卡死倒计时）。
这两条都在 Tier 0，因为它们依赖的那道前端守卫正在重构范围内。

**另外两条被证伪/降级的技术质疑**（保留在分报告里，供后续排查时不再走弯路）：

- 「`-race` 需要 cgo，而本仓库承诺构建链不含 C 编译器」：本地 `CGO_ENABLED=0` 确实直接拒绝，
  `CGO_ENABLED=1` + MinGW gcc 时通过。CI runner 是否预装 gcc 未确认，
  故 `-race` 只作为"待确认"写进建议，没有当成缺陷（Windows 上 race 检测器确实需要 cgo）。
- 「`HoldsText` 可能因 `GlobalSize` 取整而误判」：探针证伪，见 §4。

---

## 6. 审计过程与工作区状态

- 四份分报告由四路并行审计产出，本文件是对它们的收敛与交叉验证；分报告内含更细的证据、探针源码与待确认项。
- 所有探针写在系统临时目录（`%TEMP%\typingprobe`、`%TEMP%\type-audit-b`），
  我的两条判定探针临时放进包内、跑完即删，仓库最终无任何遗留。
- 最终工作区状态：`git status --porcelain` 只有 `?? audit/`；`git diff --stat` 与 `--cached --stat` 均为空；
  `internal/web/dist/index.html` 的 working blob 与 HEAD blob 同为 `0cc9796c…`（未被重新生成）；
  根目录 `Type.exe`（`.gitignore:4` 忽略）mtime 为 2026-09-27 15:09，早于本次审计，未被动过。
- `scripts/build.ps1` 全程未执行（它会重写前端产物并在仓库根写出 exe）。
- 审计结束后基线复跑：`gofmt -l ./cmd ./internal ./tools` 输出为空、`go vet ./...` 通过、
  `go test -count=1 ./...` 全绿。
