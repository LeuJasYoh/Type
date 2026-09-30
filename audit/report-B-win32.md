# 审计报告 B：平台层 internal/win32

- 审计对象：`internal/win32/` 全部 8 个文件（win32.go、win32_keyboard.go、win32_clipboard.go、win32_window.go、win32_instance.go、win32_msgbox.go、win32_webview2.go、win32_test.go），仓库 HEAD `19b22d6`。
- 方式：只读静态审查，加上构建/vet/测试与两个临时探针。探针写在系统临时目录，仓库内只新增本报告。探针只操作自己进程的隐藏窗口与私有 GlobalAlloc 内存，不注入按键或文本到任何真实窗口，不读写剪贴板。
- 严重度分级：阻塞（必须修）／重要（应当修）／次要（值得修）／吹毛求疵（记录即可）。
- 本报告结论：没有发现阻塞级问题。Input 结构体布局、SendMessageTimeout 判据、剪贴板句柄配对、uintptr 转换这几处实测下来都是对的，细节见「已核查、未发现问题的部分」。

## 结论摘要

1. `[次要]` 快照读不到的格式被静默丢弃，恢复流程却仍判定为「已恢复」：`EmptyClipboard` 已经把那些格式销毁，而返回值没有任何记录，用户看到的是「输入完成」（win32_clipboard.go:229-231、259-281）。
2. `[次要]` 剪贴板快照没有大小上限，整块复制所有内存块格式，大图（CF_DIB/CF_DIBV5）会带来几十到几百 MB 的常驻内存尖峰，任务期间一直占着（win32_clipboard.go:251）。
3. `[次要]` 单实例守卫在 `CreateMutexW` 返回 0 时一律放行；按文档，名字已被占用但调用方没有 MUTEX_ALL_ACCESS（例如先启动的是管理员权限实例）时也是返回 0，此时守卫失效，而 README:76 恰好建议用户以管理员身份运行（win32_instance.go:55-58）。
4. `[次要]` 文本直投在拿不到焦点子窗口时退回顶层前台窗口，浏览器一类容器会把 WM_CHAR 丢掉，而 `SendMessageTimeoutW` 仍返回成功，于是「一个字都没进去」被报成注入成功；这与 AGENTS.md 写的「拿不到窗口时退化为 SendInput」不一致（win32_window.go:190、194，win32_keyboard.go:145-154）。
5. `[次要]` `applyWindowIcon` 对 `hwnd == 0` 没有任何防御，而 `RedrawWindow(NULL, ...)` 按文档是「更新桌面窗口」，配合 `RDW_ALLCHILDREN` 相当于整屏失效，还会随延迟重试再发生三次；main.go:70 对同一个 `hw` 值是加了 `hw != 0` 判断的（win32_window.go:138）。

## 逐条发现

### B1 `[次要]` 快照读不到的格式没有记录，恢复仍判定为成功

- 位置：internal/win32/win32_clipboard.go:229-231、240、259-281、286-301
- 证据：`Snapshot` 对每个格式调 `readClipboardFormat`，只要 `GetClipboardData`、`GlobalSize`、`GlobalLock` 任一失败就 `return nil, false`（:240、:243、:247），调用侧只是不 append（:229-231），快照里不留任何痕迹。恢复时 `RestoreSnapshotRaw` 先 `EmptyClipboard`（:297），这会把原持有方留在剪贴板上的那些格式一并销毁；随后 `writeClipboardFormats` 只遍历快照里保住的格式，全部写成功就返回 true（:259-280），`RestoreSnapshotRaw` 照样返回 true。上层 internal/typing/typing.go:521-524 用这个布尔值判定 `restored`，于是终态是「输入完成」，不是「输入完成，但剪贴板未恢复」。
- 影响：用户原本复制的东西里，只要有一个格式本次读不到，就被我们销毁且没有任何提示，属于「静默丢数据」；这正是项目自己定的「失败必须可见」要排除的情形。触发条件是 `GetClipboardData` 失败（持有方渲染失败、拒绝渲染、格式数据已被回收等），不常见但不是不可能。
- 建议：给快照加一个「不完整」标记（例如 `Snapshot` 返回结构体，或对读失败的格式记一条 `Data == nil` 的占位），`RestoreSnapshotRaw` 遇到不完整快照时返回 false，让已有的「剪贴板未恢复」终态文案把它说出来。另外 win32_clipboard.go:183 与 :240 的注释写「延迟渲染格式 GetClipboardData 返回 0」，与文档不符：延迟渲染的机制是应用请求数据时系统给持有方发 WM_RENDERFORMAT、由持有方现渲染（Clipboard 概述里 WM_RENDERFORMAT 条目），返回 0 只在渲染失败时成立，建议把注释改成实际情况。

