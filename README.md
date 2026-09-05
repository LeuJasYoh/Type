# Type

**键盘模拟输入器** —— 在文本框中输入内容，5 秒后自动模拟键盘键入到任意目标窗口。

![Type 截图](source/screenshot.png)

---

## 功能

- **多行文本输入** —— 支持中文、英文、符号、换行
- **两种输入模式**：
  - **逐字符模拟** —— 纯英文用 `SendInput` 快速注入，含中文时自动切换为剪贴板
  - **剪贴板粘贴** —— 用 `Ctrl+V` 粘贴，速度快，自动保存/恢复剪贴板
- **目标窗口预览** —— 倒计时期间实时显示当前前台窗口标题，确认焦点已切对
- **可调延迟** —— 1~10 秒倒计时，给你时间聚焦目标窗口
- **启动/取消** —— 随时中止操作；运行中防重入，不会叠加启动
- **实时状态** —— 倒计时显示、输入进度、完成提示
- **可靠的剪贴板操作** —— 占用时自动重试；粘贴后仅在剪贴板未被用户改动时才恢复旧内容，避免覆盖新复制的数据
- **现代化 UI** —— WebView2 + Vue 3 渲染

---

## 使用

1. 双击 `Type.exe`
2. 在文本框中输入要模拟键入的内容
3. 勾选“绕过粘贴检测”可强制逐字符输入（默认含中文自动走剪贴板）
4. 调节倒计时秒数
5. 点击 **启动**
6. 在倒计时结束前将鼠标焦点切换到目标窗口（如记事本、浏览器、聊天框等）
7. 程序自动完成输入

---

## 系统要求

| 组件 | 要求 |
|------|------|
| 操作系统 | Windows 10 / 11（x64） |
| 运行时 | **无** — 单文件，零依赖 |
| WebView2 | Windows 11 预装，Windows 10 需安装 [WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/) |

---

## 技术栈

```
语言      Go 1.26
GUI       WebView2 (Edge Chromium)
前端      Vue 3 + TypeScript, Vite 构建为单文件 HTML (无其他运行时依赖)
构建      vue-tsc 类型检查 + Vite (vite-plugin-singlefile) + windres + go build
Win32 API SendInput (KEYEVENTF_UNICODE) + 剪贴板 (CF_UNICODETEXT, RtlMoveMemory) + 前台窗口检测 (GetForegroundWindow)
图标      圆角多尺寸 ICO（Pillow 生成）
资源      windres 编译 .rc → .syso
```

### 项目结构

```
Type/
├── Type.exe              ← 可执行文件 (构建产物, 不入库)
├── main.go                 ← Go 入口 + Win32 API 调用 + webview 绑定
├── main_test.go            ← 单元测试 (UTF-16 拆分/ASCII/CJK 标点判定)
├── frontend/               ← Vue 前端 (Vite 项目根)
│   ├── index.html          ← Vite 入口
│   ├── tsconfig.json       ← TypeScript 配置 (vue-tsc)
│   ├── dist/
│   │   └── index.html      ← 构建产物: 自包含单文件 (go:embed 引用, 入库)
│   └── src/
│       ├── main.ts         ← 应用入口 (createApp)
│       ├── App.vue         ← 界面骨架
│       ├── style.css       ← 界面样式（Fluent Design 风格）
│       ├── ipc.ts          ← webview Bind 全局绑定的类型化封装
│       ├── types.ts        ← TypingStatus/TypingPhase (与 Go 端结构对应)
│       ├── composables/
│       │   └── useTypingTask.ts ← 输入任务状态机 + 状态轮询
│       └── components/
│           └── StatusBar.vue    ← 状态栏 + 进度条
├── vite.config.ts          ← Vite 配置 (单文件打包)
├── source/
│   ├── icon.ico            ← 应用图标 (version.rc 引用, windres 编译进 exe)
│   ├── icon.jpg            ← 图标源图
│   └── screenshot.png      ← README 截图
├── version.rc              ← 版本/作者信息资源
├── version.syso            ← 编译后的资源文件 (构建产物, 不入库)
├── winres/                 ← winres 格式资源定义 (winres.json + 多尺寸 PNG)
├── build.ps1               ← 一键构建脚本（版本号单一来源）
├── go.mod / go.sum         ← Go 模块定义
├── package.json / package-lock.json ← npm 定义 (vue / vite / vue-tsc 等)
└── .gitignore              ← 忽略构建产物/依赖/工具元数据
```

---

## 自行编译

```powershell
# 前置条件
#   - Go 1.26+ (与 go.mod 声明一致)
#   - MinGW-w64 (gcc, windres)
#   - Node.js 20+ (前端构建期需要, 产物无需)
#   - WebView2 库（go mod tidy 自动下载）

cd Type
npm install                # 安装前端依赖 (仅首次)
go mod tidy

# 一键构建 (推荐): 版本号同步 → Vue 前端构建 → windres → go build
powershell -ExecutionPolicy Bypass -File .\build.ps1

# 或手动分步:
npm run build                       # vue-tsc 类型检查 + Vite → frontend/dist/index.html
windres -o version.syso version.rc
go build -ldflags="-H windowsgui -s -w" -o Type.exe .
```

---

## 前端开发模式 (HMR)

```powershell
npm run dev                 # 终端 1: Vite dev server (http://localhost:5173)
Type.exe -dev               # 终端 2: 窗口指向 dev server, 改代码即时热更新
```

`-dev` 模式下 webview 的 Go 绑定照常工作，可完整调试 IPC 链路。

---

## 输入错误说明

`SendInput` + `KEYEVENTF_UNICODE` 对全角标点（U+FF00~FFEF）存在系统级处理异常，表现为标点重复、后续字符被吞。**解决方案**：程序检测到文本含中文时，自动降级为剪贴板 `Ctrl+V` 模式，保证输入准确无误。

---

## 作者

**LeuJasYoh**

---

## 许可

本项目基于 [MIT License](LICENSE) 开源发布。

