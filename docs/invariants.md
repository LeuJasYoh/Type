# 非显而易见的不变量与踩坑记录

本文件从 `AGENTS.md` 拆出（2026-10），内容逐行搬运、小节标题未改。
**代码注释里写的"见 AGENTS.md「某小节」"现在都在本文件里** —— 搜小节标题比记文件名快。

## 非显而易见的不变量与踩坑记录

### 目标窗口与轮询

- **目标窗口语义**：预览 = 当前前台窗口（100ms 节拍采样，窗口标识或标题一变
  下一拍就刷新，秒数仍每秒一档；用户切到哪个窗口就显示哪个，含 Type 自身；
  不要引入"排除自身/显示占位"之类的过滤）；执行时锁定当时的**顶层前台窗口标识**
  （不是标题）并贯穿到执行与终态状态；倒计时结束焦点仍在 Type 自身时报错并零注入。
  用户明确定调：所见即所选，程序只负责刷新
- **前端轮询是自续链条（v1.4.0 的教训）**：`startPolling` 必须以定时器点火
  （`setTimeout(0)`），不能直接调用 `tick`：tick 末尾用 `pollTimer !== null`
  判断"链条继续"，而 pollTimer 只在该判断保护的分支里赋值，直接调用会让链条
  第一拍后断裂、状态永不刷新（症状：预览不跟随、完成后启动键卡死）。
  恢复可见/焦点时无条件重启链条，作为 WebView2 挂起定时器的兜底
- **链条身份靠代数令牌，不靠 `pollTimer` 是否为空**：`pollTimer` 是共享变量，
  一拍在飞时重启链条，旧那拍回来会看到非空的 `pollTimer`，于是给自己再排一拍，
  两条链同时轮询（每秒请求数翻倍）；用户此时点取消，旧那拍还会把"已取消"盖回
  倒计时，而链条已停、界面不会自愈。`stopPolling` 递增 `chain`，tick 回来先比
  `my !== chain` 就返回，旧代的响应一律不写状态、不续链条。回归用例在
  `frontend/test/typingTask.test.ts`（⑤ 迟到响应、⑥ 双链条两条），
  改轮询前后都跑一次 `cd frontend; npm test`

### 焦点锁定与漂移防护（v1.5.3）

- **采样必须一次读全**：`Foreground` 接口只有 `Sample() ForegroundSample`
  （标识 + 标题 + 是否自身）。拆成三个方法会在两次调用的间隙发生切换时得到
  互相矛盾的组合：展示的标题不是锁定下来的那个窗口，或"非自身"判定与目标锁定
  之间用户切回 Type，让守卫从第一步就失效。`TargetID` 是平台无关的不透明标识，
  业务层只做相等比较，不得解释其内容
- **判定用标识，不用标题**：标题会重名、会中途变化（浏览器切标签、文档改名）。
  判据是**顶层**前台窗口标识（`GetForegroundWindow`）：输入法候选窗、补全弹窗、
  同一程序内换输入框都不是顶层变化，因此不会误触发；反过来，目标程序自己弹出
  的模态窗口会让顶层窗口改变，按设计中止输入
- **不用 `focusedHWND()` 做漂移判定**：它是 WM_CHAR 的寻址手段（取前台线程的
  焦点子窗口），与"还是不是同一个窗口"无关
- **守卫位置**：逐字符路径在每个字符注入前比对；剪贴板路径在写入剪贴板前、
  粘贴前各比对一次，并在 `SendPaste` 返回后**立刻**复检（不是等 200ms 稳定
  等待之后，用户看到粘贴成功后才切窗口是正常操作，那时不该报失败）。三条
  中止文案各不相同，见 behavior-contract.md；剪贴板快照恢复流程在中止路径上照旧执行
- **倒计时是 100ms 一拍**（每秒 10 拍，总时长仍是 delay 秒），取消与切窗的响应
  都缩到一拍内。等待必须走可注入的 `s.sleep`，不要用 `time.Ticker`（测试靠
  假时钟推进）；**采样与写状态分离**：每拍只采样，秒边界或内容变化才写状态，
  否则会凭空多出每秒 10 次的状态写入
- **标题保持每秒刷新**：不要"优化"成"只在窗口标识变化时才读标题"，同一窗口
  的标题变化（浏览器切标签、文档改名、未保存标记）就再也不更新了，与 README
  的预览承诺不符。跨进程顶层窗口的标题是 `GetWindowTextW` 取回的缓存文本，
  系统保证它不会因目标进程无响应而阻塞，所以每次采样都读标题是安全的
- 已知残余：检查与注入之间存在毫秒级间隙（字符间隔 8~16ms），切换恰好发生在
  其中时最多漏进一两个字；已作为已知限制写进 README
- **判定只到窗口这一层，别轻率"加固"**：窗口内部的目标变化（网页/编辑器里换
  输入框、浏览器换标签页）看不见：实测一个开着 7 个标签页的浏览器窗口只有
  1 个内容子窗口，切标签页时窗口树完全不动。改用"焦点子窗口"判据只对原生程序的
  输入框切换有效，对浏览器仍无效，却会让表单 Tab 跳格、验证码自动跳格这类正常
  用法误停；用窗口标题当辅助信号更糟（网页编辑器常把首行内容写进标题，输入中就
  会变）。要真正覆盖得上 UI Automation 做元素级比对，成本与稳定性风险都高，
  当前决定是维持现状并写进 README 已知限制

