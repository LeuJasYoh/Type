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
- **现代化 UI** —— WebView2 + TypeScript 渲染

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
前端      TypeScript → 编译为 JavaScript（无框架，零运行时依赖）
构建      tsc (TypeScript) + windres + go build
Win32 API SendInput (KEYEVENTF_UNICODE) + 剪贴板 (CF_UNICODETEXT, RtlMoveMemory) + 前台窗口检测 (GetForegroundWindow)
图标      圆角多尺寸 ICO（Pillow 生成）
资源      windres 编译 .rc → .syso
```

### 项目结构

```
Type/
├── Type.exe              ← 可执行文件 (构建产物, 不入库)
├── main.go                 ← Go 入口 + Win32 API 调用
├── main_test.go            ← 单元测试 (UTF-16 拆分/ASCII/CJK 标点判定)
├── frontend/
│   ├── index.html          ← 界面骨架
│   ├── style.css           ← 界面样式（Fluent Design 风格）
│   ├── script.js           ← tsc 编译产物 (由 ts/script.ts 生成, 不入库)
│   ├── ts/
│   │   ├── script.ts       ← 前端逻辑源码（TypeScript）
│   │   └── global.d.ts     ← webview 全局绑定类型声明
│   └── tsconfig.json       ← TypeScript 编译配置
├── source/
│   ├── icon.ico            ← 应用图标 (version.rc 引用, windres 编译进 exe)
│   ├── icon.jpg            ← 图标源图
│   └── screenshot.png      ← README 截图
├── version.rc              ← 版本/作者信息资源
├── version.syso            ← 编译后的资源文件 (构建产物, 不入库)
├── winres/                 ← winres 格式资源定义 (winres.json + 多尺寸 PNG)
├── build.ps1               ← 一键构建脚本（版本号单一来源）
├── go.mod / go.sum         ← Go 模块定义
├── package.json / package-lock.json ← npm 定义（仅 devDependency: typescript）
└── .gitignore              ← 忽略构建产物/依赖/工具元数据
```

---

## 自行编译

```powershell
# 前置条件
#   - Go 1.26+ (与 go.mod 声明一致)
#   - MinGW-w64 (gcc, windres)
#   - Node.js 16+ (仅构建期需要, 产物无需)
#   - WebView2 库（go mod tidy 自动下载）

cd Type
npm install                # 安装 typescript (仅首次)
go mod tidy

# 一键构建 (推荐): 编译 TS → 同步版本号 → windres → go build
powershell -ExecutionPolicy Bypass -File .\build.ps1

# 或手动分步:
npx tsc -p frontend/tsconfig.json   # 编译 TypeScript → frontend/script.js
windres -o version.syso version.rc
go build -ldflags="-H windowsgui -s -w" -o Type.exe .
```

---

## 输入错误说明

`SendInput` + `KEYEVENTF_UNICODE` 对全角标点（U+FF00~FFEF）存在系统级处理异常，表现为标点重复、后续字符被吞。**解决方案**：程序检测到文本含中文时，自动降级为剪贴板 `Ctrl+V` 模式，保证输入准确无误。

---

## 作者

**LeuJasYoh**

---

## 许可

本项目仅供学习交流使用。

