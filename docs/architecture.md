# 架构、布局与单一来源

本文件从 AGENTS.md 拆出（2026-10），2026-10-20 重组：截图复现、版式探针、资产管线、语言统计
这些操作细节移到 docs/tooling.md，这里只留分层、目录与"改一处就够"的单一来源。

## 布局与单一来源

- 分三层四个包，依赖方向单向：平台层 → 业务层，装配层 → 两者（internal/viewfit 是第四个
  与平台无关的包，只承接"该不该再校正一次"这条规则）。
  - internal/typing/：业务层，TypingService 输入状态机 + 平台能力接口（消费方定义）+ 时序
    常量区；刻意不带构建约束，任何系统上都能 go test。一次任务占住槽之后怎么走完在
    taskrun.go（五个阶段：收尾、开工前置、倒计时与锁定、分流注入、终态）。
  - internal/viewfit/：内容缩放校正的节流器；只认"这个比值该不该再摆一次窗口"，不含任何
    Win32 调用，怎么摆窗口由装配层传入。
  - internal/win32/：平台层，三个接口的 Win32 实现。win32.go 是 DLL 入口清单页；
    keyboard / clipboard / foreground / size（尺寸与 DPI）/ window（置顶）/ icon / instance /
    msgbox / webview2 各管一摊。实现与业务层接口的绑定在 win32.go 末尾以 `var _ typing.X = ...`
    编译期核对；业务层不得反向依赖本包。
  - internal/web/：go:embed 前端产物包（dist/index.html 入库，免 Node 亦可 go build/test）。
  - cmd/type/：装配层，main.go 开窗与 Bind 绑定（平台能力在此注入）；devserver_prod.go /
    devserver_dev.go 由 dev 构建标签二选一（dev server 地址与 WebView2 调试模式）；
    构建约束 windows && (amd64 || arm64)。
