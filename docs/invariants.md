# 非显而易见的不变量与踩坑记录

本文件从 AGENTS.md 拆出（2026-10），2026-10-20 按专题重组并去掉了版本演进叙事。
**代码注释里的"见 docs/invariants.md「某小节」"都指向这里**：搜标题比记文件名快。
只写"只读代码推不出来、又必须知道"的结论；逐次实测的完整留档在 git 历史与 release-notes。

## 目标窗口与轮询

- **预览 = 当前前台窗口**：100ms 节拍采样，窗口标识或标题一变下一拍就刷新；执行时锁定当时的
  顶层前台窗口标识并贯穿到终态。用户定调"所见即所选"，不要加"排除自身/显示占位"之类的过滤；
  倒计时结束焦点仍在 Type 自身时报错并零注入。
- **前端轮询是自续链条**：`startPolling` 必须以定时器点火（`setTimeout(0)`），不能直接调用
  `tick`——tick 末尾用 `pollTimer !== null` 判断链条继续，而它只在受保护的分支里赋值，
  直接调用会让首拍后断裂（症状：预览不跟随、完成后启动键卡死）。恢复可见/焦点时重启链条（页面隐藏的那次
  visibilitychange 不重启），作为 WebView2 挂起定时器的兜底。
- **链条身份靠代数令牌，不靠 `pollTimer` 是否为空**：`stopPolling` 递增 `chain`，旧那拍回来
  先比 `my !== chain` 就返回；旧代的响应一律不写状态、不续链条。回归用例在
  `frontend/test/typingTask.test.ts`。

## 焦点锁定与漂移防护（v1.5.3）

- **采样必须一次读全**：`Foreground` 接口只有 `Sample()`（标识 + 标题 + 是否自身）。拆成多个
  方法会在两次调用间隙发生切换时得到矛盾组合。`TargetID` 是不透明标识，业务层只做相等比较。
- **判定用顶层前台窗口标识，不用标题**（标题会重名、会中途变化）；也不用 `focusedHWND()`——
  那是 WM_CHAR 的寻址手段，与"还是不是同一个窗口"无关。
- **守卫位置**：逐字符路径在每次注入前；剪贴板路径在写入前、粘贴前各一次，并在 `SendPaste`
  返回后**立刻**复检（不是等 200ms 稳定等待之后：用户看到粘贴成功后才切窗口是正常操作）。
  三条中止文案各不相同，见 behavior-contract；中止路径照旧恢复快照。
- **倒计时 100ms 一拍**：等待走可注入的 `s.sleep`，别用 `time.Ticker`（测试靠假时钟推进）；
  每拍只采样，秒边界或内容变化才写状态，否则会凭空多出每秒 10 次状态写入。
- **标题每秒刷新**：不要"优化"成只在窗口标识变化时才读标题，同一窗口的标题变化就再也不更新。
  跨进程顶层窗口标题由系统缓存，不会因目标进程无响应而阻塞。
- **判定只到窗口这一层**：窗口内部换输入框、浏览器换标签页看不见（实测多标签浏览器窗口树
  完全不动）。不要改用"焦点子窗口"或窗口标题当辅助判据：前者让 Tab 跳格/验证码跳格误停，
  后者被网页编辑器常改的标题带偏；要覆盖得上 UI Automation，成本与稳定性风险都高。
- 已知残余：检查与注入之间存在毫秒级间隙（字符间隔 8~16ms），切换恰好发生在其中时最多漏进
  一两个字（README 已知限制）。

## 任务槽与并发启动（v1.5.6）

- **"启动成功"与"占住任务槽"是同一个事实**：判定与占领同在 `s.mu` 临界区，`Start` 返回 nil
  即已占住。拆成"先读标志、goroutine 自己再认领"会让两路并发同时通过（实测 300 轮里 246 轮
  把同一段文本注入两遍）。
- **"有没有任务在跑"只有一个权威：`s.prevTask`**。要等上一任务，等它自己的
  `taskSlot.done`，不要等共享标志（那是等自己，必然误报超时）。