### 任务槽与并发启动（v1.5.6）

- **"启动成功"必须与"占住任务槽"是同一个事实**。`Start` 里读一次运行标志就放行、
  再由新开的 goroutine 自己去认领任务，是两次几乎同时到达的启动都能通过的原因：
  实测两路并发 300 轮里 246 轮把同一段文本注入两遍。判定与占领现在同在 `s.mu`
  临界区里完成，`Start` 返回 nil 即已占住
- **"有没有任务在跑"只有一个权威：`s.prevTask`**。曾经同时用 `runningFlag` 与它，
  结果新任务一进来就置上了运行标志，而"等上一任务让位"也靠这个标志判断，等于在等
  自己，于是必然误报超时（v1.5.6 修复过程中踩到，症状是"取消后立刻重启"变成
  `启动失败：上一任务未能及时退出`）。要等上一任务，等它自己的 `taskSlot.done`
- **"这次取消是不是冲我来的"靠取消标志的前后两次读**。采样前读一次、占领时
  再读一次：采样前就是 true 的，那是分配给上一任务的取消（它正靠这个标志退出），
  本次启动是"取消后立即重启"，不该被它拦下；采样前 false、之后变 true 的，
  是用户在这次采样的间隙里点的取消，必须认。**别改用"取消计数器 + 基线"那套**：
  取消发生在上一任务还活着的时候、新任务又从采样窗口里进来，两种取消在计数上
  长得一模一样，要分辨就得再引入一个"轮次"标识，越绕越容易错（v1.5.6 在这儿
  反复栽了四次）
- **标志一律不在 `Start` 里清**：清了会同时踩两个坑 —— 抹掉采样窗口里刚到达的
  取消，以及让"取消后立即重启"的第二次启动被重入判定拒掉。给 `beginRun`（taskrun.go）
  在等到上一任务停手之后再清。
  **这一步与 `Start` 之间有一个亚毫秒级的窗口**（2026-10 独立复核 + 实测定位）：
  `Start` 是在起 goroutine **之前**就占了任务槽的，而任务的 goroutine 要跑到
  `beginRun` 顶部才清标志；用户若在"启动返回"与"任务开工"之间点取消，那次取消
  会被这行清掉（`Start` 的重入判定随即看到 `prev != nil && !cancelFlag`，于是"取消后
  立即重启"被拒成 `已有输入任务在运行中`）。GUI 点不出这个时序，产品侧判定为可接受；
  **但别试图用"看代数再清"来堵**（2026-10 试过，实测无效）：判据读一次代数、再
  `Store(false)`，两步之间照样能被 `Cancel` 插进去，窗口只是变窄没有消失。
  真正被修的是**用例的前置条件**：`TestRestartAfterCancelSupersedesOldTask` 以前
  只等 `waitRunning`（= 槽被占），那不等于任务已开工，于是 30 次独立进程里红 10 次
  （全量跑时前面的用例预热了调度，反而看不出来），CI 会随机红。现在它等"第一次
  sleep 被调用"，即任务真的走进倒计时，用例因此确定性通过（40 次独立进程 0 红）。
  `waitRunning` 的注释也一并改成"只说明任务已被受理"
- **被判"出发前就取消"的任务必须自己把终态说出来**。它接着 `return` 而不写状态的话，
  界面就停在 `Start` 写下的倒计时上，而倒计时不是终态、前端会一直轮询，
  用户看到永远不动的"剩余 N 秒"。写的时候带 `gen` 守卫，已被新一代接管时不插嘴
- **`Start` 里的前台采样放在锁外**。`Foreground.Sample` 是 Win32 调用，目标进程
  无响应时回不来；抓着锁采样会把用户的取消一起冻住（`Cancel` 要取同一把锁）。
  同理，`beginRun` 里等上一任务让位时也不许持锁
- **用超时那条终态写入必须带 `gen` 守卫**，别写成 `s.taskGen.Load()`：那是拿自己
  和自己比、恒真，退位的任务会把"启动失败：上一任务未能及时退出"盖到在途的新任务
  头上，前端在终止态停轮询，用户看到假失败而文本其实已经送进目标窗口。
  **这条长期只有一个假报警器**：`TestSupersededTaskTimeoutDoesNotOverwriteNewerTask`
  自己构造代数直接调 `storeStatus`，钉的是守卫函数本身；把生产侧那行改回自比较，
  41 条用例一条都不红（2026-10 变异实测）。现在由
  `TestSupersededTimeoutWriteIsDroppedOnProductionPath` 走**生产路径**钉住（真任务在
  超时点上以过代身份写终态）；它必须真等满 `yieldDeadline`，两条任务同时起跑时
  "T3 自己的超时"会与"T2 收尾"撞拍而随机红，所以 T3 晚 300ms 起跑——改这条用例前
  先读它的注释
