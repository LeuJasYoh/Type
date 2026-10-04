# 架构、布局与单一来源

本文件从 `AGENTS.md` 拆出（2026-10），内容逐行搬运、标题未改。
分层与目录布局、"单一来源"落在哪一处、工具链分界都在这。

## 布局与单一来源

- 分三层三包，依赖方向单向：平台层 → 业务层，装配层 → 两者
  - `internal/typing/`：业务层，TypingService 输入状态机 + 平台能力接口（消费方定义）
    + 时序常量区；刻意不带构建约束，与平台无关，任何系统上都能 `go test ./internal/typing`。
    **一次任务占住槽之后怎么走完，在 `taskrun.go`**（五个阶段：收尾、开工前置、倒计时与
    锁定、分流注入、终态；2026-10 从 256 行/9 参的 `runTypingTask` 拆出来，判定顺序与
    守卫位置一字未改，见 invariants.md 的「任务槽与并发启动」）
  - `internal/win32/`：平台层，三个接口的 Win32 实现。win32.go 是 DLL 入口清单页；
    win32_keyboard/clipboard/window/instance/msgbox/webview2 各管一摊；实现与业务层接口的
    绑定在 win32.go 末尾以 `var _ typing.X = ...` 编译期核对，签名漂移直接构建失败。
    **业务层不得反向依赖本包**（当前无此依赖，编译器会拒绝任何反向引用）
  - `cmd/type/`：装配层，main.go 开窗与 Bind 绑定（平台能力在此注入）。`devserver_prod.go` /
    `devserver_dev.go` 由 `dev` 构建标签二选一，两个文件各持有同一组开发开关：dev server 地址
    （devServerURL）与 WebView2 调试模式（devMode，决定 DevTools 与右键菜单），正式构建里两者都关；
    启动预检与提示框在 internal/win32 的 win32_webview2.go 与 win32_msgbox.go，两者都不经过
    WebView2（界面起不来时，系统自己的对话框是唯一还能用的通道）
- `internal/web/`：go:embed 前端产物包（dist/index.html 入库，免 Node 亦可 go build/test）
- `frontend/`：自包含 Vite 项目（package.json / node_modules 都在这里，不在仓库根）。
  `test/` 是前端单测：用 Node 自带的测试跑器（`npm test`），零新依赖，不引测试框架；
  `ts-resolve.mjs` 是测试期的解析钩子（给省略扩展名的相对导入补 `.ts`，
  因为 src 的导入是按打包器写的）。tsconfig 只含 `src/**`，测试不进类型检查与打包
- `assets/`：图标源图与产物（screenshot-light.png / screenshot-dark.png 为 README
  配图，README 用 `<picture>` + `prefers-color-scheme` 引用，GitHub 会自行按访问者主题切换）
- **README 配图的复现方式**（1152×960，即逻辑 576×480 / 2 倍像素密度，与尺寸下限档一致）：
  ① 临时页 = `internal/web/dist/index.html` 开头插一段打桩脚本（定义 `startTyping` /
  `getTypingStatus` 等四个绑定，并按运行态摆出目标窗口条与进度条），再补一段停用
  transition/animation 的样式（否则进度条的入场动画在无头下停在 0 高度）；用本地 HTTP 服务
  提供，`file://` 下 localStorage 不可用，主题没法显式指定；
  ② 用系统已装的 Edge 无头截图：`msedge --headless=new --force-device-scale-factor=2
  --window-size=576,480 --virtual-time-budget=6000 --user-data-dir=<临时目录>
  --screenshot=<out.png> "<URL>?theme=<light|dark>"`，不弹窗口、不碰用户桌面。
  **两张图必须同一次生成**（同一份产物、同一档尺寸），否则浅色与深色会对不上。
  改窗口尺寸下限（`MinWindow*`）时记得一起重截，图里的界面尺寸就是那一档。
  两个已验证死路，别再走：内置浏览器截图通道在非 1:1 像素密度下会把画面平铺成多份；
  截真实窗口（改窗口尺寸/截屏）会干扰用户桌面。`<picture>` 是 GitHub 明确支持的特性
  （渲染时会被包一层自家的 `themed-picture`），配图用 `<p align="center">` 居中。
  **`<img>` 不要写死 `width`**：留空时按 GitHub 的 `max-width:100%` 铺满正文列
  （与旧 markdown 配图观感一致），写死会明显变小
