# AGENTS.md — 面向 AI 代理的工程约定

人类读者请看 [README.md](README.md)；本文件写给在此仓库工作的 AI 编码代理，
收录不成文的工程约定，避免每次会话重新考古。

## 常用命令

```powershell
# 一键构建 (版本同步 → 前端 → windres → go build, 产物 Type.exe 在仓库根)
powershell -ExecutionPolicy Bypass -File ./scripts/build.ps1

# Go 验证三件套 (任何 Go 改动后)
gofmt -l ./cmd ./internal ./tools   # 应输出为空
go vet ./...
go test -count=1 ./...

# 前端开发 (HMR)
cd frontend; npm run dev            # 终端 1
..\Type.exe -dev                    # 终端 2 (或 TYPE_DEV_URL 环境变量指定端口)

# 图标资产再生成 (uv, 字节级可复现, 仅在更换 assets/icon.jpg 时需要)
uv run scripts/gen_icon.py

# 重构等价性验证 (逐函数比对函数体, 结构调整后证明零行为变化)
go run ./tools/equivcheck <旧rev> <新rev> [--renamed]
```

## 布局与单一来源

- `cmd/type/` — Go 入口包：业务层（typing.go 的 TypingService）+ Win32 平台层（win32_*.go）
- `internal/web/` — go:embed 前端产物包（dist/index.html 入库，免 Node 亦可 go build/test）
- `frontend/` — 自包含 Vite 项目（package.json / node_modules 都在这里，不在仓库根）
- `assets/` — 图标源图与产物、version.rc、winres/（screenshot.png 为 README 截图）
- `testdata/` — 手工测试页（paste-guard.html），无任何自动引用
- **版本号单一来源**：`cmd/type/main.go` 的 `version` 变量；build.ps1 自动同步到
  version.rc / winres.json / package.json——改版本只改 main.go，然后跑 build.ps1

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

## 其他

- Win32 怪癖的注释保留在 win32_*.go 实现内（全角标点 WM_CHAR 绕行、GDI 句柄型
  格式跳过、图标句柄所有权约定）——它们是本代码库最有价值的文档，重构时勿删
- 仅支持 `windows && (amd64 || arm64)`；386 编译被刻意禁止（INPUT 结构体手工
  填充仅匹配 64 位 ABI）
- 提交信息：中文一行式主题 + 正文说明要点
- 发版流程：改 `version` → `scripts/build.ps1` → 提交推送 →
  `gh release create vX.Y.Z ./Type.exe`（附件为根目录 Type.exe）