- **`Cancel` 不覆盖已经有结局的状态**（success / error，2026-10 修）：任务写完终态到
  腾空任务槽之间也在这个窗口里，所以判据取**状态**而不是任务槽。硬覆盖的话那句"已取消"
  是假话 —— 内容可能已经全部送达，用户以为没打完、再点一次启动就把同一段文本打了两遍。
  前端配套：`cancel()` 先给即时反馈，再回读一次真实状态，只有读到 success/error 才改口
  （`typingTask.test.ts` ⑩ 钉着；后端用例 `TestCancelDoesNotOverwriteFinishedTask`）
- 回归用例在 `typing_test.go`：`TestConcurrentStartNeverStacks`、
  `TestCancelDuringStartLeavesNoStuckCountdown`（断言终态必须是 cancel 且零注入，
  只断言 isTerminal 会被"吞掉取消后的假成功"满足）、
  `TestRestartAfterCancelSupersedesOldTask`、`TestStartDeadlineWhenPreviousTaskStuck`、
  `TestSupersededTaskTimeoutDoesNotOverwriteNewerTask`。改这块前后都要跑 `-race`

### 文本直投（v1.5.0，WM_CHAR 文本层注入）

- 带代码补全的在线编辑器（在线作业/考试平台的代码框一类）有两个按键层行为会打乱注入的代码：
  ① 补全弹窗把空格/回车/Tab 的键义改写为"接受候选"；② 括号自动配对（键入
  `(` 自动补 `)`，闭括号"跳过"行为因编辑器而异，Backspace 方案会在无配对的
  编辑器里误删字符，不存在状态安全的按键序列）。两个行为都挂在 keydown 层，
  而 `WM_CHAR` 注入不产生任何按键事件，字符走 `SendText`（文本层）后，
  实测逐字符原样落盘、零 keydown、零配对（证据：tools/wmcharprobe 的 char
  模式 c=精确长度/k=0/p=0，keys 模式同文本 p+2、a+1）
- 回车与 Tab 的边界（实测，勿"顺手统一"）：Tab 可以走文本层（WM_CHAR 的
  `\t` 在 Chromium 里原样插入制表符）；**换行不行**，`\n`/`\r` 属控制字符
  会被 Chromium 过滤丢弃，换行必须走真按键（`sendEscaped(SendEnter)`，
  Esc + 20ms + 回车）。这也是文本直投里唯一保留的按键注入
- 不混用的原则：字符走 SendMessage(W 同步直投)、按键走 SendInput(排队)，
  两者队列不同、理论上存在乱序窗口。实测热机窗口下产品时序（每换行
  Esc+20ms+回车，字符间隔 8ms+）内容哈希逐字节一致；**冷启动窗口**
  （刚 spawn 的浏览器渲染组件重建中）会丢/乱事件，实测排障要等窗口
  热机（数秒）再注入，真实使用场景天然满足
- `sendEscaped`（Esc + 20ms + 回车）是状态无关设计：不回答"弹窗在不在"，
  只做两种状态下都安全的动作（弹窗开着则关闭、没开则基本无操作），
  勿改成"探测后再 Esc"。默认关闭：无弹窗目标里凭空 Esc 有副作用
  （浏览器全屏退出、Vim 退出插入态、关页面弹窗）
- 仅逐字符路径生效；剪贴板粘贴不经弹窗劫持，无此逻辑。textDirect 为
  startTyping 第 4 参；发送目标沿用 focusedHWND()（前台线程焦点窗口）
- **拿不到焦点子窗口就退化为按键注入**（v1.5.6）：`focusedHWND` 不再拿顶层窗口
  兜底，取不到焦点子窗口时返回 0，`sendCharUnitsViaWMChar` 据此走
  `sendCharUnitsViaInput`。原因是顶层容器窗口会把 `WM_CHAR` 丢掉，而
  `SendMessageTimeout` 照样返回成功，"一个字都没进去"会被报成注入成功。
  已知盲区（未实测）：全角标点的绕行共用这个函数，而 `SendInput` 对
  U+FF00-FFEF 恰有那个系统级 bug（症状是标点重复、后续字符被吞）。退化之后
  这类字符会怎样没有真机验证过；`sendChar16` 判的是 SendInput 的入队计数，而
  那个 bug 发生在目标侧渲染、入队照样成功，所以**很可能静默出错**而不是报失败。
  要下结论得用 `tools/wmcharprobe` 在真窗口上逐字比对，别只读代码下判断
- 验收/复现页 testdata/completion-guard.html（补全 + 配对开关、逐键日志、
  预期对比、title 遥测）；勿带 `?allow-paste=1` 做 Type 实测，那是停用
  粘贴拦截、供自动化注入的诊断模式

### 排障方法(血的教训)

- **先沿数据流找源头, 再怀疑表现层**（2026-10，一个空进度轨道花了很久才定位）：现象在界面上
  （"启动时空闲态多出一条空进度轨道"），但根因在后端初始状态发错了值（`progress: 0` 而不是
  `-1`），而**仓库里早就写着答案**：`internal/typing/contract_test.go` 的冻结初始状态
  `{"phase":"idle",...,"progress":0,...}` 一字不差地摆着那个错误取值。排障顺序应当是
  **① 端点的真实取值 → ② 消费方怎么解释它 → ③ 才轮到样式/渲染**；反过来从 CSS 倒推，
  会得到"透明度不可靠""合成层有问题"这类看似有理、实际全错的结论（本仓库真栽过：两次归因
  都指向渲染，而 CSS 从来不是原因）。