- frontend/：自包含 Vite 项目（package.json / node_modules 都在这里）。src 只放产品代码；
  test/ 用 Node 自带测试跑器，零新依赖；tsconfig.json 只含 src，test/** 由 tsconfig.test.json
  收口（node 全局类型不能并进主配置，否则会抢走浏览器侧 setTimeout 的返回值）。
- tools/：不进产品链路的工具，各自一个目录：mkres（资源生成）、pecheck（产物读回校验）、
  wmcharprobe（注入探针）、equivcheck（重构等价性）、gen-icon（资产管线）、less-ai-tone
  （对外文字规则）、repo-check（仓库治理闸门，test-only 包；go test ./... 会跑到）。
  操作手册见 docs/tooling.md。
- docs/ 分四层：契约（behavior-contract / architecture / comment-style / build-and-release /
  writing-style）、知识（invariants）、工具手册（tooling）、规划（roadmap）。注释重写的
  一次性档案已移出工作区，git 历史保存快照，工作区只留 docs/comment-ledger/README.md 指针。

## 单一来源（改一处就够）

| 事实 | 唯一来源 | 谁校验 |
|---|---|---|
| 版本号 | cmd/type/main.go 的 `var version`；提取实现 scripts/version.ps1 | build.ps1 / 三个 workflow 共用；repo-check 断言不许有第二份提取正则 |
| 冻结文案与跨端契约 | docs/behavior-contract.md | internal/typing 与 cmd/type 的契约测试 |
| 注释规则 | docs/comment-style.md | tools/repo-check 的注释/指针/链接闸门 |
| README 项目结构树 | README.md | tools/repo-check 双向核对 |
| 文档指针与链接 | docs/ 标题与真实文件 | tools/repo-check |
| 检查项 | .github/workflows/verify.yml | ci.yml 与 release.yml 复用（只有这一处定义） |
| Node 主版本 | .node-version | verify.yml 读它；build.ps1 构建前核对 |
| 前端产物 | frontend/src → build.ps1 → internal/web/dist/index.html | verify.yml 的 git hash-object 漂移检查 |
| 规划 | docs/roadmap.md（规划不是契约，冲突以 README / behavior-contract 为准） | 人工，每版本窗口更新一次 |

### 版本号与构建环境

- 版本可带预发布后缀（如 `1.5.0-rc.1`）：资源里的数字字段取后缀前的数字部分，ProductVersion /
  package.json 用完整串。**提取实现只有 scripts/version.ps1 的 `Get-TypeVersion` 一处**：
  正则锚定行首 `var version =`、大小写敏感、断言恰好一处；build.ps1 与 verify/ci/release
  三个 workflow 都 dot-source 它。以前四处手写同款正则、靠逐字比对防走散，现在只有一份。
- build.ps1 自动同步 package.json 与 package-lock.json 的**两处根版本**（顶层与 `packages[""]`，
  依赖自己的 version 不碰），并有位置守卫与读回核对。lock 里有一个空字符串键，所以 verify.yml
  刻意用正则而不是 `ConvertFrom-Json`（PS 5.1 遇到空键会报 `argument "name" is not valid`）。
- build.ps1 自己钉构建环境（`GOARCH=amd64`、`GOFLAGS` 清空，结束还原），避免调用者会话里遗留的
  环境变量把构建带歪。脚本必须保留 UTF-8 BOM：Windows PowerShell 5.1 对无 BOM 的 .ps1 按系统
  ANSI 解码，中文注释会吞掉换行成为死代码；repo-check 与 verify.yml 各有一道 BOM 断言。
- **build.ps1 现在也在 CI 执行**：ci.yml 的 build 作业在 amd64 档跑一次完整构建（版本同步 →
  前端 → 资源 → go build → 读回），与本地同一路径；以前 CI 只校验它的 BOM。

### 入库前端产物

改 `frontend/src` 就必须跑 `scripts/build.ps1` 重新生成并提交 `internal/web/dist/index.html`
（go:embed 是静默的，本地不报错，CI 的漂移检查会失败）。verify.yml 用 `git hash-object` 比对
HEAD 产物与新构建产物（套用与 git diff 相同的行尾归一，Windows CRLF 检出不会误报）。
.vue/.css/index.html 在 .gitattributes 里锁 LF，避免产物字节随平台漂移。

### CI / 发布

- `verify.yml`：检查项的唯一处（`on: workflow_call`），含 gofmt / vet / `-race` 测试 /
  govulncheck / go build / build.ps1 BOM / 前端 format:check / typecheck / test / 产物漂移 /
  版本同步；仓库治理闸门随 `go test ./...` 一起跑。
- `ci.yml`：verify + build 两个作业；build 按 amd64/arm64 矩阵生成资源、发布参数构建、
  pecheck 读回校验，arm64 另做测试编译，amd64 跑一次 build.ps1 全链路。
- `release.yml`：发版（见 build-and-release.md）；release 作业 `needs: verify`，发布包不可能
  出自检查不过的提交。

## 工具链分界（勿混用）

| 用途 | 工具 |
|---|---|
| 业务代码 / Go 测试 / 仓库治理闸门 / 工具脚本 | Go |
| 前端 | TypeScript + Vite；单测用 Node 自带测试跑器（零新依赖） |
| 构建编排 / 发布打包 | PowerShell（build.ps1 / version.ps1 / release.yml 的步骤） |
| Windows 资源生成（图标/版本信息） | Go（tools/mkres，winres 库），取代 windres + .rc |
| 构建产物校验（PE 架构 / 资源读回） | Go（tools/pecheck，仅构建期使用） |
| 图标/图像资产管线 | Python，仅经 uv（tools/gen-icon 是自包含项目，锁定 pillow 版本） |

构建链**不含 C 编译器**：webview 绑定是纯 Go，资源由 mkres 生成，go build / go test 都不需要 cgo；
新增依赖时别把 cgo 带回来。唯一例外是 `go test -race`：Windows 上竞态检测器要 cgo，需要
MinGW 的 gcc（测试期依赖，产品构建与发布不需要）。

## GitHub 的语言构成只统计产品代码

仓库页的语言条**只代表产品代码**：测试与构建/工具链都不计入（判据是"这文件是不是产品的一部分"，
不是"它重不重要"）。规则用 Linguist 的 `-linguist-detectable` 开关写在 .gitattributes 里，
只影响统计与语言条，不影响语法高亮与任何构建。覆盖范围与新增目录的注意事项见
docs/tooling.md「语言统计与 .gitattributes」；**具体字节数不写进文档**（写过就会漂，需要的
时候按 .gitattributes 的口径现场统计）。