### B2 `[次要]` 快照没有大小上限

- 位置：internal/win32/win32_clipboard.go:213-234、251
- 证据：`readClipboardFormat` 对每个格式 `data := make([]byte, int(size))`（:251）后整块拷贝（:252），格式遍历没有任何总量或单项上限（:220-232）；快照在 typing.go:471 取出后一直持有到 :494 恢复完，恢复时 `writeClipboardFormats` 还要再分配一个同样大的 HGLOBAL（:262）。
- 影响：4K 截图（CF_DIB 约 3840×2160×4 ≈ 33MB）量级尚可，全景图、超大位图或某些应用塞进剪贴板的超大注册格式可以到几百 MB；这段时间内进程内存翻一倍以上，恢复瞬间还要再叠一份，低配机器会明显卡顿甚至触发换页。用户完全无感，只知道「输入时卡」。
- 建议：设一个总字节上限（例如 32MB，超出就按 B1 的「快照不完整」处理，如实告诉用户剪贴板没能完全恢复），或者至少跳过超过单项上限的格式并记录原因。不要默默超限。

### B3 `[次要]` 单实例守卫：互斥体存在但无权打开时放行

- 位置：internal/win32/win32_instance.go:47-66（关键是 :55-58 与 :59-63）
- 证据：`claimInstanceMutex` 拿 `procCreateMutexW.Call` 的返回值 `h`，只要 `h == 0` 就 `return true`（:56-58，注释写「创建失败(如权限受限)时放行」）。CreateMutexW 文档明确：`lpName` 命中已存在的具名互斥体时函数请求 MUTEX_ALL_ACCESS 权限，只有「后续进程拥有足够访问权」才拿到句柄并返回 ERROR_ALREADY_EXISTS；访问权不足时函数失败、返回 NULL。也就是说，「名字已被占用但打不开」与「根本没建成」在返回值上是同一个 0，而前者的语义恰恰是「已有实例在跑」。先启动的实例如果是管理员权限（高完整性级别），后启动的普通权限实例会在这一步被拒，然后被当成创建失败放行。方向是单向的：先普通后提权时，提权实例能打开普通实例建的互斥体，守卫仍然生效。
- 影响：守卫失效，两个实例同时运行，而守卫存在的理由（:4-6 注释）正是「两个实例争抢剪贴板与键盘焦点，快照/恢复互相交错可能把用户数据写丢」。README:76 又明确建议需要向提权窗口注入的用户以管理员身份运行 Type.exe，这条路径对用户是自然的。触发还要求两边都真的跑剪贴板路径的任务，所以不算高频。
- 建议：区分两种失败。`h == 0` 时看 `callErr`，若是 `ERROR_ACCESS_DENIED`（5），说明同名对象已存在（否则不会走到访问检查），按「已有实例」处理并弹提示框；其余错误保持现在的放行策略。改动只有几行，不影响「守卫本身不挡住启动」的初衷。
- 待确认：本机无法做提权对照实验，见「待确认」V1。

### B4 `[次要]` 焦点子窗口取不到时退回顶层窗口，字符可能被静默丢弃

