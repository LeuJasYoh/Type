# 开发工具与操作手册

本文件收纳"要做某件具体事时照做"的操作细节：README 配图、版式探针、注入探针、资产管线、
重构等价性、去 AI 味、语言统计。分层与单一来源见 docs/architecture.md；这里只写怎么做。

## README 配图的复现

固定 1152×960（逻辑 576×480 / 2 倍像素密度，与窗口尺寸下限档一致）：

1. 临时页 = internal/web/dist/index.html 开头插一段打桩脚本（定义 startTyping / getTypingStatus
   等绑定，按运行态摆出目标窗口条与进度条），再补一段停用 transition/animation 的样式
   （否则进度条入场动画在无头下停在 0 高度）；用本地 HTTP 服务提供（file:// 下 localStorage
   不可用，主题没法显式指定）。
2. 系统已装的 Edge 无头截图：`msedge --headless=new --force-device-scale-factor=2 --window-size=576,480
   --virtual-time-budget=6000 --user-data-dir=<临时目录> --screenshot=<out.png> "<URL>?theme=<light|dark>"`，
   不弹窗口、不碰桌面。
3. 两张图必须同一次生成（同一份产物、同一档尺寸），否则明暗对不上；改窗口尺寸下限时一起重截。
4. 已验证的死路别再走：内置浏览器截图通道在非 1:1 像素密度下会把画面平铺成多份；截真实窗口
   （改窗口尺寸/截屏）会干扰用户桌面。

## 版式探针 frontend/tools/layout-probe.mjs

改版式、定尺寸、怀疑"文案会不会被裁"时先用它，别靠读 CSS 估：

    node frontend/tools/layout-probe.mjs --sizes 576x480,610x509,648x540 --shot

它把构建产物（先跑 npm run build 让它最新）复制到临时目录，注入打桩绑定与收集脚本，用无头
Edge 逐档量真实几何：scrollHeight vs clientHeight（裁不裁）、scrollWidth vs clientWidth
（横向溢出）、.input-wrap 与 .status-bar 实测高、状态块是否贴底、文案折几行。
--scenario full 加目标条与进度条；--shot 另存截图；--out 换工作目录。
三个踩过的坑：① 起 Edge 必须用异步 spawn（spawnSync 会锁住同进程里的 HTTP 服务）；
② --dump-dom 下 --window-size 是含窗口框的外框且系统有最小窗口宽度，--screenshot 下等于视口；
③ 每档必须用独立 --user-data-dir（撞车时 Edge 静默退出码 21）。它不进 CI 也不进 tsconfig，
绑定的桩清单以 frontend/src/ipc.ts 为准，Bind 改名时记得同步。

## 注入通道探针 tools/wmcharprobe

WM_CHAR 文本直投 vs SendInput 按键的 A/B 验证；direct 模式逐字镜像产品的文本直投算法，
取焦点窗口的规则与产品同步（拿不到焦点子窗口就放弃 WM_CHAR 投递，不拿顶层窗口顶替）。
读 testdata/completion-guard.html 的 title 遥测（c/k/n/p/a/L/h）作判据；用法见文件头注释。
它的边界与产品注解在 docs/invariants.md「文本直投」「注入通道的进程内读回」。

## 图标资产管线 tools/gen-icon

仅在更换 assets/icon.jpg 时需要，产物字节级可复现。它是自包含 uv 项目（pyproject / uv.lock /
.python-version / .venv 都在那里），所以要在仓库根用：

    uv run --directory tools/gen-icon gen_icon.py

脚本靠向上找 go.mod 定位仓库根，别改回按层数数 parents[N]（它搬过一次家，按层数会指错目录）。

## 重构等价性验证 tools/equivcheck

逐函数比对函数体（token 序列），用于结构调整后证明零行为变化：

    go run ./tools/equivcheck <旧rev> <新rev> [--renamed] [--old-file <路径>]

它只比函数体：签名/参数顺序/返回值/包级常量/结构体字段与 tag 都在视野之外；旧侧只读
--old-file 指定的一个文件，跨多文件要逐个跑。边界与注意事项见 docs/invariants.md
「取证与工具边界」。

## 对外文字去 AI 味 tools/less-ai-tone

写、改 README / release-notes / 界面文案前先读 tools/less-ai-tone/SKILL.md；规则依据与实测在
同目录 RESEARCH.md，改完用脚本复量：

    python tools/less-ai-tone/scripts/check-translationese.py README.md release-notes/

