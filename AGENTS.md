# AGENTS.md：面向 AI 代理的工程约定

人类读者请看 [README.md](README.md)；本文件写给在此仓库工作的 AI 编码代理，
收录不成文的工程约定与踩过的坑，避免每次会话重新考古。

**先记住四条铁律**（其余可以边做边查）：

1. **冻结文案一个字都不能改**（它们是发布语言，见「行为契约」）；可以新增，但新增必须登记进清单。
2. **改了 `frontend/src` 就必须跑 `scripts/build.ps1` 重新生成并提交 `internal/web/dist/index.html`**（go:embed 是静默的，本地不报错，CI 的漂移检查会失败）。
3. **版本号只改 `cmd/type/main.go` 一处**，其余由 build.ps1 与构建期资源生成自动同步。
4. **发版要连发布说明一起提交**（`release-notes/v<版本>.md`），打上 `v<版本>` 标签即由 CI 完成构建、校验与发布。

## 常用命令

```powershell
# 一键构建 (版本同步 → 前端 → 资源生成 → go build, 产物 Type.exe 在仓库根;
# 末尾读回 exe 核对版本, 再过一遍 tools/pecheck: 资源没链进去就构建失败)
powershell -ExecutionPolicy Bypass -File ./scripts/build.ps1

# ARM64 发行版 (纯 Go 交叉编译, 不需要额外工具链; 资源由 mkres 按架构生成)
$env:GOARCH = "arm64"; go build -trimpath -ldflags="-H windowsgui -s -w" -o Type-arm64.exe ./cmd/type

# Go 验证三件套 (任何 Go 改动后, 与 CI 同款)
gofmt -l ./cmd ./internal ./tools   # 应输出为空
go vet ./...
go test -count=1 -race ./...        # Windows 上竞态检测需要 cgo, 要 MinGW 的 gcc
#   internal/win32 会操作真实剪贴板: 本机若有第三方剪贴板程序(桥接/同步类)占着,
#   那两条真剪贴板用例会 SKIP 并写明读到什么; **CI 里同样情形一律红**(不许跳过掩盖
#   写读链路的回归)

# 发布构建的读回校验: 图标/版本/DPI manifest 是否真的链进了 exe。
# 资源缺失或架构不匹配时 go build 不报错, 只有读回才看得见 (CI 同款)
go run ./tools/pecheck -exe Type.exe -version <main.go 中的版本> -arch amd64

# 前端开发 (HMR): 必须带 dev 构建标签, 否则 exe 不含 dev server 代码路径。
# dev 构建同时打开 WebView2 的 DevTools 与右键菜单, 正式构建两者皆关
cd frontend; npm run dev            # 终端 1
go build -tags dev -o Type-dev.exe ./cmd/type   # 终端 2
.\Type-dev.exe                      # (-dev 参数或 TYPE_DEV_URL 指定端口)

# 前端单测 (轮询状态机 / 内容缩放上报 / 状态栏取值 / 观感缩放; 零新依赖:
# Node 自带测试跑器直接跑 TS, 打桩 window/document 与定时器, 不依赖 WebView2 与真实时间)
cd frontend; npm test

# 图标资产再生成 (uv, 字节级可复现, 仅在更换 assets/icon.jpg 时需要。
# tools/gen-icon 是自包含的 uv 项目: pyproject/uv.lock/.python-version 与 .venv 都在那里,
# 所以在仓库根直接跑 `uv run tools/gen-icon/gen_icon.py` 找不到本项目, uv 会退回系统
# Python 报 ModuleNotFoundError: PIL —— 那条报错看着像没装 Pillow, 不是路径错了)
uv run --directory tools/gen-icon gen_icon.py

# 重构等价性验证 (逐函数比对函数体, 结构调整后证明零行为变化;
# 旧侧只读 --old-file 指定的那一个文件, 默认 cmd/type/main.go, 跨多文件要逐个跑)
go run ./tools/equivcheck <旧rev> <新rev> [--renamed] [--old-file <路径>]
```

## 发布流程（v1.5.3 起由 CI 完成）