- 位置：internal/win32/win32_window.go:181-195（:190 判据，:194 退回顶层），win32_keyboard.go:144-166（:145-154 只在 `hwnd == 0` 时退化）
- 证据：`focusedHWND` 在 `GetGUIThreadInfo` 失败或 `gti.hwndFocus == 0` 时返回顶层前台窗口（:194），而它自己的注释就写着「前台顶层窗口通常只是容器（如浏览器主窗口），直接向其发消息会被丢弃」（:179-180）；AGENTS.md 的实测结论同此。`sendCharUnitsViaWMChar` 只在 `hwnd == 0` 时才退化为 SendInput（:145-154），拿到顶层窗口这条路径不会退化，直接把 WM_CHAR 发给容器。探针 ①（见验证清单）证实：只要消息被窗口过程处理过，`SendMessageTimeoutW` 就返回非零，与过程返回值无关，所以「投递成功」被当成「注入成功」。
- 影响：文本直投模式下，若某个前台窗口的线程查询失败或没有焦点子窗口，整段文本可能一个字都没落进去，而终态报「输入完成」。这是本项目最忌讳的静默失败。触发频率未知，但这条路径没有任何补救。
- 建议：把「拿不到焦点子窗口」与「拿不到窗口」同等对待，让文本直投走 SendInput 退化（与 AGENTS.md 的描述对齐）。注意全角标点路径（win32_keyboard.go:62-66）共用这个函数，而它的退化目标 SendInput 对 U+FF00-FFEF 有已知 bug，所以退化判据要么按调用方区分（SendText 允许退化、全角绕行不允许），要么在注释里写明这处盲区。

### B5 `[次要]` `applyWindowIcon` 不防 `hwnd == 0`

- 位置：internal/win32/win32_window.go:113-139（:138 那条 `RedrawWindow`），调用侧 cmd/type/main.go:51-56
- 证据：`applyWindowIcon` 直接拿 hwnd 调 `PostMessageW`/`SetClassLongPtrW`/`RedrawWindow`（:116-138），没有任何有效性判断。RedrawWindow 文档：`hWnd` 为 NULL 时「the desktop window is updated」，而 `RDW_ALLCHILDREN`（0x0100）要求把子窗口一并纳入，桌面的子窗口就是全部顶层窗口，`RDW_INVALIDATE`（0x0001）在两个矩形/区域参数都是 NULL 时表示整个窗口失效。也就是说传入 0 会得到一次整屏失效，而 main.go:56 调 `RetrySetIcon(hw)` 后还有三档延迟重试（win32_window.go:154-159），最坏重复四次。main.go:70 对同一个 `hw` 值写了 `if hw != 0`，说明这个值在装配层是被当作可能为 0 的。
- 影响：任务栏/桌面/所有可见窗口被强制重画四次，正在跑全屏程序时会有可见闪动。当前上游路径下 `hw == 0` 走不到：go-webview2 的 `NewWithOptions` 在 `CreateWithOptions` 失败时返回 nil（模块缓存 webview.go:109-111），而 hwnd 建失败会让后面的 `Embed` 失败，从而被 main.go:43 的判空拦下。所以这是防御性缺口，不是当前可复现的缺陷，但补一行守卫的成本极低。
- 建议：`applyWindowIcon` 开头加 `if hwnd == 0 { return }`，或在 `RetrySetIcon` 里判一次；同时给那次重试加同样的判据。

### B6 `[次要]` INPUT 的 40 字节布局没有测试钉住

- 位置：internal/win32/win32_keyboard.go:37-52，internal/win32/win32_test.go（缺对应用例）
- 证据：布局靠注释与 386 编译禁令保证（:45-46），测试里只有图标类索引用了实测用例（win32_test.go:311-335），没有 `unsafe.Sizeof(INPUT{})`、`unsafe.Offsetof` 类断言。探针实测：`KEYBDINPUT` 24 字节、`dwExtraInfo` 偏移 16、`INPUT` 40 字节、`ki` 偏移 8、`GUITHREADINFO` 72 字节、`SHFILEINFO` 696 字节，与 x64 ABI 一致，当前是对的。
- 影响：布局写错时 `SendInput` 的行为是静默的（要么整体失败，要么按错位的字段注入），而编译器不会报错（386 已被 build tag 排除，等于少了一层本来能兜住的保护）。
- 建议：照 `TestApplyWindowIconClassIndex` 的先例补一条，断言 `unsafe.Sizeof(INPUT{}) == 40`、`unsafe.Offsetof(INPUT{}.ki) == 8`、`unsafe.Sizeof(KEYBDINPUT{}) == 24`、`unsafe.Offsetof(KEYBDINPUT{}.dwExtraInfo) == 16`。这类「真建窗口跑一遍」的实测用例是本仓库已经定下的规矩，结构体布局比常量更值得钉。