- **界面几何测量：`frontend/tools/layout-probe.mjs`**（改版式、定尺寸、怀疑"文案会不会
  被裁"时先用它，别靠读 CSS 估）：

  ```
  node frontend/tools/layout-probe.mjs --sizes 576x480,610x509,648x540 --shot
  ```

  它把 `internal/web/dist/index.html`（构建产物，**先跑 `npm run build` 让它是最新的**）
  复制到临时目录，注入打桩的五个绑定与收集脚本，用无头 Edge 逐档量真实几何：`scrollHeight`
  vs `clientHeight`（裁不裁）、`scrollWidth` vs `clientWidth`（横向溢出）、`.input-wrap`
  与 `.status-bar` 实测高、状态块是否贴底、文案折几行。`--scenario full` 加目标条与进度条
  （元素最全的一档），`--shot` 另存截图，`--out <临时目录>` 换工作目录。
  三个踩过的坑：① 起 Edge 必须用异步 `spawn`，`spawnSync` 会锁住同进程里的 HTTP 服务、
  Edge 永远拿不到页面；② `--dump-dom` 下 `--window-size` 是**含窗口框**的外框（本机
  大 26×93）且系统有最小窗口宽度（视口到不了 496 以下），`--screenshot` 下则**等于视口** ——
  截图直接用目标尺寸，不要用校准后的外框尺寸；③ 每档必须用独立的 `--user-data-dir`，
  撞车时 Edge 静默退出码 21。收集脚本自身的隐藏元素要用 `position:fixed`，`left:-9999px`
  那不换行的元素会把 `documentElement.scrollWidth` 顶大，报出页面并不存在的横向溢出
- **文案宽度只信实测**：最长的那条终态文案 `未切换到目标窗口：…`（37 字）在 540 宽下实测
  `textW = 446px`、状态栏可用 470px，**折 1 行**；两行要到 ≤496 宽才出现。2026-10 之前
  文档里的"折 2 行/3 行"是按"汉字 26px 宽"估出来的（错了一倍：13px 字号下 CJK 回退字体
  一个汉字就是 13px），据此定出的"450 高会被裁"结论不成立 —— 拿不准就用上面那个探针量，
  别用字符数乘宽度的估算
- `release-notes/`：各版本的发布说明（`v<版本>.md`），发布时原样作为 Release 正文
- `testdata/`：手工测试页（paste-guard.html / completion-guard.html），无任何自动引用；用法写在文件头注释里
- `tools/`：构建与验证期工具，都不进产品链路
  - `mkres/`：生成图标 + 版本信息 + DPI 感知 manifest → `version_<arch>.syso`（构建期生成，不入库）
  - `pecheck/`：读回校验构建产物，核对 PE 架构 + 图标/版本/manifest 是否真的链进 exe（发版与 CI 共用）
    不数图标帧数，那是 `assets/icon.ico` 的属性，重新生成图标就会变
  - `wmcharprobe/`：注入通道探针，WM_CHAR 文本直投 vs SendInput 按键的 A/B 验证，
    direct 模式逐字镜像产品的文本直投算法；取焦点窗口的规则与产品同步（拿不到焦点子窗口就放弃 WM_CHAR 投递，不拿顶层窗口顶替，否则测出来的结论不属于产品）；读 completion-guard.html 的 title 遥测（c/k/n/p/a/L/h）作判据；
    用法见文件头注释
  - `equivcheck/`：重构等价性验证（逐函数比对函数体；键含 receiver（`类型.方法`），
    不同结构体的同名方法不再互相覆盖）。**只比函数体**：签名、参数顺序、包级常量、
    结构体字段与 tag 都在视野之外，别拿它的输出当"零行为变化"的唯一证据。
    旧侧只读 `--old-file` 指定的**一个**文件（默认 `cmd/type/main.go`），重构跨了多个
    文件时要逐个跑；默认值必须跟当前布局一致，写错时它会给出提示而不是一句 git 报错
  - `less-ai-tone/`：对外文字的去 AI 味规则与检测脚本（写、改散文前读它，不进产品与 CI）
  - `gen-icon/`：图标资产管线（**自包含的 uv 项目**：`gen_icon.py` + pyproject.toml /
    uv.lock / .python-version，`.venv` 就地生成；命令 `uv run --directory tools/gen-icon gen_icon.py`）。
    脚本靠**向上找 `go.mod`** 定位仓库根，别改回按层数数 `parents[N]`：它搬过一次家
    （scripts/ → tools/gen-icon/），按层数会在搬家时指向 `tools/assets` —— 而源图在仓库根