- **界面上"某个元素不该在"时，先问"它是被哪个值拉活/撑开的"**：`.progress-wrap` 的高度来自
  `.active` 类，`.active` 来自 `progress >= 0`，`progress` 来自后端状态 —— 顺着这条链读三处
  代码就能定位，不必抓图。**读一遍产生该状态的那几行代码，比对着截图量十次像素更省事。**
- **冻结契约文件不只是"改代码时要同步"的清单，它本身就是现状说明书**：`contract_test.go`
  的字面量 JSON、`cmd/type/contract_test.go` 里的签名表，都是"程序当下真实契约"的权威记录。
  现象与它们对不上时，先怀疑实现对不上契约，再怀疑契约本身该不该改。
- 取证工具本身也会骗人：抓真窗口前确认没有残留进程（单实例守卫会让新进程静默退出，你抓到的
  是旧窗口）；像素判据要能区分相邻元素（本轮两次"以为 CSS 没生效"，一次是旧进程、一次是
  把边界线当成了轨道）。**结论要落到"哪个值/哪一行代码产生它"，而不是"图上看起来像"。**

### 其它不变量

- **窗口尺寸只在启动时算一次，之后固定**（`internal/win32` 的 `initialWindowSize` +
  `GetMonitorInfoW.rcWork`；它原叫 `InitialWindowSize`，2026-10 收窄导出面时改小写）：占工作区高度 49%，夹在 540×480 ~ 648×540（逻辑像素），
  宽度由高度按 6:5 推出，位置在工作区内居中。下限是**保守取值**：实测最长的那条终态
  文案在 540 宽下只占一行（446px < 可用 470px），状态栏恒为单行 37.5px，两行要到 496 宽
  才出现（已在官方尺寸之外），所以别按"刚好放下"的更紧数字往下调。
  三条不许顺手加回来的东西：
  ① 不响应 `WM_DPICHANGED`、不做跨显示器跟随（用户明确不要；窗口侧改尺寸与内容侧
  Chromium 改缩放一旦不同步，就是"渲染缩放 ≠ 显示器缩放"的位图拉伸，正是发虚的来源）；
  ② 不放宽窗口样式 —— `SetSize(HintFixed)` 去掉 `WS_THICKFRAME|WS_MAXIMIZEBOX` 是
  "不可拖大"的唯一来源，`TestWindowSizeIsFixed` 读回样式位并用 `WM_NCHITTEST` 命中测试
  钉着（只调 `SetWindowClientRect` 的话窗口仍可拖大，A/B 实测过）。**该用例是 A/B 形式**
  （2026-10 加固）：先用 `WS_OVERLAPPEDWINDOW` 建窗、证明这套取证认得出可拖大（右下角命中
  `HTBOTTOMRIGHT`=17），清位（+`SWP_FRAMECHANGED`）之后才断言认不出。此前它拿无边框样式
  建窗（本来就没有 `WS_THICKFRAME`）、命中测试又按**客户区**取点（落在客户区里面，任何带
  边框的窗口都回 `HTCLIENT`），两条断言恒真，等于没装 —— 修它是加固，不是放松；
  ③ 不在运行时调 `SetProcessDpiAwarenessContext`。
  `SetWindowClientRect` 把客户区尺寸反推成窗口矩形 —— 少了这一步客户区会比目标矮一个
  标题栏（界面底部被切，而表面与客户区仍一致，不会发虚、更难发现）。2026-10 起这条
  反推有两处加固：走 `AdjustWindowRectExForDpi`（非 DPI 版在"系统 DPI ≠ 显示器 DPI"的
  机器上按错的那个算边框，客户区差十几个物理像素），并**读回复核**、差 1 像素以上按差额
  再摆一次。`AdjustWindowRectExForDpi` 的参数是 5 个 `(lpRect, dwStyle, bMenu, dwExStyle, dpi)`：
  只传 4 个的话 dpi 取到寄存器残留值，一次实测把 600×400 的请求摆成了 1540×941 的客户区。
  实测留档（125% / 120 DPI 机器）：请求 864×719 物理 → `GetClientRect` 读回 864×719，
  折回逻辑 720×600 逐像素相等，说明 WebView2 渲染表面与客户区同源、无重采样
  （那次实测时的上限是 720×600，后来上限收到 648×540，机制不变）