### B7 `[吹毛求疵]` 图标句柄的释放与线程约定

- 位置：internal/win32/win32_window.go:80-106、109-112、151-160，cmd/type/main.go:56
- 证据：`loadAppIcon` 用 SHGetFileInfoW 取大小图标句柄后从不 `DestroyIcon`。SHGetFileInfoW 文档要求「you are responsible for freeing it with DestroyIcon when you no longer need it」；同一页还建议「call this function from a background thread」，而 main.go:56 是在 UI 线程（`Run()` 之前）同步调用的。代码注释（:109-112）称 WM_SETICON 设置的句柄「归窗口所有，替换/销毁时由系统释放」，WM_SETICON 文档只讲了 lParam 与返回值，对所有权没有任何表述，这个说法在文档里找不到依据（也没有相反的依据，属于未写明）。
- 影响：句柄数固定为 2，进程结束由系统回收，观测不到泄漏；WM_SETICON 走 PostMessage（:116），若窗口先被销毁、消息没被处理，这两个句柄就一直没人释放，同样随进程结束消失。UI 线程调用发生在消息循环启动前，窗口内容此刻还没加载，实际影响是启动期多一次（可能几十到几百毫秒的）图标提取，不构成「界面卡住」。
- 建议：把注释改成实际做法（例如「句柄不显式释放，随进程结束回收」），或在确认无人接管时对未成功设置的句柄补 DestroyIcon；顺手在注释里说明为什么必须在 UI 线程调（SHGetFileInfo 要求先 CoInitialize，而 go-webview2 的 `New` 已经在主线程做了 STA 初始化），免得后人照文档把它挪到后台线程反而破坏 COM 前提。

### B8 `[吹毛求疵]` 三处被丢掉的返回值

- 位置：internal/win32/win32_window.go:81、88-103；internal/win32/win32_webview2.go:69-72；internal/win32/win32_clipboard.go:91
- 证据：`os.Executable()` 的错误被丢弃（:81），`SHGetFileInfoW` 的返回值也没判（:88、:97），失败时 `hIcon` 为 0，`applyWindowIcon` 就静默跳过，图标不设置而没有任何信息；`openInBrowser` 不看 `ShellExecuteW` 的返回值（≤32 表示失败），用户在「Type 无法启动」框上点「是」之后如果没能打开浏览器，就什么反馈都没有，而这是他唯一的出路；`SetText` 里 `procEmptyClipboard.Call()` 的返回值被丢（:91），失败只会在后面的 `SetClipboardData` 上以「剪贴板操作失败」这个笼统原因体现。
- 影响：都是局部、可恢复的场景，属于「可见性可以更好」，没有实际功能损坏。
- 建议：`loadAppIcon` 判一次 `SHGetFileInfoW` 与 `os.Executable` 的失败（返回值或 `hIcon == 0` 时打日志/留注释）；`openInBrowser` 在 `ShellExecuteW <= 32` 时补一个原生提示框（此时只有 MessageBox 可用，与 win32_webview2.go 的既有做法一致）；`SetText` 判 `EmptyClipboard` 失败即返回 false。

## 待确认

- V1 单实例守卫权限分支的可达性（对应 B3）。本机没有做提权对照实验（本会话无法提权）。验证方法：以管理员身份启动 `Type.exe` 并保持运行，再以普通权限启动一次，看是否出现第二个窗口；或写两个探针进程，第一个用 `CreateMutexW` 建具名互斥体后把自身 DACL 收紧（或直接以高完整性级别运行），第二个照 `claimInstanceMutex` 的逻辑调用并打印 `GetLastError`，预期看到 5 (ERROR_ACCESS_DENIED)。
- V2 `GetClipboardData` 在延迟渲染格式上的真实行为（对应 B1 里注释与文档的出入）。文档只说应用请求数据时持有方会收到 WM_RENDERFORMAT 并现渲染，没有说请求方是否会被无限期阻塞。验证方法：两个探针进程，A 用 `SetClipboardData(fmt, NULL)` 声明延迟渲染后停止泵消息，B 调 `GetClipboardData` 并计时，看是立刻返回 0、返回有效句柄，还是挂住。注意这个实验会写真实剪贴板，需要在实验前用 Snapshot 保存并在结束后恢复，最好在独立会话或虚机里做，不要在日常桌面上跑。
- V3 快照读失败的现实频率（对应 B1、B2）。验证方法：写一个只读探针，枚举当前剪贴板格式并对每个格式只取 `GetClipboardData`/`GlobalSize`（不写回），统计返回 0 的格式与出现的应用；多收集几天日常使用数据，才能判断「静默丢弃格式」是罕见还是常见。不要枚举后去触发渲染以外的写操作。