- **"这次取消是不是冲我来的"靠取消标志的前后两次读**：采样前为 true 的是分配给上一任务的取消
  （本次是"取消后立即重启"，不该被拦）；采样前 false 之后变 true 的必须认。别改用
  "取消计数器 + 基线"：两种取消在计数上长得一样，越绕越容易错。
- **取消标志不在 `Start` 清**，留给 `beginRun` 在等到上一任务停手后清。两者之间有亚毫秒级窗口
  （Start 先占槽、goroutine 才跑到清标志），GUI 点不出，产品侧判定可接受；别试图用"看代数再清"
  来堵（实测无效，窗口只是变窄）。
- **被判"出发前就取消"的任务必须自己写终态**（带 `gen` 守卫），否则界面停在倒计时上永远不动。
- **`Start` 里的前台采样放在锁外**：`Foreground.Sample` 是 Win32 调用，目标进程无响应时回不来，
  抓着锁采样会把用户的取消一起冻住；`beginRun` 等上一任务时也不许持锁。
- **超时终态写入必须带 `gen` 守卫**，不能写成 `s.taskGen.Load()`（拿自己和自己比、恒真，
  会把"启动失败：上一任务未能及时退出"盖到在途的新任务上）。回归用例走生产路径钉住。
- **`Cancel` 不覆盖已经有结局的状态**（success / error，判据取状态而不是任务槽）：硬覆盖会把
  已经全部送达的任务说成"已取消"，用户再点一次就把同一段文本打了两遍。
- 回归用例在 `internal/typing/typing_test.go`；改这块前后都要跑 `-race`。

## 文本直投（v1.5.0，WM_CHAR 文本层注入）

- 带补全的在线编辑器有两个按键层行为会打乱注入：补全弹窗把空格/回车/Tab 改写成"接受候选"；
  括号自动配对（闭括号"跳过"行为因编辑器而异，Backspace 方案会误删，不存在状态安全的按键序列）。
  `WM_CHAR` 注入不产生任何按键事件，字符走 `SendText`（文本层）后逐字符原样落盘。
- **Tab 可以走文本层**（Chromium 里 `\t` 原样插入制表符）；**换行不行**（`\n`/`\r` 属控制字符
  会被过滤），换行必须走真按键 `sendEscaped(SendEnter)` = Esc + 20ms + 回车。
- **不混用原则**：字符走 `SendMessage`（W 同步直投），按键走 `SendInput`（排队），两者队列不同；
  冷启动窗口（刚 spawn 的渲染组件重建中）会丢/乱事件，实测排障要等窗口热机。
- `sendEscaped` 是状态无关设计（弹窗开着则关闭、没开基本无操作），勿改成"探测后再 Esc"；
  默认关闭：无弹窗目标里凭空 Esc 有副作用（浏览器全屏退出、Vim 退出插入态、关页面弹窗）。
- 仅逐字符路径生效；`textDirect` 是 `startTyping` 第 4 参；发送目标沿用 `focusedHWND()`。
- **拿不到焦点子窗口就退化为按键注入**（v1.5.6）：顶层容器窗口会把 `WM_CHAR` 丢掉而
  `SendMessageTimeout` 照样返回成功，"一个字都没进去"会被报成注入成功。`sendChar16` 判的是
  SendInput 的入队计数，目标侧渲染环节的问题一律看不出来。

## 注入通道的进程内读回（2026-10 实测）

- **结论**：全角标点走 SendInput 退化路径，在标准 EDIT 控件上没有观察到重复与吞字（6 段文本 ×
  3 条通道，读回逐字节相等，端到端 13~53ms）。取证是测试进程里真建 EDIT、整串打进去再用
  `WM_GETTEXT` 读回比对。
- **这套取证能看见错**：把"全角字符多发一次"做变异，读回立刻重复并让用例变红；另有阳性对照守
  "重复/吞字"两种对称错法。
- **边界**：标准 EDIT 不等同于带 IME 的输入框，也不等同于 Chromium 系编辑器；结论只表达
  "在 EDIT 这一层没有复现"，不表达"那个 bug 不存在"。换目标仍要用 `tools/wmcharprobe` 实测。
