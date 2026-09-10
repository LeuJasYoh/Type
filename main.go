//go:build windows && (amd64 || arm64)

// INPUT 结构体的手工填充(40 字节)仅匹配 64 位 ABI; 386 下真实布局为 28 字节,
// SendInput 会静默注入乱码, 因此直接禁止 32 位编译
package main

import (
	_ "embed"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/webview/webview_go"
)

// ─── 嵌入前端资源 ─────────────────────────────────────

// Vite 构建的自包含单文件 (vue-tsc + vite build 生成于 frontend/dist/)

//go:embed frontend/dist/index.html
var indexHTML string

var version = "1.3.3"

var topmostFlag atomic.Bool // 窗口置顶开关(与输入任务无关, 归装配层)

// ─── 主程序 ───────────────────────────────────────────

func main() {
	w := webview.New(true)
	defer w.Destroy()
	w.SetTitle("Type " + version)
	w.SetSize(540, 450, webview.HintFixed)

	// 设置窗口图标（首次 + 延迟重试）
	hw := uintptr(w.Window())
	retrySetIcon(hw)

	// 初始化状态
	typingStatus.Store(&TypingStatus{Phase: PhaseIdle})

	// 绑定 Go 函数到 JS
	// (须在加载页面前完成: 绑定的注入脚本对随后创建的文档生效)

	w.Bind("startTyping", func(text string, delay int, forceSendInput bool) (string, error) {
		// 上一任务活跃且并非取消收尾: 拒绝重入
		if runningFlag.Load() && !cancelFlag.Load() {
			return "", fmt.Errorf("已有输入任务在运行中，请先取消或等待完成")
		}
		// 同步写入倒计时初态: 前端 await 本调用后才开启轮询,
		// 保证首个 tick 必读到新状态; 过代旧任务被代数守卫拦截, 无法覆盖
		gen := taskGen.Add(1)
		typingStatus.Store(&TypingStatus{
			Phase:        PhaseCountdown,
			Message:      fmt.Sprintf("剩余 %d 秒 — 请聚焦目标窗口...", delay),
			SecondsLeft:  delay,
			Progress:     -1,
			TargetWindow: foregroundWindowTitle(),
		})
		go runTypingTask(gen, text, delay, forceSendInput)
		return "started", nil
	})

	w.Bind("cancelTyping", func() (string, error) {
		// 递增代数使在途任务的所有后续状态写入作废, 取消标志则加速其退出;
		// 此处不做等待 —— 旧实现阻塞 UI 线程最长 2 秒导致窗口冻结,
		// "取消后立即启动"的衔接由 runTypingTask 自行等待旧任务让出 runningFlag
		taskGen.Add(1)
		cancelFlag.Store(true)
		typingStatus.Store(&TypingStatus{
			Phase: PhaseCancel, Message: "已取消", Progress: -1,
		})
		return "cancelled", nil
	})

	w.Bind("toggleTopmost", func() (bool, error) {
		on := !topmostFlag.Load()
		topmostFlag.Store(on)
		if hw != 0 {
			setTopmost(hw, on)
		}
		return on, nil
	})

	// 前端轮询读取当前输入状态
	w.Bind("getTypingStatus", func() *TypingStatus {
		return typingStatus.Load().(*TypingStatus)
	})

	// 加载界面: 开发模式指向 Vite dev server (支持 HMR, 需先 npm run dev),
	// 默认加载嵌入的自包含页面
	if url := devServerURL(); url != "" {
		w.Navigate(url)
	} else {
		w.SetHtml(indexHTML)
	}

	w.Run()
}

// devServerURL 开发模式页面地址: TYPE_DEV_URL 环境变量优先 (可指定任意端口),
// 其次 -dev 参数 (默认 5173); 均未设置时返回空串, 即生产模式
func devServerURL() string {
	if u := os.Getenv("TYPE_DEV_URL"); u != "" {
		return u
	}
	for _, arg := range os.Args[1:] {
		if arg == "-dev" {
			return "http://localhost:5173"
		}
	}
	return ""
}