## 本次验证的命令与结果

在 `D:\Projects\Type` 下执行：

| 命令 | 结果 |
|---|---|
| `go vet ./internal/win32` | 退出码 0，无输出 |
| `go build ./internal/win32` | 退出码 0 |
| `go test -count=1 -v ./internal/win32` | 8 个用例全 PASS，`ok ... 0.565s`（含真剪贴板快照往返、图标类索引实测） |
| `GOOS=windows GOARCH=arm64 go build ./internal/win32` | 退出码 0 |
| `GOOS=linux go build ./internal/win32` | 退出码 1，`build constraints exclude all Go files` |
| `GOOS=windows GOARCH=386 go build ./internal/win32` | 退出码 1，同上（386 是刻意禁止的） |
| `GOOS=linux go list -f '{{.ImportPath}} goFiles={{len .GoFiles}}' ./...` | 全量输出里只有 internal/typing、internal/web 与 tools 的三个包；internal/win32 与 cmd/type 完全不在列表里，确认非 Windows 下平台层不参与编译 |
| `gofmt -l ./cmd ./internal ./tools` | 无输出 |

`go test` 会真实读写一次剪贴板（用例自带快照与恢复），这是任务指定要跑的；此外没有对剪贴板做任何操作。

探针（写在 `%TEMP%\type-audit-b\`，`go run` 直接跑，不写仓库）：

1. `layout_globalsize.go`：结构体布局与 `GlobalAlloc(GHND)/GlobalSize` 语义。结果：`KEYBDINPUT` 24（`dwExtraInfo` 偏移 16）、`INPUT` 40（`ki` 偏移 8）、`GUITHREADINFO` 72、`SHFILEINFO` 696，全部与 x64 ABI 一致；请求 1、2、4、6、8、10、12、14、16、18、20、22、24、26、30、32、40、64、100、254、256、1024、4096、100000 字节时 `GlobalSize` 全部等于请求值，没有取整放大。这一条直接支撑 `HoldsText` 的整块字节比对成立（若 `GlobalSize` 会取整，`HoldsText` 会对大多数文本误判成「用户已改动」，进而跳过恢复并静默丢掉原内容）。
2. `smto_probe.go`：`SendMessageTimeoutW` 的语义，只用本进程自己创建的隐藏窗口。结果：① 同线程直投、窗口过程返回 0 时 `ret = 1`（`result = 0`），说明产品用 `ret == 0` 当失败判据不是假警报，窗口过程返回 0 不会被误判为失败；② 跨线程投给一个不泵消息的线程时 `ret = 0`、`GetLastError` = 超时（1460）、耗时 311ms（阈值 300ms），目标线程 1.5s 后开始泵消息，那条已超时的 WM_CHAR 始终没有到达（处理次数 0）。也就是说「报失败」与「确实没送到」在这里是一致的，不存在「超时后字符又补投」的乱序风险。

## 已核查、未发现问题的部分

- 内存与句柄：所有 `unsafe.Pointer → uintptr` 转换都写在 `Proc.Call` 的参数表达式里（win32_keyboard.go:109、159；win32_clipboard.go:106、134、167、252、273；win32_window.go:89、98、190、222；win32_instance.go:55；win32_msgbox.go:35-36；win32_webview2.go:70-71），没有「先存进 uintptr 变量再传」的写法。`GlobalAlloc` 的失败与 `GlobalLock` 失败路径都配了 `GlobalFree`（win32_clipboard.go:96-103、267-272、275-278），`SetClipboardData` 成功即交出所有权、失败即自己释放（:108-112、275-278），`GetClipboardData` 拿到的句柄只读不释放、锁后必解锁（:120-135、153-168、237-253），与文档要求一致。`instanceMutex` 句柄按注释常驻到进程结束（win32_instance.go:25-27），是有意为之。
- 错误处理：五个注入方法的成功判据都落在返回值上（`SendInput` 比较实际事件数，win32_keyboard.go:97、103-113；WM_CHAR 比较 `SendMessageTimeoutW` 的返回值，:159-162），没有 `defer` 吞错误或可能 panic 的写法（`:90`、`:119`、`:177`、`:217`、`:296` 的 defer 都是非 nil 的 `LazyProc.Call`）。
- 焦点与窗口：`Foreground.Sample` 只调一次 `GetForegroundWindow`，标识、标题、是否自身都从这一次结果派生（win32_window.go:207-217），符合「采样必须一次读全」的要求；`windowTitle` 依赖 `GetWindowTextW` 对跨进程窗口返回缓存标题的特性，与文档表述一致。`focusedHWND` 用前台窗口的线程 ID 调 `GetGUIThreadInfo`、`cbSize` 按 `unsafe.Sizeof` 填（:189），用法正确；按 AGENTS.md，它只用于 WM_CHAR 寻址、不参与漂移判定，这一点在 internal/typing 的调用处也一致。`RetrySetIcon` 的延迟重试与窗口销毁之间没有 use-after-free：HWND 只是句柄值，三个调用对失效句柄都只是失败返回，且进程内只有一个顶层窗口，5 秒内不存在句柄被回收复用的对象，所以不构成缺陷。
- 单实例守卫：不存在 TOCTOU，`CreateMutexW` 是原子的「创建或打开」；异常退出也不残留，句柄随进程关闭，对象随最后一个句柄销毁（CreateMutexW 文档明说），不存在「上次崩了这次打不开」；`Local\` 前缀按注释是多用户会话各自一个实例，是有意设计。
- 剪贴板：`skippableFormat` 的判据（句柄型格式、块内含句柄、所有者绘制/私有显示/GDI 对象族要跳过，程序私有格式族不跳过）与它自己的定义一致，测试也覆盖了边界；读写 4×50ms 与恢复 8×100ms 两档重试按注释是有意分开的；`Snapshot` 返回 nil（打开失败）与空切片（剪贴板为空）的区分，与 internal/typing/typing.go:517-525 的消耗方式吻合。`HoldsText` 用原始字节比对、`GetText` 在 NUL 处截断，两者分工正确。
- 常量与平台约束：`^uintptr(13)`/`^uintptr(33)` 的类图标索引有真建窗口读回的实测用例（win32_test.go:311-335，本次也跑过并通过），`HWND_TOPMOST`/`HWND_NOTOPMOST` 同理写成 `^uintptr(0)`/`^uintptr(1)`；8 个文件的 build tag 完全一致，linux 与 386 下都按预期「exclude all Go files」；唯一有最低版本要求的 API 是 `GetDpiForWindow`（Windows 10 1607+），与 README 声明的系统要求（Windows 10/11）不冲突，`ScaledForDPI` 对调用失败返回 0 的情况也做了 96 DPI 兜底（win32_window.go:31-37）。
- 明确不报为缺陷的有意设计：`^uintptr(n)` 表达负常量、文本直投与 SendInput 不混用、`skippableFormat` 的取舍、快照重试档位、`HoldsText` 按原始字节比对、单实例守卫的 `Local\` 命名空间与「守卫失败时放行」的初衷，以及启动路径的两道闸门（预检 + 判空）与 `MsgWebView2Missing`/`MsgWebView2InitFailed` 的分工。预检把「查询失败」也当成「不可用」是注释里写明的选择（win32_webview2.go:37-40），代价是运行时明明装好但查询被策略挡下时会给用户看「缺少运行时」这条不完全准确的理由，因为文案已冻结、改动面也不小，这里只作记录，不算缺陷。