**一次发版 = 改版本号 + 写发布说明 + 提交推送 + 打标签**，剩下的 CI 全包（约 2 分钟）。

`.github/workflows/release.yml` 两种触发方式，任选：

- **打标签（标准做法）**：`git tag v1.5.4 && git push origin v1.5.4`
- **网页按钮（网络不便时）**：Actions → Release → Run workflow，填版本号；标签由工作流在当前提交上创建

发布前有三道闸门，任何一道不过都不产出（宁可没发出去，也不发名不副实的包）：

1. **检查全绿**：`verify.yml` 先跑完（gofmt / vet / `-race` 测试 / 漏洞扫描 / 前端产物漂移 / 版本同步）。
   它与 CI 是同一个工作流，**检查项只有那一处定义**；发版这条路上的检查不能比平时松，
   因为"改了 `frontend/src` 忘了重新生成产物"的提交如果放过去，Release 里的 exe 会嵌着
   与源码不符的旧界面，而且没有任何环节会失败（这件事曾经真的会漏，v1.5.6 补的）；
2. **版本一致**：标签 == `cmd/type/main.go` 的 `version`，且 `release-notes/v<版本>.md` 存在、
   里面确实写了本版包名、`package.json` 的 `version` 字段已同步（后两条专治"复制上一版说明
   忘了改版本号"）；
3. **读回校验**：`tools/pecheck` 逐项确认架构、版本号、图标、DPI manifest 真的链进了 exe。

发布说明与代码放在同一次提交里（`release-notes/v<版本>.md`），原样作为 Release 正文：
表格、配图、链接、任意长度都能用，与手工发布观感一致。**内容是人工写的，工作流只负责把它送进 Release**。
写之前先看上一版的文件照抄结构（下载表格 + 本次变化），包名必须与本版一致。

发布物：两个压缩包 `Type-<版本>-windows-<架构>.zip`（amd64 / arm64），
**包内只有一个 `Type.exe`**（与既有 Release 一致：解压后双击即可，不加目录层级）。

新打包进 exe 的第三方组件（**包括前端依赖**）要登记进 `THIRD_PARTY_NOTICES.md`：MIT/BSD
这类宽松许可要求随二进制再分发时带上声明（Vue 就是这么补上的；打包工具默认会删掉 npm 包
自带的许可横幅，所以声明文件是唯一的载体）。`release.yml` 里手动触发填的版本号一律走
`env:` 传值，别把 `${{ inputs.* }}` 直接插值进 `run:` —— 那是文本替换，等于让人在构建机上
执行任意脚本。

版本号规则：**由人决定这次涨多少**，新功能进次版本号（1.5→1.6），修缺陷进补丁号（1.5.3→1.5.4），
可带预发布后缀（如 `1.6.0-rc.1`）。写入位置只有 `cmd/type/main.go` 一处。

判断口径（2026-10 补，此前每次发版都要重新讨论一遍）：**用户能自己看出来"多了一样东西"就进
次版本号** —— 新增开关、新增注入通道、窗口/布局这类看得见的行为变化都算；内部重构、文案
微调、CI 与工具链改动、纯缺陷修复一律进补丁号。1.5.3 曾把"焦点漂移防护"（用户可见的新能力）
放进补丁号，那是偏差不是先例，别再照它办。

**文档必须先于发版落地**（用户明确要求）：README / AGENTS / 发布说明要在**打标签之前**已经
提交到远端，否则 Release 页面与仓库文档对不上，而 Release 正文是用户唯一会读的那份说明。
顺序固定为：改代码 → 同步文档与发布说明 → 构建产物 → 提交推送 → 打标签。

## 布局与单一来源

- 分三层三包，依赖方向单向：平台层 → 业务层，装配层 → 两者
  - `internal/typing/`：业务层，TypingService 输入状态机 + 平台能力接口（消费方定义）
    + 时序常量区；刻意不带构建约束，与平台无关，任何系统上都能 `go test ./internal/typing`。
    **一次任务占住槽之后怎么走完，在 `taskrun.go`**（五个阶段：收尾、开工前置、倒计时与
    锁定、分流注入、终态；2026-10 从 256 行/9 参的 `runTypingTask` 拆出来，判定顺序与
    守卫位置一字未改，见下「任务槽与并发启动」）
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
  - `release.yml`：发版（见「发布流程」）。它的 `release` 作业 `needs: verify`，
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