- 观测未进断言：`\n` 走 WM_CHAR 时多行 EDIT 会收成 `\r\n`（控件行为，解释了产品为何先剔除
  `\r`），但只要 `focusedHWND()` 没取到焦点子窗口就会退化，落点不稳定。
- **夹具六条硬教训**（改这组用例前先看）：① 建窗必须 `runtime.LockOSThread()`，跨线程同步
  SendMessage 会永久阻塞；② 顶层窗口必须带 `WS_VISIBLE`，否则焦点链建不起来；③ 注入后要一直
  泵到读出来为止，定长泵会读到空并误判"丢字"；④ 给控件发消息走 `SendMessageTimeoutW`，
  `WM_SETTEXT` 的 lParam 必须指向真宽字符串（空指针会把 EDIT 挂死）；⑤ `SendMessageTimeout` 的
  返回值只说送没送成，真正结果在 `lpdwResult`（把它当返回值用会恒读到 1，恰好又是 `HTCLIENT`）；
  ⑥ 每个用例的窗口类名必须不同（同类名 `RegisterClassExW` 报 Class already exists，随机红）。
- **焦点没确认时不注入**：`focusEdit` 先把窗口顶到前台并核对 `focusedHWND` 就是那个 EDIT，
  否则宁可跳过——`focusedHWND` 取的是全局前台窗口，认错了内容会真的打进别的程序。
  环境顶不上前台时按真剪贴板用例的同款口径跳过并写明读到什么；CI 上不许停留在这个分支。
- **前台是全局资源，随时会被抢走**：每例开工前还要 `ensureFocus` 再确认；整串没等到时先怀疑
  焦点而不是内容。断言（整串逐字节相等）从头到尾没有放宽。

## 排障方法（血的教训）

- **先沿数据流找源头，再怀疑表现层**：现象在界面，根因常在状态值（本仓库真栽过两次：空进度
  轨道是后端初始 `progress: 0` 而不是 `-1`；两次"以为 CSS 没生效"其实都不是 CSS）。
  排障顺序：端点的真实取值 → 消费方怎么解释它 → 才轮到样式/渲染。
- **界面元素不该在时，先问它是被哪个值拉活/撑开的**：`.progress-wrap` 高度来自 `.active`，
  `.active` 来自 `progress >= 0`，`progress` 来自后端状态；顺着链读三处代码比对着截图量十次像素省事。
- **冻结契约文件本身就是现状说明书**：契约测试里的字面量 JSON 与签名表是"程序当下真实契约"的
  权威记录；现象与它们对不上时，先怀疑实现对不上契约。
- **取证工具本身也会骗人**：抓真窗口前确认没有残留进程（单实例守卫会让新进程静默退出，抓到的是
  旧窗口）；像素判据要能区分相邻元素。结论要落到"哪个值/哪一行代码产生它"，而不是"图上看起来像"。

## 其它不变量

### 窗口尺寸与 DPI
- **窗口尺寸、DPI 与显示器工作区已由 mygo 接管**：装配层用 `WindowOptions` 的
  `Width/Height`（540×480，DIP）、`UseContentSize`、`Min/MaxWidth/Height`（540×480 ~ 648×540）、
  `FitContent` 与 `DisableResize` 一次性声明，框架按显示器缩放换算物理像素并居中。
- `DisableResize` 在 Windows 上同样清掉 `WS_THICKFRAME|WS_MAXIMIZEBOX`，与旧的
  `SetSize(HintFixed)` 等价；固定尺寸的判据改为 `Window.IsResizable() == false`。
- DPI 感知仍由 `tools/mkres` 生成的 manifest（PerMonitorV2）声明，mygo 在 Windows 启动
  时另调 `SetProcessDpiAwarenessContext`；两者一致，装配层不再手工换算逻辑尺寸。
- 旧的 `ScaledForDPI` / `SetWindowClientRect` 与 `internal/win32/win32_size.go` 已删除：
  物理像素与 DIP 的换算是框架内部职责，产品代码不再直接调 `SetWindowPos`。

### 内容缩放与界面几何
- **内容缩放校正已由 mygo 的 `FitContent` 接管**：页面把内容尺寸经 bridge 报给框架，
  框架按缩放比把客户区摆到设计尺寸并在工作区内居中；`reportViewport` 绑定、
  `frontend/src/viewportReport.ts` 与 `internal/viewfit` 因此整体退役。