- `.github/workflows/`：
  - `verify.yml`：**检查项的唯一处**，`on: workflow_call`，被 ci.yml 与 release.yml 共用。
    内容：gofmt / vet / `-race` 测试 / 漏洞扫描 / build / 前端单测 / 前端产物漂移检查 / 版本同步检查 /
    `build.ps1` 的 BOM 检查。
    漏洞扫描用 `govulncheck` 的退出码当判据：可达的漏洞返回非零（构建失败），只在依赖里
    存在而调不到的返回零（不算失败，否则一个用不上的 CVE 就能卡住发版）；分析器版本钉住，
    免得 releases 上冒出问题版本时构建莫名其妙地红
  - `ci.yml`：两个作业。`verify`（调用上面的 verify.yml）与 `build`（amd64/arm64 矩阵：
    mkres 生成资源 → 发布参数构建 → `tools/pecheck` 读回校验；arm64 另做 `go vet` 与
    `go test -c` 测试编译（编 internal 两个包与 cmd/type）。那一步必须**逐个包编译并核对
    产物存在且非空**：只判退出码会假绿 —— 包里没有测试文件时 `go test -c` 既不报错也不
    产出任何文件（2026-10 补，同一个坑踩过第二次）。
    拆开是刻意的：前端构建不必按架构重复，而"资源有没有链进产物"只有真构建一次才回答得了
  - `release.yml`：发版（见 build-and-release.md）。它的 `release` 作业 `needs: verify`，
    所以发布包不可能出自检查不过的提交
- **版本号单一来源**：`cmd/type/main.go` 的 `version` 变量；build.ps1 自动同步到
  package.json 与 package-lock.json 的**两处根版本**（顶层与 `packages[""]`；依赖自己的
  version 不碰），并在构建期把它传给 `tools/mkres` 生成资源，改版本只改 main.go，
  然后跑 build.ps1。lock 的根版本曾长期没人管（停在 1.5.8 而 package.json 已是 1.6.1），
  现在 verify.yml 的版本同步检查会连它一起断言。**那条断言刻意用正则而不是
  `ConvertFrom-Json`**：lock 里有一个**空字符串键**（npm 的根包条目 `packages[""]`），
  PS 5.1 的 `ConvertFrom-Json` 遇到空键直接报 `argument "name" is not valid`
  （最小复现 `'{"":"x"}' | ConvertFrom-Json`；同一时刻解析 package.json 正常）。
  与文件行数无关，换个 PowerShell 版本或 npm 布局就会冒出与版本号无关的失败。
  版本可带预发布后缀（如 `1.5.0-rc.1`）：
  资源里的数字字段取后缀前的数字部分（只允许数字），ProductVersion / package.json 用
  完整串（semver 兼容）；ci.yml / release.yml 的版本检查用同款正则校验完整串。
  **提取版本号的正则必须锚定 `(?m)^\s*var\s+version\s*=`、带 `-CaseSensitive`，并断言
  匹配数恰好为 1**（build.ps1 / verify.yml / ci.yml / release.yml 四处同款，改一处要
  一起改）：`version` 是常见词，无锚点的子串匹配会命中注释里的 `// version = "1.5.8"`
  或将来某个 `minXxxVersion = "…"`，而下游（标签闸门、资源、包名）全用这个提取值 ——
  取错值会一路错到底，且没有任何环节比对"提取值 == 程序真正用的 version"（v1.5.7 修）
- **"必须多处同款"的东西有本地闸门**（2026-10 补）：`cmd/type/build_contract_test.go` 在
  `go test` 里钉着三件事 —— 四处版本号正则逐字相同且都带 `-CaseSensitive` 与"恰好一处"
  断言、三处发布构建命令行都含 `-trimpath` 与 `-ldflags="-H windowsgui -s -w"` 且指向
  `./cmd/type`、`build.ps1` 首三字节仍是 BOM。断言只看**非注释行**：否则把真命令里的
  `-trimpath` 删掉、旁边补一条写着同款字面量的注释就能骗过它（独立验证的变异实测过）。
  它是逐字比较，不吃"行为等价的改写"（拆行、反引号续行、`1 -ne $m.Count`）——四处一起
  改写时要顺手改掉测试里的常量
- **build.ps1 必须保留 UTF-8 BOM**（文件首三字节 EF BB BF）：Windows PowerShell 5.1
  对无 BOM 的 .ps1 按系统 ANSI（中文系统为 GBK）解码，中文注释的尾字节会吞掉换行，
  把下一行代码并进注释成为死代码，ProductVersion 同步曾因此静默失效。改脚本后若
  BOM 丢失（部分编辑器会吞），构建产物版本属性会先出症状。**verify.yml 的「build.ps1 BOM 检查」量首三字节**（2026-10 补：这条检查加上来之前，改写工具刚吞过一次）
  **别以为"部分编辑器"离你很远**：2026-10 又一次复现——用不带 BOM 写回的方式改了一行脚本，
  本机 PowerShell 5.1 立刻报"字符串缺少终止符"，靠 `cmd/type/build_contract_test.go` 的
  `TestBuildScriptKeepsUTF8BOM` 当场拦下；改完 `.ps1` 请先跑一次 `go test ./cmd/type/`