- **内容缩放（WebView2 的 rasterization scale）≠ 窗口 DPI 缩放时，界面必然挤爆**（2026-10 修，
  用户报告"界面挤成一团、选项行整行不见"）：尺寸等式只有一条，
  **CSS 视口 = 客户区物理像素 ÷ 内容缩放**。窗口尺寸用的是显示器 DPI，内容缩放却由 WebView2
  自己定（官方口径是"显示器缩放 × 用户文本大小"，还叠着页面缩放），两者不等时视口就不是
  设计尺寸。实测那台机器（1920×1200、推荐 175%、实际 125%）：窗口按 125% 建成 720×600 物理
  像素（是对的），内容却按约 1.75 倍渲染，视口缩到 411×343，高 195px 的输入框（`position:
  relative` + 不透明底，画在静态兄弟行之上）直接盖住了选项行 —— 用户看到的是"少了一整行"，
  而 DOM 里它一直都在。**判据是量出来的比值，不是猜的**：照片里 36px 的启动按钮是 64 物理
  像素，64/36 ≈ 1.78。
  处置（三层，改动前后都别只做一半）：
  ① 前端启动时把 `devicePixelRatio` 经 `reportViewport` 报给宿主（立刻一拍 + 400ms 一拍，
  覆盖创建初期读数不稳），宿主用 `WindowClientForScale` 按"设计逻辑尺寸 × 该比值"重设客户区
  并在工作区内重新居中，最多校正 2 次（同一个比值只处理一次，防来回摆）；
  ② 按该比值算出的窗口装不进工作区时不校正，由前端兜底：`.section` 的下限 217px
  （标签 16 + gap 6 + 输入框下限 195，三个数都不参与 `--ui-scale`）与
  `.options-row/.actions-row/.status-block` 的 `flex-shrink: 0`，让"放不下"表现成滚动条，
  而不是控件被上一行盖住；
  ③ 跨显示器拖动仍**不**跟随（既有决定不变），此时界面退化为可滚动。
  复现配方（不改一行代码）：`WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--force-device-scale-factor=1.75`
  + dev 构建，视口立刻缩到 412×343，症状与用户截图逐项一致；量产品进程里的真实值用 CDP
  （同一环境变量里再加 `--remote-debugging-port=<port>`，`/json/list` 拿 webSocketDebuggerUrl，
  `Runtime.evaluate` 读 `devicePixelRatio` 与 `documentElement.clientWidth`）。
  `go-webview2` 既不设也不暴露 `RasterizationScale`（连 Controller3 的 vtable 都没有），
  所以"钉死缩放"要动依赖，"量回来再适配"是当前能做且已做的那条路。
  两处实测补充（独立复核留下）：① 内容缩放 1.75 时 CSS 视口实测是 576×481（设计 480），
  而客户区物理像素仍是精确的 1008×840 —— 7 个数据点都符合"CSS = ceil(物理 × float32(1/缩放))"，
  是 1/1.75 的 float32 倒数偏大 1px 所致（机制未逐行验证），多的那 1px 被输入框吸收，无害；
  ② 契约测试必须**连回调体与调用端一起钉**：只钉签名时，把 `reportViewport` 的回调体掏空成
  `return nil`、或删掉页面里的上报调用，整套闸门都全绿而校正彻底失效（独立复核实测）。
  现在的安排是：`cmd/type/contract_test.go` 要求回调体里真的调用 `WindowClientForScale`，
  并要求 `main.ts` 里出现 `startViewportReporting()`；上报本身抽在
  `frontend/src/viewportReport.ts`，节拍由 `frontend/test/viewportReport.test.ts` 在行为上钉住
  （立刻一拍 + 400ms 一拍、报的是 `devicePixelRatio`、绑定不存在时不冒泡，另有一条**不打任何
  注入、直接走生产默认值**的用例：默认参数被换成空函数时，只有它拦得住）。**只断言"某个
  表达式在文件里出现过"是拦不住"调用点被删掉"的**，所以调用端必须钉调用点本身；
  **默认参数也是接线**，钉了内层函数的默认值不等于钉了外层的
- **`ScaledForDPI` 的取整必须四舍五入**（`roundDiv`；2026-10 删掉 `ClientLogicalSize`
  后，生产里只剩"逻辑→物理"一个调用点）：整数除法的截断在
  非整数倍缩放下会稳定少一个物理像素（125% 下 1024 → 1023），而客户区尺寸同时决定
  WebView2 的渲染表面大小，差一像素就要重采样。本机实测 DPI 为 120（非 96 整数倍），
  这类机器正是误差最容易露头的地方，`TestScaledByDPIRoundsToNearest` 钉着
- 前端的 `--ui-scale`（`frontend/src/composables/useUiScale.ts` 注入）只许缩放**数值型
  细节**：内边距、间距、圆角、控件高度、图标边长。`border-width` 与 `font-size` 永不参与
  —— 小数像素边框会渲染成深浅不一的虚边，而字号是布局的输入，跟着缩放会把输入区高度、
  状态栏高度、字数徽标全变成联动量。默认值与 540 宽档恒为 1，保证默认档渲染逐像素不变。
  取值规则抽成纯函数 `uiScaleFor`（`frontend/test/uiScale.test.ts` 钉边界：540→1、
  648→1.2、675→1.25 才够得着上限）；**基准宽度 540 必须等于宿主窗口下限 `MinWindowW`**，
  这条跨端一致性由 `cmd/type/contract_test.go` 的 `TestUiScaleBaseMatchesWindowFloor` 核对
  （两边只改一处时，两边的编译与测试都不会失败，界面却会开始缩放或不再缩放）