## 行为契约（冻结，改动需双端同步）

**这一节的内容有自动守护**，改动了会有测试变红，别只改一边：

- `internal/typing/contract_test.go`：冻结文案逐字表 + `TypingStatus` 的 JSON 键集合
  与六个 phase 取值 + 初始状态的整体 JSON；
- `cmd/type/contract_test.go`：六个 Bind 函数名、`startTyping` 的四个参数与类型、
  `reportViewport` 的一个参数与类型、`Status`/`Cancel` 的签名，并读 `frontend/src/ipc.ts`
  核对前端那份镜像。

文案断言刻意写成**字面量**而不是引用常量（看着像自我复读，但那正是它的用途：
别处断言用的是同名常量，改了常量测试照样绿）。新增文案请一并登记进下面两张清单与表。

- webview Bind 函数名：`startTyping` / `cancelTyping` / `toggleTopmost` / `getTopmost` /
  `getTypingStatus` / `reportViewport`
  （`startTyping` 参数：text, delay, forceSendInput, textDirect；`reportViewport` 参数：dpr，
  前端启动时报上来的真实内容缩放，宿主据此让窗口适配内容，见「其它不变量」的内容缩放一条）
- `TypingStatus` JSON 字段与 phase 枚举值（`frontend/src/types.ts` 是其镜像）
- 状态机用户可见文案逐字符保持，它们是发布语言的一部分
- 已冻结文案清单：`剩余 N 秒 — 请聚焦目标窗口...` / `检测到中文，正在操作剪贴板...` /
  `剪贴板操作失败` / `正在逐字符输入 N / M ...` / `输入完成` / `输入失败` /
  `已取消` / `启动失败：上一任务未能及时退出` / `已有输入任务在运行中，请先取消或等待完成`
- 允许**新增**文案（如注入被拒时的 `输入中断：目标窗口拒绝了模拟按键，可能其权限高于 Type`、
  `粘贴未生效：...`、`无内容可输入`、焦点未切换时的 `未切换到目标窗口：...`），
  但不得改写上面已冻结的那些。v1.5.3 的漂移中止三条同属新增，各代表一种不同的
  落点事实，别合并：`输入中断：目标窗口已切换，未输入任何内容`（一个字都没送出去）、
  `输入中断：目标窗口已切换，已输入 N 字`（逐字符路径，N 为实际注入数）、
  `输入中断：目标窗口已切换，粘贴结果无法确认`（粘贴已发出后才发现切换）；
  `输入完成，但剪贴板未恢复，原内容可能已丢失`同理：注入已送达而剪贴板没换回来，
  是两个都成立的事实，既不能退化成 `输入完成`，也不能退化成 `输入失败`
- **无内容可输入的文本在后端就被拒**（v1.5.7）：`Start` 对「剔除 `\r` 后为空」的文本返回
  `无内容可输入`（复用上面那条既有文案，不新增），不写倒计时、不占任务槽；前端 `start()`
  里也有一道同样的校验，文案是前端自己的 `请输入要模拟键入的文本`（只在真正空串时出现，
  一并登记在此）。**判据必须在后端**：界面能改、能被绕过，而「批准一个什么都不做的任务」
  会让用户白等一个倒计时再看到报错。只含空格/换行/制表符的文本是**合法**输入内容，
  别再拿 `trim()` 当成空文本拦掉（前端 ⑧⑨ 两条用例钉着）
- 启动提示框的标题与两条正文同属发布语言，逐字固定：`Type 无法启动` /
  `缺少 Microsoft Edge WebView2 运行时，Type 的界面需要它才能显示。…`（运行时缺失）/
  `WebView2 初始化失败，Type 的界面无法创建。…`（运行时查得到但起不来）。
  读者是从没听说过"运行时"的人，正文只承载三件事：缺什么、点哪里、然后做什么，
  别往里加技术细节；按钮文案跟随系统语言（中文系统显示"是/否"）