- **build.ps1 自己钉住构建环境**（2026-10）：`GOARCH=amd64`、`GOFLAGS` 清空，跑完把两者还原给调用者。
  调用者会话里遗留的 GOARCH（「常用命令」的 ARM64 配方就在同一个 shell 里导出）会让
  `go run ./tools/mkres` 先交叉编译再执行，报 `This version of %1 is not compatible with the
  version of Windows you're running`，理由看着像资源生成坏了；`GOFLAGS=-tags=dev` 会静默产出
  带 DevTools 与右键菜单的开发版 exe，而 pecheck 只看资源、查不出来
- **入库的前端产物必须与源码同步**：改了 `frontend/src` 就要跑 build.ps1 重新生成
  `internal/web/dist/index.html` 并一起提交；忘了的话 CI 的漂移检查会失败
  （go:embed 是静默的，本地不会有任何报错）

## 工具链分界（勿混用）

| 用途 | 工具 |
|---|---|
| 业务代码 / Go 测试 / 入库工具脚本 | Go（equivcheck 即 Go 写的） |
| 前端 | TypeScript + Vite；单测用 Node 自带的测试跑器（零新依赖） |
| 构建编排 / 发布打包 | PowerShell（build.ps1、release.yml 的步骤） |
| Windows 资源生成（图标/版本信息） | Go（tools/mkres，winres 库），取代 windres + .rc |
| 构建产物校验（PE 架构 / 资源读回） | Go（tools/pecheck，winres 库，仅构建期使用） |
| 图标/图像资产管线 | Python，仅经 uv（自包含项目在 `tools/gen-icon/`，pyproject 锁定 pillow==12.3.0） |

构建链**不含 C 编译器**：webview 绑定是纯 Go 的 go-webview2，资源由 tools/mkres 生成，
`go build` / `go test` 都不再需要 cgo，新增依赖时别把 cgo 带回来（那会重新要求
MinGW 的 gcc/g++，把"只需 Go + Node 即可构建"这个前提打破）。

唯一的例外是 `go test -race`：Windows 上的竞态检测器要 cgo，所以它需要 gcc
（本机有 MinGW 时可直接跑，CI 的 runner 自带）。这只是**测试期**的依赖，
产品构建与发布仍然不需要 C 编译器；`CGO_ENABLED=0` 下 `go build` / `go test`
照常通过，只有 `-race` 会被拒绝。

## GitHub 的语言构成只统计产品代码

仓库页那条语言构成（Language bar）**只代表产品代码**：测试与构建/工具链一律不计入。
判据是"这个文件是不是产品的一部分"，不是"它重不重要" —— 测试与工具链都不进发布产物，
所以都不算；产品侧的 `cmd/type/**`、`internal/**`、`frontend/src/**` 照常统计。

规则本身写在 `.gitattributes` 里，用的是 Linguist 的官方开关 `-linguist-detectable`：
它只影响统计与语言条，**不影响语法高亮，也不进任何构建产物**（文件照旧被构建读取）。
覆盖范围：`*_test.go`、`frontend/test/**`、`frontend/tools/**`、`scripts/**`、`tools/**`、
`*.config.ts`（构建配置是构建的输入，不是产品代码；首个漏网的是 `frontend/vite.config.ts`）。

为什么要有这条（2026-10）：`scripts/build.ps1` 的一次改动给它加了约 35 行注释，文件从
5.6KB 涨到 8.5KB，于是在语言条上把只有 7.5KB 的 Vue 挤进了 Other —— 一个构建脚本排在
界面代码前面，读起来完全不像这个项目。**中文注释在 UTF-8 里一个字 3 字节**，注释写得
越清楚字节数涨得越快，所以"按字节比大小"的条尤其不适合表达"这个项目是什么写的"。

按这条规则重整之后的构成：

| 语言 | 字节 | 占比 |
|---|---|---|
| Go | 105,868 | 68.7% |
| CSS | 20,002 | 13.0% |
| TypeScript | 19,364 | 12.6% |
| Vue | 7,530 | 4.9% |
| HTML | 1,305 | 0.9% |

合计约 150 KB（Go 侧不含 126 KB 测试、前端侧不含 23 KB 测试与 24 KB 工具）。
JavaScript、Python、PowerShell 三者**全部**来自测试与工具链（版式探针、测试解析钩子、
资产管线、资源生成与校验工具、构建脚本），因此不在条里。

新增顶层测试或工具目录时，记得让 `.gitattributes` 的规则覆盖到；否则它会带着自己的
字节数重新挤进那条构成里。