- 实测（125% 缩放显示器）：`devicePixelRatio = 1.25` 时 CSS 视口恰为 540×480（设计尺寸），
  窗口加载前后 `ContentBounds` 一致、没有二次摆动。这是迁移前靠"量回来再适配"达到的
  同一效果，现在由框架保证。
- **启动缩放为 100%，但不禁用滚轮**：装配层 `PageOptions.ZoomFactor: 1`，框架创建窗口时
  无条件复位一次 `ZoomFactor`，清掉用户数据目录里残留的 per-origin zoom，保证首帧就是
  设计尺寸；`IsZoomControlEnabled` 保持开启，Ctrl+滚轮与 Ctrl+± 仍可用。用户主动缩放后
  CSS 视口会变小，由低视口媒体查询兜住裁切；框架记录的 zoom 仍是 1，但
  `Min/MaxWidth/Height` 兜住了窗口尺寸，不会乱跳。系统 DPI 仍由框架换算，不受此约束影响。
- 装不进工作区时由 `MaxWidth/MaxHeight` 与框架的工作区约束兜界，界面仍可滚动；
  `.section` 下限 217px、各行列 `flex-shrink: 0` 的兜底样式保留。CSS 视口跌破 540×480
  时 `style.css` 的低视口媒体查询放开根元素下限、把输入区下限收到 120/98px，
  只在该档生效，540×480 及以上一像素不变。
- 复现配方（不改代码）：`WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--force-device-scale-factor=1.75`
  + dev 构建，观察视口是否仍等于设计尺寸。
- **`--ui-scale` 只缩放数值型细节**：内边距、间距、圆角、控件高度、图标边长；`border-width` 与
  `font-size` 永不参与（小数边框会出虚边，字号是布局输入）。默认与 540 宽档恒为 1；基准宽度
  540 必须等于 `WindowOptions.MinWidth`（跨端契约测试从 `main.go` 的 `minWindowW` 核对）。
- 状态栏高度写死一行（`37.5px` = `calc(13px * 1.5 + 16px + 2px)`），文案 nowrap + 省略号。
  判断文案是否被截断不能只看 `scrollWidth > clientWidth`（两者都取整，差 1~2px 会假报），以截图为准。

### 界面玻璃与主题

- **玻璃只在页面内部**：半透明卡片靠 `backdrop-filter` 与 `#app` 的环境层（冷光 + 等高线 SVG）
  成立；宿主窗口保持不透明，不启用 WebView2 透明背景或 DWM 亚克力。环境层被移除时卡片仍有
  半透明底色，只是失去可模糊的颜色。
- **漂移与减动效**：环境层以 26s 缓慢漂移，`prefers-reduced-motion: reduce` 时静止；动画只改
  transform，不参与布局，也不改变任何几何判据。
- 调色板用 `oklch()` / `color-mix()`，玻璃用 `backdrop-filter`；WebView2（Chromium）均支持。
  页面仍无 web font、无外部图片，离线单文件不受影响。

### 剪贴板快照与恢复

- **写入失败必须区分原内容是否保全**：业务接口为
  `typing.Clipboard.SetText(text string, snap []ClipboardFormat) (written bool, preserved bool)`。
  `written` 表示写入是否成功；`preserved` 仅在写入失败时有意义，true 表示未改动剪贴板，或已在
  同一次打开剪贴板期间恢复快照；false 表示已修改且回滚失败。写入成功时不解读第二个结果。
- **写入准备必须在清空之前完成**：写入文本所需内存在 `EmptyClipboard` 之前准备好，准备失败不改
  剪贴板。部分写入失败必须在仍持有剪贴板时回滚，不许关闭后再尝试恢复；所有退出路径都要关闭
  已打开的剪贴板，发 `Ctrl+V` 前不得持有剪贴板。
- **正常恢复的原子边界是同一次打开剪贴板**：业务调用 `Clipboard.RestoreSnapshot(snap, injected) bool`，
  在同一次 `OpenClipboard` 内完成原始字节比较、`EmptyClipboard` 和写回，不得拆成先检查、关闭后
  再打开恢复。写回成功返回 true；确认用户已改动则跳过恢复并返回 true；读不到或恢复失败返回 false。
  `RestoreSnapshotRaw` 仅保留给现有真剪贴板测试收尾，业务不再调用。