- **失败终态必须携带具体原因**，`输入失败` 只在无处可归时兜底：剪贴板路径的原因由
  `typeTextViaClipboard` 返回（`剪贴板操作失败` = 剪贴板本身不可用，`粘贴未生效：...`
  = 剪贴板已写入但目标窗口拒收 Ctrl+V），逐字符路径用 `输入中断：...`。v1.5.1 修过一次
  "专用文案不可达"：该函数曾返回两个恒等的布尔值，调用侧区分"粘贴被拒"的分支永远进不去，
  用户只看得到通用文案。**返回多个同义值等于没返回**，写返回值时先确认各取值真能区分
- 界面 pill 文案：按钮标签 `绕过粘贴检测`、`文本直投`（v1.5.0）冻结，是发布语言；
  悬停提示（title）为可调文案不入冻结清单，但保持一行短句风格（与按钮同量级），
  详细说明写 README；v1.5.0 发布后曾因提示过长精简约一半

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
  中止文案各不相同，见「行为契约」；剪贴板快照恢复流程在中止路径上照旧执行
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

## 提交前：检查文档同步（每次提交必做）

代码改了文档没跟上，是本仓库反复出现过的问题（CI 描述、新增文案、行为语义都栽过）。
每次提交前对照下表过一遍 README.md（面向用户）与本文件（面向代理），
需要更新的与代码放进同一次提交，不要"下次再补"：

| 变更类型 | 同步位置 |
|---|---|
| 用户可见行为 / 功能 / 限制 | README.md：功能、使用步骤、已知限制 |
| 新增或修改用户可见文案 | 本文件「行为契约」的文案清单 |
| CI 步骤 / 检查项 / 发布方式 | README.md 技术栈与项目结构的 CI 行 + 本文件「布局与单一来源」「发布流程」 |
| 命令 / 构建流程 / 目录结构 | 本文件「常用命令」「布局与单一来源」+ README「自行编译」 |
| 非显而易见的新约定 / 踩坑结论 | 本文件对应章节（如「焦点锁定与漂移防护」） |
| 版本号 | 只改 cmd/type/main.go；package.json 交给 build.ps1，资源版本由 tools/mkres 构建期生成 |
| 发版 | 本文件「发布流程」+ 新增/更新 `release-notes/v<版本>.md`（与代码同一次提交） |
| 对外文字（README / 发布说明 / 界面文案）增改 | 按「对外文字的约定」自查，改完跑一次去 AI 味脚本 |

## 对外文字的约定

README、`release-notes/`、界面文案是给人读的散文，动手写或改之前先过下面两条。

**不点名具体第三方产品/平台。** 要交代适用场景就用类别描述（如「带代码补全的在线
编辑器」）。点名会把项目绑死在某个产品上，也让读者误以为只支持它。此前文本直投的
文案里出现过具体的在线作业平台名，已清理；新增文案别再引入（产品名即使在代码注释、
测试页里也不必出现，那里复刻的是行为而不是某个站点）。

**去 AI 味。** 规则全文在 `tools/less-ai-tone/SKILL.md`，实测依据是同目录的
RESEARCH.md，改完用同目录的脚本复量：

    python tools/less-ai-tone/scripts/check-translationese.py README.md release-notes/

三条要点：

- **不拿长破折号当分隔或揭晓。** 列表项的标签与说明之间用全角冒号，正文里要停顿
  就用逗号或句号。2026-09 清理前本仓库唯一超标的标记就是它：README 6.87/千字、
  本文件 5.22，人类基准 0.80、AI 均值 2.38
- **只改命中规则的那一处，不顺手润色。** 句长、顿号罗列、设问、比喻、被动句、
  名词化都是正常中文写法，SKILL.md 把它们列入"不作为改写理由"；照着"更像人写的"
  感觉去改，会把这些正常写法一起改坏
- **冻结文案与引用的界面字符串逐字保留**，永不在清理范围内。清理脚本曾把倒计时
  文案 `剩余 N 秒` 那一条里的长破折号当成分隔符误伤，改完要复核这类引用

代码注释不在此列，那是本仓库的内部记法，按工程习惯写就行。