- **空闲态不许有进度条：初始 `progress` 必须是 -1**（2026-10 修，症状是"只有第一次启动
  才看得见一条空进度轨道"）：`progress` 的语义是 **0~100 表示进度，-1 表示隐藏**，前端按
  `progress >= 0` 决定显不显示进度条（`StatusBar.vue` 的 `progressActive`）。而
  `TypingStatus.Progress` 是 int，**零值恰好等于一个合法取值 0**：`NewTypingService` 里
  只写 `&TypingStatus{Phase: PhaseIdle}` 就会把 `progress: 0` 发给前端，于是空闲态的界面在
  启动时长出一条高度 10px 的空轨道、把上方 UI 顶上去；跑过一次任务后所有状态路径都写 -1，
  那条轨道又消失 —— 看起来像"第一次启动的幻觉"。**给结构体写字面量时，凡是"有几个合法取值、
  其中一个是零值"的字段都要显式赋值**，`TestServiceInitialState` 与
  `TestInitialStatusIsIdleZeroValue` 用字面量 JSON 钉着 -1。
  配套的 CSS 保险（不是根因，但保留）：`.progress-wrap` 显隐同时改 `height`（0 ↔ 10px，
  这是"上方 UI 被挤上去"的动效来源，用户要的就是它）、`opacity`、`visibility`，并且
  `.progress-track` 空闲时 `background: transparent`（只在 `.active` 下给轨道色）——
  让某样东西看不见时，除了不透明度，最好让它自己也没有可画的东西
- **状态栏高度写死为一行**：`.status-bar` 的 `height: calc(13px * 1.5 + 16px + 2px)` = 37.5px
  （`line-height` 是无单位 1.5、跟着字号解析，写高度时必须跟着写 1.5），文案 `nowrap` +
  省略号截断，避免文案折行顶动输入框。边界：省略号只在文案超出一行时出现，而实测官方尺寸
  （540~648 宽）下最长的那条终态文案占一行、宽 446px、可用 470px，正常路径看不到省略号。
  **判断文案有没有被截断不能只看 `scrollWidth > clientWidth`**：两个值都取整，差 1~2px 时
  会假报"被截"（540 档实测 `scrollWidth 446 / clientWidth 444`，而截图里文案完整无省略号）——
  以截图为准
- **抓真窗口取证的三个坑**（2026-10，用 GDI `PrintWindow` + `PW_RENDERFULLCONTENT` 抓
  `Type.exe` 客户区时踩到）：① 抓之前**必须先确认没有 Type 进程在跑** —— 单实例守卫会让
  新进程静默退出，接着你抓到的是**旧进程的窗口**，于是"改了没生效"的结论完全是假的
  （本仓库踩过一次：据此以为 CSS 没生效，其实是量到了上一个构建的窗口）；
  ② 宿主脚本要先声明 DPI 感知（`SetProcessDpiAwarenessContext(-4)` 或 `SetProcessDPIAware()`），
  否则 `GetClientRect` 返回的是虚拟化后的逻辑坐标，抓出来是"左上角被裁掉一块"的图；
  ③ PowerShell 宿主脚本用**纯 ASCII**（或带 UTF-8 BOM），无 BOM 的中文脚本会被 5.1 按 GBK
  解码吞掉引号，报成"字符串缺少终止符"（与 `build.ps1` 的 BOM 那条同源）
- **DPI 感知不能丢**（v1.5.1 换 webview 绑定时踩过）：进程 DPI 感知由 `tools/mkres`
  生成的 manifest 声明（PerMonitorV2）。旧库是在运行时调 `SetProcessDpiAwarenessContext`，
  纯 Go 绑定没有这层，少了 manifest 就会在缩放非 100% 的显示器上被系统做位图拉伸、
  整窗发虚。声明之后窗口坐标**一律按物理像素解释**，逻辑尺寸必须经 internal/win32 的
  `ScaledForDPI` 换算，否则固定尺寸窗口会比预期小两成（旧库在内部做过同一件事）。
  跨不同缩放显示器拖动时**不重新适配**是刻意决定（用户明确不要多显示器跟随），
  理由与守卫见本章首条，已记在 README 已知限制里
- Win32 怪癖的注释保留在 internal/win32 的实现内（全角标点 WM_CHAR 绕行、GDI 句柄型
  格式跳过、图标句柄所有权约定），它们是本代码库最有价值的文档，重构时勿删
- **发布构建一律带 `-trimpath`**（build.ps1 / ci.yml / release.yml 三处）：少了它，产物里
  嵌着构建机的绝对路径（实测 exe 里能搜到 `D:/Projects/Type/...`），既漏一点环境信息，
  也让"同样源码编出同样文件"做不到。加了之后源码位置以 `internal/typing/typing.go`
  这种相对路径记录，pecheck 与图标/版本/manifest 都不受影响
- 注入失败必须可见：`TextInjector` 五个方法都返回 bool（SendInput 被 UIPI 拦截时
  整体返回 0；WM_CHAR 直投的 SendMessageTimeout 超时/失败返回 0）。不要把返回值
  改成 void，那正是"静默失败被报成输入完成"的来源