- **读取结果仍须保留 `(holds, known)` 两种事实，不许压回一个 bool**：`HoldsText` 从业务接口删除，
  按字节比对下移为已打开剪贴板下的内部 helper。「读不到」（打开失败、GlobalLock 失败、GlobalSize
  为 0）必须按「没恢复」上报（原内容可能已丢）；「剪贴板已被用户改动」则应跳过恢复，剪贴板归用户所有。
- **恢复失败必须可见**：`RestoreSnapshot` 返回 false，或 `SetText` 失败且 `preserved == false`，
  均须沿用既有未恢复提示，底层写回结果不得吞掉。成功路径用「输入完成，但剪贴板未恢复，原内容可能已丢失」，
  失败路径在具体原因后追加既有未恢复后缀。恢复比读写更耐心（4×50ms 对 8×100ms），因为失败后
  原内容可能已丢。用户已复制新内容时跳过恢复不算失败。
- **快照不完整时不许碰剪贴板**：`Complete` 是"能不能拿去恢复"的唯一判据。恢复会先
  `EmptyClipboard`，拿残缺快照去恢复等于把没抄到的格式永久销毁，而用户看到的是"输入完成"。
  这种情况整条剪贴板路径不作数，退回逐字符输入。
- **剪贴板路径失败之后不再退到逐字符**：快照完整后，一旦尝试写入，失败就按原原因结束，即使
  剪贴板未改动或已回滚也不再重打。粘贴已发出时再打一遍可能注入两遍，也会用泛化原因盖掉
  「粘贴未生效：...」这类具体原因。
- **快照只跳过"不是普通内存块"的格式**（句柄型 CF_BITMAP/CF_PALETTE/CF_ENHMETAFILE、块内含句柄的
  CF_METAFILEPICT、所有者绘制与私有显示格式、GDI 对象族 0x0300-0x03FF）；CF_PRIVATEFIRST..LAST
  不跳过。判据是"恢复时会不会写回失效句柄或垃圾字节"，别退化成"全抄"或"只抄白名单"。
- **剪贴板为空与"读不到"必须区分**：nil 表示未取得可恢复的原状态，不许当成空快照清空剪贴板；
  空剪贴板用非 nil 空切片表达（`Formats = []`、`Complete = true`，可恢复成空）。剪贴板只有位图等
  被跳过的格式时仍是"原状态未知"，退回逐字符；若把它当空剪贴板，恢复会把用户的位图永久清掉。
- 剪贴板恢复守卫用原始字节比对而不是 `GetText` 字符串比较（CF_UNICODETEXT 内嵌 NUL 时字符串会在
  NUL 处截断而误判）。

### 启动与单实例
- **启动三道闸门**：① `mygo.WebViewAvailable()` 在创建窗口前预检 WebView2 Runtime，
  缺失给 `MsgWebView2Missing`；② `NewWindow` 在控制器异步创建失败时可能 panic，
  `App.WhenReady` 回调里的 recover 接住并给 `MsgWebView2InitFailed`；③ `BindAs` 出错
  同样是 panic，由 `bindService` 的 recover 给 `MsgBindFailed`。三条提示都走自研
  `MessageBoxW`：此刻 `App.Run` 尚未开始或刚失败，框架的 `Dialog` 不可用。
- **宿主进程完整性级别偏低时 WebView2 拒绝创建控制器**：给 exe 所在目录打 Low 完整性标签
  （`icacls <目录> /setintegritylevel Low`）后必崩、拷到别处就正常。排查先看 `icacls` 输出；
  修复是在用户自己的终端执行 `icacls <目录> /setintegritylevel Medium`。
- 复现"机器上没有 WebView2"：把 `WEBVIEW2_BROWSER_EXECUTABLE_FOLDER` 指向不存在的目录再启动；
  期望弹出「Type 无法启动」提示框，stderr 为空。
