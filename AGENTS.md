# AGENTS.md：面向 AI 代理的工程约定

人类读者请看 [README.md](README.md)；本文件写给在此仓库工作的 AI 编码代理。
**本文件只放"每次动手都要先看"的东西**（铁律、常用命令、提交前检查表），其余按专题拆在
`docs/` 下（见下面的地图）。2026-10 拆分的原因：本文件当时已 68KB，超过一次能完整注入的
上限，AI 读到的其实是被截断的版本。搬运是逐行做的、小节标题未改，只有跨文件的「见…」指向
跟着更新。

**先记住四条铁律**（其余可以边做边查）：

1. **冻结文案一个字都不能改**（它们是发布语言，清单见 [docs/behavior-contract.md](docs/behavior-contract.md)）；可以新增，但新增必须登记进清单。
2. **改了 `frontend/src` 就必须跑 `scripts/build.ps1` 重新生成并提交 `internal/web/dist/index.html`**（go:embed 是静默的，本地不报错，CI 的漂移检查会失败）。
3. **版本号只改 `cmd/type/main.go` 一处**，其余由 build.ps1 与构建期资源生成自动同步。
4. **发版要连发布说明一起提交**（`release-notes/v<版本>.md`），打上 `v<版本>` 标签即由 CI 完成构建、校验与发布。

## 文档地图（动手前先对号入座）

| 你要动的东西 | 先读哪份 |
|---|---|
| 输入状态机 / 注入路径 / 并发 / 用户可见文案 | [docs/behavior-contract.md](docs/behavior-contract.md)（冻结清单）+ [docs/invariants.md](docs/invariants.md)（不变量与踩坑） |
| 构建 / CI / 发版 / 版本号 | [docs/build-and-release.md](docs/build-and-release.md) |
| 目录结构 / 分层 / 平台层导出面 / 工具链选型 | [docs/architecture.md](docs/architecture.md) |
| README / 发布说明 / 界面文案 | [docs/writing-style.md](docs/writing-style.md) |

**代码注释里的「见 …」直接指向对应文件**；小节标题在拆分时原样保留，搜标题最快。
本文件与 `docs/` 都随代码提交，改了哪一处的规矩就同步哪一份。

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


## 提交前：检查文档同步（每次提交必做）

代码改了文档没跟上，是本仓库反复出现过的问题（CI 描述、新增文案、行为语义都栽过）。
每次提交前对照下表过一遍 README.md（面向用户）与本文件（面向代理），
需要更新的与代码放进同一次提交，不要"下次再补"：

| 变更类型 | 同步位置 |
|---|---|
| 用户可见行为 / 功能 / 限制 | README.md：功能、使用步骤、已知限制 |
| 新增或修改用户可见文案 | docs/behavior-contract.md 的文案清单 |
| CI 步骤 / 检查项 / 发布方式 | README.md 技术栈与项目结构的 CI 行 + docs/architecture.md / docs/build-and-release.md |
| 命令 / 构建流程 / 目录结构 | 本文件「常用命令」+ docs/architecture.md + README「自行编译」；新增测试或工具目录时，确认 `.gitattributes` 的语言构成排除规则覆盖到（语言条只统计产品代码） |
| 非显而易见的新约定 / 踩坑结论 | docs/invariants.md 对应章节（如「焦点锁定与漂移防护」） |
| 版本号 | 只改 cmd/type/main.go；package.json 与 package-lock.json 交给 build.ps1，资源版本由 tools/mkres 构建期生成 |
| 发版 | docs/build-and-release.md + 新增/更新 `release-notes/v<版本>.md`（与代码同一次提交） |
| 对外文字（README / 发布说明 / 界面文案）增改 | 按 docs/writing-style.md 自查，改完跑一次去 AI 味脚本 |