- **启动路径有三道闸门**（internal/win32 的 win32_webview2.go 与 cmd/type/main.go 的 createWebView2）：
  ① 创建 WebView2 之前先用依赖自带的 `webviewloader.GetInstalledVersion()` 预检（微软对这类设备的
  官方建议就是"先检测、再引导用户去官网安装"），缺运行时给 `MsgWebView2Missing`；
  ② 创建之后仍要判 `New` 的返回值：预检通过不等于创建成功（运行时可能损坏或被策略拦下）。
  **nil 判空必须在 `defer w.Destroy()` 之前** —— `New` 同步失败时返回的是 nil 接口，
  而 defer 语句求值 receiver 的那一刻就 panic，栈顶落在那条 defer 上而不是后面的 SetTitle，
  看着像是别处的问题；
  ③ 控制器其实是在回调里**异步**创建的，失败时不走 `New` 的返回值：go-webview2 用
  `int64(res) < 0` 判 HRESULT，而错误码是负的 32 位值、零扩展进 uintptr 之后 `int64()`
  反而是正数，这个判断永不成立，它接着对 nil 控制器解引用，panic 从 `NewWithOptions`
  里冒出来（帧都在库内，但整条链在 main 的 goroutine 上）。`createWebView2` 用 recover
  把它折算成 nil，与 ①② 落到同一句 `Type 无法启动` 上。**别把 recover 当多余兜底删掉**：
  GUI 子系统没有控制台，不接住的话症状就是"双击之后窗口一闪就没了"，正是这条路径要消灭的
- **宿主进程完整性级别偏低时 WebView2 拒绝创建控制器**（2026-09 实测，走的就是上面第 ③ 条）：
  把 exe 所在目录打上 Low 完整性标签（`icacls <目录> /setintegritylevel Low`，某些沙箱类
  工具会对工作区这么做），同一份 exe 从该目录启动必崩、拷到别处就正常，实测此时进程的
  完整性级别确为 Low。排查手法：`icacls <目录>` 看有没有
  `Mandatory Label\Low Mandatory Level`；想复现就在别处给一份 exe 加同一个标签。
  本地调试碰到"从仓库目录跑就崩、拷出去就好"，先看这条，别去怀疑代码或运行时版本
- **复现"机器上没有 WebView2"的启动路径**（改动启动代码前后都该跑一遍，真机不用动）：
  把环境变量 `WEBVIEW2_BROWSER_EXECUTABLE_FOLDER` 指向一个不存在的目录再启动。
  期望：弹出 `Type 无法启动` 提示框，进程停在框上，stderr 为空。修这个之前这里是
  `Result: 80070002` 加一句 nil 指针 panic 后退出 —— 而 GUI 子系统没有控制台，
  这两种输出用户一个都看不见，症状就只剩"双击之后什么都没有"
- 剪贴板恢复守卫用 `Clipboard.HoldsText`（按原始字节比对）而不是 `GetText` 的
  字符串比较：CF_UNICODETEXT 内嵌 NUL 时字符串会在 NUL 处截断而误判
- **`HoldsText` 返回 `(holds, known)` 两个值，不许压回一个 bool**（v1.5.7 修）：
  「读不到剪贴板」（打开失败、GlobalLock 失败、GlobalSize 为 0）与「剪贴板已被用户改动」
  是两件处置**相反**的事，前者必须按「没恢复」上报（注入前的内容早已被覆盖，原内容可能
  已经丢了），后者跳过恢复才对（剪贴板归用户所有）。旧签名只有一个 bool，歧义被解到了
  掩盖失败的那一边：用户原内容丢了，收到的却是「输入完成」。回归用例
  `TestUnreadableClipboardIsReported`、`TestServiceClipboardGuard`
- **剪贴板恢复失败必须可见**：`RestoreSnapshotRaw` / `writeClipboardFormats` 都返回
  是否真的写回成功，`restoreClipboardSnapshot` 把它折算成"剪贴板是否仍保有注入前的
  内容"，终态据此在成功路径改用 `输入完成，但剪贴板未恢复，原内容可能已丢失`。
  恢复与读写的重试档位是分开的（4×50ms 对 8×100ms）：失败意味着用户原本复制的东西
  丢了，值得多等几百毫秒；这段时间在任务收尾，用户无感。用户已复制新内容时跳过恢复
  不算失败（剪贴板归用户所有，正是想要的结果），快照没拿到（nil）才算
- **快照不完整时不许碰剪贴板**（v1.5.6）：`Snapshot()` 返回
  `ClipboardSnapshot{Formats, Complete}`，`Complete` 是"这份快照能不能拿去恢复"的
  唯一判据。恢复流程会先 `EmptyClipboard`，所以拿残缺的快照去恢复，等于把没抄到的
  那些格式永久销毁，而用户看到的会是"输入完成"。读某个格式失败、一个格式都没读到、
  或剪贴板打开失败，都算不完整（后两种无从区分，对调用方也没区别）。
  这种情况下剪贴板这条路整个不作数，退回逐字符输入：慢一点，但用户剪贴板里的东西
  一个字都不会动。**别把这条改回"照抄多少算多少，完事报个'可能丢了'"** ——
  那是丢完了才告诉人家