- **单实例改用 `mygo.App.RequestSingleInstanceLock()`**：第二个实例把命令行参数转发给
  第一个后自行退出；第一个在 `OnSecondInstance` 里**只 `FlashFrame`，绝不 `Focus`** ——
  Type 靠前台窗口锁定注入目标，此刻抢焦点会触发「输入中断：目标窗口已切换」或
  「未切换到目标窗口」。旧的 `CreateMutexW` 加模态提示框方案已删除。
- 产品仅支持 `windows && (amd64 || arm64)`：386 被刻意禁止（INPUT 结构体手工填充仅匹配 64 位 ABI）。
  业务层 `internal/typing` 无构建约束，可在任何系统编译；但非 Windows 上
  `go test ./...` 会**明确失败**（win32 与 cmd 被构建约束排除），所以产品验证都在 Windows 上。

### 取证与工具边界

- **抓真窗口取证的坑**：① 抓之前先确认没有 Type 进程在跑（单实例守卫会让新进程静默退出，
  接着抓到的是旧进程窗口，"改了没生效"的结论完全是假的）；② 宿主脚本先声明 DPI 感知，否则
  `GetClientRect` 返回虚拟化逻辑坐标、抓出来左上角被裁；③ PowerShell 宿主脚本用纯 ASCII 或
  带 UTF-8 BOM（无 BOM 的中文脚本被 5.1 按 GBK 解码吞引号）。
- **`tools/equivcheck` 的视野边界**：只比函数体的 token 序列，签名/参数顺序/返回值/包级常量/
  结构体字段与 tag 全在视野之外。按 token 而不是"删掉全部空白"比对是刻意的（后者会把字符串
  字面量内部的空格删掉，`剩余 N 秒` 与 `剩余N秒` 会被判成一致）。所以时序常量、文案改动必须靠
  字面量断言；别拿它的"一致"当零行为变化的唯一证据。旧侧一次只读 `--old-file` 一个文件，
  跨多文件重构要逐个跑；取函数体必须用 `go/parser`（旧的正则实现遇函数内行首大括号会提前截断）。
- **Win32 负常量以 `^uintptr(n)` 表达**（`^` 是按位取反不是取负）：看着像笔误但不是；这地方
  极易被误读误改。注入读回夹具里的 `wsOverlappedWindow` 之类常量照此写法，别只写
  "常量等于某值"式的自我复读。
- **Win32 怪癖的注释保留在 `internal/win32` 的实现内**（全角标点 WM_CHAR 绕行、GDI 句柄型格式
  跳过、剪贴板快照的完整性判据），它们是本代码库最有价值的文档，重构时勿删。
- **发布构建一律带 `-trimpath`**：少了它产物里嵌着构建机绝对路径（实测 exe 里能搜到仓库路径），
  既漏环境信息，也让"同样源码编出同样文件"做不到。
- **注入失败必须可见**：`TextInjector` 五个方法都返回 bool（SendInput 被 UIPI 拦截时整体返回 0；
  WM_CHAR 直投的 `SendMessageTimeout` 超时/失败返回 0）。别把返回值改成 void。
- **界面以 `about:blank` 加载，没有可用的源**：装配层走 `Page.LoadHTML(web.IndexHTML, "")`，
  文档 origin 为 null，`localStorage`/`sessionStorage` 一律抛 `SecurityError`（实测仍是
  `blocked:SecurityError`）；所以主题的手动选择不跨启动保留（README 已知限制）。
  要"记住偏好"得走宿主侧存储并经 shim 暴露给页面，别直接调 localStorage 靠 try/catch 兜。
- **主题的首帧接线有三处约定**：存储键写在两处（首帧内联脚本读它、`useTheme` 写它）；内联脚本必须
  排在入口脚本之前（挪进 Vue 等于首帧闪白）；"该用哪套主题"的判定只许有一处（契约测试连
  "useTheme 不许读存储"一起钉住）。
- **Node 主版本只写在 `.node-version` 一处**（现为 24）：它决定 vite/rollup 的产出字节，换版本后
  入库的 `internal/web/dist/index.html` 与重新构建的产物对不上，漂移检查会报不一致。
- 提交信息：中文一行式主题 + 正文说明要点。