## 构建期工具 mkres 与 pecheck

- mkres：生成图标 + 版本信息 + DPI 感知 manifest → cmd/type/version_<arch>.syso（构建期生成，
  不入库）。资源缺失时 go build 不报错，所以发布构建必须配合 pecheck 读回。
- pecheck：读回校验产物，核对 PE 架构 + 图标/版本/manifest 是否真的链进 exe（发版与 CI 共用）。
  不数图标帧数，那是 assets/icon.ico 的属性，重新生成图标就会变。
- **图标组的资源 ID 必须是数字 1**：mygo 注册窗口类时用 `LoadIconW(instance(), 1)` 取应用图标。
  mkres 早期按字符串名 `IDI_ICON1` 写资源（go-webview2 时代靠 `RetrySetIcon` 从 exe 重新提取，
  不依赖 ID），迁移后 ID 对不上就会让标题栏与任务栏退回系统默认图标。pecheck 会钉住这条，
  别改回按名字写。

## 语言统计与 .gitattributes

仓库页的语言条只统计产品代码（判据是"这文件是不是产品的一部分"）：测试与构建/工具链用
Linguist 的 `-linguist-detectable` 开关排除，规则写在 .gitattributes。它只影响统计与语言条，
不影响语法高亮，也不影响任何构建（文件照旧被构建读取）。

覆盖范围：`*_test.go`、`frontend/test/**`、`frontend/tools/**`、`scripts/**`、`tools/**`、
`*.config.ts`。新增顶层测试或工具目录时，记得让规则覆盖到；否则它会带着自己的字节数重新挤进
语言条。补丁文件、图标等二进制资产不参与统计。

## 自养的精简框架 third_party/mygo

Type 的窗口壳依赖 [mygo](https://github.com/egoist/mygo)，而框架是"全家桶"：TS 生成器、
更新器、下载器、内容拦截、i18n、PDF、文件监视全在同一个包里。其中只有窗口与 WebView2
宿主是 Type 用得到的，其余部分连同它们拖进来的 `net/http`、`crypto/tls`、`go/parser`
一起进发布产物——那是 10.42 MB 里的 6 MB。

所以本仓保留一份改过的 mygo：`third_party/mygo` 是**独立模块**（自带 go.mod），根的
`go.mod` 用 `replace github.com/egoist/mygo => ./third_party/mygo` 指过去。独立模块这个
形态有两个好处：不进根的 `go test ./...`，也不进 `tools/repo-check` 的扫描范围（它的
目录清单里没有 third_party）。

**已经删掉的**（Type 不用，删了不影响窗口）：

| 部分 | 为什么删 | 省下 |
|---|---|---|
| `internal/tsgen` 与 TS 客户端生成 | `BindAs` 会链接 `go/parser` 整套编译器前端，而前端是手写的 ipc.ts | 0.43 MB |
| `updater.go` / `downloads.go` | 应用不自动更新、页面不产生下载 | 0.17 MB |
| `protocol.go` 的 `http.Handler` 层 | 换成整份返回字节；Type 只用 `LoadHTML`，`mygo://` 只服务内嵌页面 | 0.96 MB |
| 内容拦截（`content_rules.go` + `internal/contentrules`） | 没有广告拦截需求；它经 `x/net/publicsuffix` 拖进 `net/http` 与 `cookiejar` | 1.29 MB |
| 非 Windows 后端（darwin/linux/js）、cmd/mygo、examples、docs、plugins | 本仓只在 Windows 构建 | 仓库 3.8 MB |

**同步上游的方式**（框架升级时照着做）：

1. 把新版本的 mygo 覆盖进 `third_party/mygo`（先 `chmod -R u+w`，模块缓存里的文件是只读的）。
2. 重放上表的删改。改动处都有 `本仓` 开头的注释，`git grep 本仓 third_party/mygo` 能找到。
3. 跑 `go mod tidy`，再按下面的清单验证。

**改完必须验证的四件事**：`go build ./...`、`go test ./...`（根的，不含 third_party）、
`go run ./tools/pecheck`（图标/版本/manifest 仍在），以及**真的启动一次 exe 看界面**——
scheme 层与页面加载动过之后，编译通过并不能证明页面画得出来。