- **剪贴板路径失败之后不再退到逐字符**：能走到失败那一步说明剪贴板已经被写过
  或粘贴已经发出，再打一遍就是注入两遍，也会用 `SendRune` 的失败原因把
  `粘贴未生效：...` 那类具体原因盖成一句泛泛的 `输入中断`（v1.5.6 踩过：
  终态文案被覆盖，两条既有用例当场变红）。入口那次漂移守卫失败（一个字都没
  送出去、剪贴板也没碰）由调用方按整条路径处理，所以 `typeTextViaClipboard`
  不必再回报"碰没碰过" —— 曾经加过一个这样的返回值，加上才发现它两个取值走法
  完全相同，属于「返回多个同义值等于没返回」，已删
- **快照只跳过"不是普通内存块"的格式**（`skippableFormat`）：句柄型（CF_BITMAP /
  CF_PALETTE / CF_ENHMETAFILE）、块内含句柄的 CF_METAFILEPICT、所有者绘制与
  私有显示格式、GDI 对象格式族（0x0300-0x03FF）。判据是"照抄下来恢复时会不会
  写回失效句柄或垃圾字节"，而不是"这个格式少见"：后者会把 HTML Format、RTF、
  PNG 这些注册格式一起丢掉；CF_PRIVATEFIRST..LAST 刻意不跳过，它们是内存块。
  新增格式时按判据决定，别退化成"全都照抄"（旧行为）或"只抄白名单"
- **单实例守卫认两个错误码**（v1.5.6）：`CreateMutexW` 对"名字已被占用"有两种回报 ——
  有权打开时报 `ERROR_ALREADY_EXISTS` 并返回句柄，无权打开时报 `ERROR_ACCESS_DENIED`
  并返回 NULL。后者与"根本没建成"共用返回值 0 却语义相反，所以判定抽在
  `mutexAlreadyHeld` 里、且有单测钉着（`TestMutexAlreadyHeld`），
  `TestClaimInstanceMutex` 只能用同名第二次调用来验证前一支。
  漏掉 `ERROR_ACCESS_DENIED` 的实际后果：README 建议需要向提权窗口注入的用户以管理员
  身份运行，于是"先管理员开的实例、后普通权限的实例"会同时跑起来，而守卫存在的理由
  正是这两个实例会互相抢剪贴板与键盘焦点
- **Win32 负常量以 `^uintptr(n)` 表达**（`^` 是按位取反不是取负：`^uintptr(13)` = -14，
  `^uintptr(33)` = -34），看着像笔误但不是，改成十进制负数字面量既编译不过也没必要。
  这地方极易被误读误改，故 `internal/win32/win32_test.go` 里 `TestApplyWindowIconClassIndex`
  会真建窗口跑一遍 `applyWindowIcon`、再按文档偏移把类图标读回来核对；新增同类 Win32 常量时
  照此补一条实测用例，别只写"常量等于某值"式的自我复读
- 产品仅支持 `windows && (amd64 || arm64)`（约束落在 cmd/type 与 internal/win32 上）；
  386 编译被刻意禁止（INPUT 结构体手工填充仅匹配 64 位 ABI）。业务层 internal/typing
  刻意不带约束，任何系统上都能编译（`go vet` / `go test -c` 都过）；但在非 Windows 上
  `go test ./...` 会**明确失败**而不是静默通过：`internal/win32` 与 `cmd/type` 因构建约束
  被整个排除，`internal/typing` 则被编译成宿主平台的目标文件再拿去执行，报
  `fork/exec ... is not a valid Win32 application` 并 exit 1（2026-09 实测，此前这里
  写的是"静默跳过"，与事实不符）。所以产品侧的本地验证与 CI 都跑在 Windows 上
- **界面以 `about:blank` 加载，没有可用的源**（2026-09 实测）：exe 走 `SetHtml`，而
  go-webview2 的 `SetHtml` 就是 `NavigateToString`（不是 data: URL），文档的 href 是
  `about:blank`、`origin` 为 `null`，`localStorage` 与 `sessionStorage` 一律抛
  `SecurityError`。所以主题的手动选择不跨启动保留（已写进 README 已知限制），界面里
  任何"记住用户偏好"的打算都得走宿主侧存储（加 Bind），别直接调 localStorage 再靠
  try/catch 兜着。探针写法：临时程序 `w.SetHtml(...)` + `w.Bind("report", ...)` 把结果
  打到 stdout，`go build` 成控制台程序（不带 `-H windowsgui`）后到**仓库目录外**运行
  （工作区带 Low 完整性标签时 WebView2 起不来，见上一条）
- **Node 主版本只写在 `.node-version` 一处**（现为 24）：`.github/workflows/verify.yml`
  用 `node-version-file` 读它，开发者的 nvm/fnm 也读同一个文件。它决定 vite/rollup 的
  产出字节，换版本后 `internal/web/dist/index.html` 与入库版本对不上，漂移检查会报不一致
- 提交信息：中文一行式主题 + 正文说明要点
