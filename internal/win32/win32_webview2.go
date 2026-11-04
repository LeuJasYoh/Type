//go:build windows && (amd64 || arm64)

// ─── WebView2 启动提示 ────────────────────────────────
// 界面由 MyGo 托管的 WebView2 渲染, 而 exe 是 GUI 子系统(没有控制台): 运行时
// 缺失或初始化失败时, 用户看到的只是"双击之后什么都没有"。预检在装配层做,
// 这里把原因交由原生提示框说出去; 此刻 App.Run 尚未开始, 框架的 Dialog 不可用。

package win32

import (
	"os"
	"syscall"
	"unsafe"
)

// webview2DownloadURL 与 README「系统要求」一栏给用户的下载地址是同一处
const webview2DownloadURL = "https://developer.microsoft.com/microsoft-edge/webview2/"

const (
	webview2PromptTitle = "Type 无法启动"
	// 两条正文都是发布语言, 逐字固定(见 docs/behavior-contract.md「行为契约（冻结，改动需双端同步）」)。目标读者是
	// 从没听说过"运行时"的人: 只需要说清缺什么、点哪里、之后做什么
	MsgWebView2Missing = "缺少 Microsoft Edge WebView2 运行时，Type 的界面需要它才能显示。\n\n" +
		"点\"是\"打开微软官方下载页；装好后重新运行 Type 即可，不必重启电脑。\n" +
		"点\"否\"直接退出。"
	// 运行时查得到、创建仍失败时用这条(如装的是损坏的运行时, 或被策略拦下)
	MsgWebView2InitFailed = "WebView2 初始化失败，Type 的界面无法创建。\n\n" +
		"点\"是\"打开微软官方下载页；装好或修复后重新运行 Type 即可。\n" +
		"点\"否\"直接退出。"
	// 界面能创建、但前后端绑定失败时用这条: 按钮全都接不上, 与"界面起不来"一样
	// 只能靠原生提示框把话说出去
	MsgBindFailed = "界面初始化失败，Type 无法把界面与后台接起来。\n\n" +
		"点\"是\"打开下载页重新安装；点\"否\"直接退出。"
)

// PromptWebView2Unusable 把"界面起不来"的原因交到用户手上: 点"是"打开官方
// 下载页, 然后以非零退出码收尾 —— 这是"没能启动", 不是"用户主动关掉", 便于
// 脚本与支持排查。不返回(提示之后流程到此为止)
func PromptWebView2Unusable(text string) {
	if messageBox(webview2PromptTitle, text,
		mbYesNo|mbIconWarning|mbSetForeground|mbTopmost) == idYes {
		openInBrowser(webview2DownloadURL)
	}
	os.Exit(1)
}

// openInBrowser 交给系统默认浏览器打开。界面起不来时程序自己没有浏览器可用,
// 这是唯一还能把用户送到下载页的办法
func openInBrowser(url string) {
	verb, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return
	}
	file, err := syscall.UTF16PtrFromString(url)
	if err != nil {
		return
	}
	const swShownormal = 1
	procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		0, 0, swShownormal)
}
