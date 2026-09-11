# AGENTS.md — 面向 AI 代理的工程约定

人类读者请看 [README.md](README.md)；本文件写给在此仓库工作的 AI 编码代理，
收录不成文的工程约定，避免每次会话重新考古。

## 常用命令

```powershell
# 一键构建 (版本同步 → 前端 → windres → go build, 产物 Type.exe 在仓库根;
# 末尾会读回 exe 版本资源, 版本没链进去就构建失败)
powershell -ExecutionPolicy Bypass -File ./scripts/build.ps1

# Go 验证三件套 (任何 Go 改动后, 与 CI 同款)
gofmt -l ./cmd ./internal ./tools   # 应输出为空
go vet ./...
go test -count=1 ./...

# 前端开发 (HMR): 必须带 dev 构建标签, 否则 exe 不含 dev server 代码路径
cd frontend; npm run dev            # 终端 1
go build -tags dev -o Type-dev.exe ./cmd/type   # 终端 2
.\Type-dev.exe                      # (-dev 参数或 TYPE_DEV_URL 指定端口)

# 图标资产再生成 (uv, 字节级可复现, 仅在更换 assets/icon.jpg 时需要)
uv run scripts/gen_icon.py

# 重构等价性验证 (逐函数比对函数体, 结构调整后证明零行为变化)
go run ./tools/equivcheck <旧rev> <新rev> [--renamed] [--old-file <路径>]
```

## 布局与单一来源

- `cmd/type/` — Go 入口包：业务层（typing.go 的 TypingService）+ Win32 平台层（win32_*.go）
  单实例守卫在 win32_instance.go；`devserver_prod.go` / `devserver_dev.go` 由 `dev` 构建标签二选一
- `internal/web/` — go:embed 前端产物包（dist/index.html 入库，免 Node 亦可 go build/test）
- `frontend/` — 自包含 Vite 项目（package.json / node_modules 都在这里，不在仓库根）
- `assets/` — 图标源图与产物、version.rc（screenshot.png 为 README 截图）
- `testdata/` — 手工测试页（paste-guard.html），无任何自动引用；用法写在文件头注释里
- `.github/workflows/ci.yml` — CI：gofmt / vet / test / build + 前端产物漂移检查 + 版本同步检查
- **版本号单一来源**：`cmd/type/main.go` 的 `version` 变量；build.ps1 自动同步到
  version.rc / package.json——改版本只改 main.go，然后跑 build.ps1
- **入库的前端产物必须与源码同步**：改了 `frontend/src` 就要跑 build.ps1 重新生成
  `internal/web/dist/index.html` 并一起提交；忘了的话 CI 的漂移检查会失败
  （go:embed 是静默的，本地不会有任何报错）

## 工具链分界（勿混用）

| 用途 | 工具 |
|---|---|
| 业务代码 / Go 测试 / 入库工具脚本 | Go（equivcheck 即 Go 写的） |
| 前端 | TypeScript + Vite |
| 构建编排 | PowerShell |
| 图标/图像资产管线 | Python，仅经 uv（pyproject 锁定 pillow==12.3.0） |

## 行为契约（冻结，改动需双端同步）

- webview Bind 函数名：`startTyping` / `cancelTyping` / `toggleTopmost` / `getTypingStatus`
- `TypingStatus` JSON 字段与 phase 枚举值（`frontend/src/types.ts` 是其镜像）
- 状态机用户可见文案逐字符保持——它们是发布语言的一部分
- 已冻结文案清单：`剩余 N 秒 — 请聚焦目标窗口...` / `检测到中文，正在操作剪贴板...` /
  `剪贴板操作失败` / `正在逐字符输入 N / M ...` / `输入完成` / `输入失败` /
  `已取消` / `启动失败：上一任务未能及时退出` / `已有输入任务在运行中，请先取消或等待完成`
- 允许**新增**文案（如注入被拒时的 `输入中断：目标窗口拒绝了模拟按键，可能其权限高于 Type`、
  `粘贴未生效：...`、`无内容可输入`、焦点未切换时的 `未切换到目标窗口：...`），
  但不得改写上面已冻结的那些

## 目标窗口与轮询（两条踩过坑的约定，勿"优化"掉）

- **目标窗口语义**：预览 = 当前前台窗口逐秒刷新（用户切到哪个窗口就显示哪个，
  含 Type 自身；不要引入"排除自身/显示占位"之类的过滤）；执行时锁定当时的前台
  窗口并贯穿到执行与终态状态；倒计时结束焦点仍在 Type 自身时报错并零注入。
  用户明确定调：所见即所选，程序只负责刷新
- **前端轮询是自续链条（v1.4.0 的教训）**：`startPolling` 必须以定时器点火
  （`setTimeout(0)`），不能直接调用 `tick`——tick 末尾用 `pollTimer !== null`
  判断"链条继续"，而 pollTimer 只在该判断保护的分支里赋值，直接调用会让链条
  第一拍后断裂、状态永不刷新（症状：预览不跟随、完成后启动键卡死）。
  恢复可见/焦点时无条件重启链条，作为 WebView2 挂起定时器的兜底

## 提交前：检查文档同步（每次提交必做）

代码改了文档没跟上，是本仓库反复出现过的问题（CI 描述、新增文案、行为语义都栽过）。
每次提交前对照下表过一遍 README.md（面向用户）与本文件（面向代理），
需要更新的与代码放进同一次提交，不要"下次再补"：

| 变更类型 | 同步位置 |
|---|---|
| 用户可见行为 / 功能 / 限制 | README.md：功能、使用步骤、已知限制 |
| 新增或修改用户可见文案 | 本文件「行为契约」的文案清单 |
| CI 步骤 / 检查项 | README.md 技术栈与项目结构的 CI 行 + 本文件「布局与单一来源」 |
| 命令 / 构建流程 / 目录结构 | 本文件「常用命令」「布局与单一来源」+ README「自行编译」 |
| 非显而易见的新约定 / 踩坑结论 | 本文件对应章节（如「目标窗口与轮询」） |
| 版本号 | 只改 cmd/type/main.go；version.rc / package.json 交给 build.ps1 |

## 其他

- Win32 怪癖的注释保留在 win32_*.go 实现内（全角标点 WM_CHAR 绕行、GDI 句柄型
  格式跳过、图标句柄所有权约定）——它们是本代码库最有价值的文档，重构时勿删
- 注入失败必须可见：`TextInjector` 三个方法都返回 bool（SendInput 被 UIPI 拦截时
  整体返回 0）。不要把返回值改成 void——那正是"静默失败被报成输入完成"的来源
- 剪贴板恢复守卫用 `Clipboard.HoldsText`（按原始字节比对）而不是 `GetText` 的
  字符串比较：CF_UNICODETEXT 内嵌 NUL 时字符串会在 NUL 处截断而误判
- 仅支持 `windows && (amd64 || arm64)`；386 编译被刻意禁止（INPUT 结构体手工
  填充仅匹配 64 位 ABI）
- 提交信息：中文一行式主题 + 正文说明要点
- 发版流程：改 `version` → `scripts/build.ps1` → 提交推送 →
  `gh release create vX.Y.Z ./Type.exe`（附件为根目录 Type.exe）
