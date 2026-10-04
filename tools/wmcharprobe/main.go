//go:build windows

// wmcharprobe — WM_CHAR 文本直投通道验证工具(开发用, 不进产品链路)。
// 靶子是 testdata/completion-guard.html(复刻"补全弹窗 + 括号配对"的在线编辑器):
// 那些行为全挂在 keydown 层, 而本工具经 WM_CHAR 把文本直投到页面、不产生按键事件。
// WM_CHAR 按窗口句柄直达、与焦点无关(char/direct 仍要先激活目标才能解析焦点子窗口)。
// 实测背景与产品侧算法见 docs/invariants.md「文本直投（v1.5.0，WM_CHAR 文本层注入）」。

// 用法(例: wmcharprobe completion-guard @D:/tmp/cn.txt direct):
//	wmcharprobe <标题子串> <文本> [char|direct|keys|close]  注入文本后打印标题
//	wmcharprobe <标题子串>                                  只打印匹配窗口的当前标题
//	char   — WM_CHAR 直投全部字符(含换行), 需把目标窗口激活成前台
//	direct — 镜像产品的"文本直投"算法: 字符(含 Tab)走 WM_CHAR; 换行前先 Esc 再发真回车
//	keys   — SendInput KEYEVENTF_UNICODE(与 Type 逐字符路径同款, 有按键事件),
//	         倒计时 2 秒后注入到当时的前台窗口
//	close  — 向匹配窗口发 WM_CLOSE(测试收尾清理)
//	文本可写 @路径 从 UTF-8 文件读取(含中文等非 ASCII 时推荐, 避开命令行编码)

// 取焦点窗口(char/direct 模式, 与产品 focusedHWND 同源):
//	先把匹配窗口带到前台, 再取前台线程的焦点子窗口(GetGUIThreadInfo), 并要求其根窗口
//	就是被匹配的那个顶层窗口, 否则放弃投递。
//	拿不到焦点子窗口时不退回顶层窗口 —— 该场景产品已退化为按键注入, 探针只测 WM_CHAR,
//	没有落点就如实说没有(理由见 focusedTarget)。
//	keys 模式注入的是当时的前台窗口: 倒计时 2 秒给操作者切窗口, 前台不是匹配窗口就退出。

// 成功判据(对照 completion-guard.html 的 title 遥测, 键义见该页遥测注释):
//
//	c=字符数(按码点) k=键盘事件 n=无键输入 p=自动配对 a=接受候选 L=换行数 h=内容哈希;
//	char/direct — c=文本码点数, k 不增(无按键事件), p/a 不增(配对/接受未触发);
//	keys 模式 — p 应增加(按键层行为被触发)。
//	两者对照即证明文本层通道绕过了 keydown 层; 换行会被 Chromium 过滤, 只有真按键才落 L。
package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

const (
	WM_CHAR           = 0x0102
	SMTO_ABORTIFHUNG  = 0x0002
	INPUT_KEYBOARD    = 1
	KEYEVENTF_KEYUP   = 0x0002
	KEYEVENTF_UNICODE = 0x0004

	VK_TAB    = 0x09
	VK_RETURN = 0x0D
	VK_ESCAPE = 0x1B
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procEnumWindows         = user32.NewProc("EnumWindows")
	procEnumChildWindows    = user32.NewProc("EnumChildWindows")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
	procGetClassNameW       = user32.NewProc("GetClassNameW")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procSendMessageTimeoutW = user32.NewProc("SendMessageTimeoutW")
	procSendInput           = user32.NewProc("SendInput")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
	procBringWindowToTop    = user32.NewProc("BringWindowToTop")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procAttachThreadInput   = user32.NewProc("AttachThreadInput")
	procGetGUIThreadInfo    = user32.NewProc("GetGUIThreadInfo")
	procGetWindowThreadPID  = user32.NewProc("GetWindowThreadProcessId")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procGetCurrentThreadId  = kernel32.NewProc("GetCurrentThreadId")
	procGetAncestor         = user32.NewProc("GetAncestor")
)

// GUITHREADINFO 与 internal/win32/win32_window.go 同款: 解析前台线程的焦点窗口
type GUITHREADINFO struct {
	cbSize        uint32
	flags         uint32
	hwndActive    uintptr
	hwndFocus     uintptr
	hwndCapture   uintptr
	hwndMenuOwner uintptr
	hwndMoveSize  uintptr
	hwndCaret     uintptr
	rcCaret       [4]int32
}

// KEYBDINPUT/INPUT 与 internal/win32/win32_keyboard.go 同款: 手工填充仅匹配
// 64 位 ABI (开发机工具, 与产品同受 386 禁止约束, 不必可移植)
type KEYBDINPUT struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type INPUT struct {
	_type uint32
	_     [4]byte
	ki    KEYBDINPUT
	_     [8]byte
}

// target 一个匹配的顶层窗口: 句柄 + 标题 + 选定的投递目标(渲染子窗口优先)
type target struct {
	hwnd   uintptr
	title  string
	sendTo uintptr
}

func windowText(hwnd uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return strings.ToLower(syscall.UTF16ToString(buf[:n]))
}

func windowClass(hwnd uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

// findRenderChild 按层(广度优先)查找渲染子窗口, 命中返回句柄并把探寻
// 路径写入 path 供诊断。现代 Chromium 的 Chrome_RenderWidgetHostHWND
// 可能嵌在中间层(如 Intermediate D3D Window)之下, 只扫一层会漏
func findRenderChild(parent uintptr, path *[]string) uintptr {
	level := []uintptr{parent}
	for depth := 0; depth < 4 && len(level) > 0; depth++ {
		var next []uintptr
		for _, h := range level {
			var hit uintptr
			cb := syscall.NewCallback(func(child, _ uintptr) uintptr {
				cls := windowClass(child)
				*path = append(*path, fmt.Sprintf("%s(0x%X)", cls, child))
				if strings.HasPrefix(cls, "Chrome_RenderWidgetHostHWND") {
					hit = child
					return 0 // 命中即停
				}
				next = append(next, child)
				return 1
			})
			procEnumChildWindows.Call(h, cb, 0)
			if hit != 0 {
				return hit
			}
		}
		level = next
	}
	return 0
}

func findTargets(substr string, paths *[]string) []target {
	sub := strings.ToLower(substr)
	var hits []target
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		vis, _, _ := procIsWindowVisible.Call(hwnd)
		if vis == 0 {
			return 1 // 继续枚举
		}
		title := windowText(hwnd)
		if !strings.Contains(title, sub) {
			return 1
		}
		t := target{hwnd: hwnd, title: title, sendTo: hwnd}
		var path []string
		if f := findRenderChild(hwnd, &path); f != 0 {
			t.sendTo = f
		}
		*paths = append(*paths, fmt.Sprintf("[%s] %s", title, strings.Join(path, " → ")))
		hits = append(hits, t)
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return hits
}

// sendRuneMsg 逐个 UTF-16 码元 WM_CHAR 直投一个 rune(代理对拆两条消息)
func sendRuneMsg(hwnd uintptr, r rune) bool {
	var result uintptr
	for _, u := range utf16.Encode([]rune{r}) {
		ret, _, _ := procSendMessageTimeoutW.Call(
			hwnd, WM_CHAR, uintptr(u), 1,
			SMTO_ABORTIFHUNG, 1000, uintptr(unsafe.Pointer(&result)))
		if ret == 0 {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
	return true
}

func sendText(hwnd uintptr, text string) int {
	failed := 0
	for _, r := range text {
		if !sendRuneMsg(hwnd, r) {
			failed++
			fmt.Printf("  码元 U+%04X 投递失败\n", r)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return failed
}

func sendVK(vk uint16) bool {
	in := []INPUT{
		{_type: INPUT_KEYBOARD, ki: KEYBDINPUT{wVk: vk}},
		{_type: INPUT_KEYBOARD, ki: KEYBDINPUT{wVk: vk, dwFlags: KEYEVENTF_KEYUP}},
	}
	n, _, _ := procSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(INPUT{}))
	return n == uintptr(len(in))
}

// sendDirect 端到端预演产品的文本直投算法: 字符(含 Tab)走 WM_CHAR 文本层, 换行无法
// 走文本层(Chromium 过滤 \n/\r), 先发 Esc 关掉可能挂着的补全弹窗再发真回车 ——
// 与 internal/typing 的 sendEscaped 同序(Esc + 20ms + 本键)。
func sendDirect(hwnd uintptr, text string) int {
	failed := 0
	for _, r := range text {
		if r == '\n' {
			ok := sendVK(VK_ESCAPE)
			time.Sleep(20 * time.Millisecond)
			ok = sendVK(VK_RETURN) && ok
			if !ok {
				failed++
			}
		} else if !sendRuneMsg(hwnd, r) {
			failed++
		}
		time.Sleep(12 * time.Millisecond)
	}
	return failed
}

// sendKeys 逐字符 SendInput KEYEVENTF_UNICODE(Type 逐字符路径同款),
// 注入到当前前台窗口; 返回被系统拒绝的字符数
func sendKeys(text string) int {
	units := utf16.Encode([]rune(text))
	rejected := 0
	for _, u := range units {
		down := INPUT{_type: INPUT_KEYBOARD, ki: KEYBDINPUT{wScan: u, dwFlags: KEYEVENTF_UNICODE}}
		up := INPUT{_type: INPUT_KEYBOARD, ki: KEYBDINPUT{wScan: u, dwFlags: KEYEVENTF_UNICODE | KEYEVENTF_KEYUP}}
		in := []INPUT{down, up}
		n, _, _ := procSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(INPUT{}))
		if n != uintptr(len(in)) {
			rejected++
		}
		time.Sleep(20 * time.Millisecond)
	}
	return rejected
}

// activate 把窗口带到前台: AttachThreadInput 归并到当前前台线程的输入队列
// 以绕过前台锁(SetForegroundWindow 的调用限制), 返回是否成功
func activate(hwnd uintptr) bool {
	const SW_RESTORE = 9
	procShowWindow.Call(hwnd, SW_RESTORE)
	fg, _, _ := procGetForegroundWindow.Call()
	if fg == hwnd {
		return true
	}
	fgThread, _, _ := procGetWindowThreadPID.Call(fg, 0)
	curThread, _, _ := procGetCurrentThreadId.Call()
	attached := false
	if fgThread != 0 && fgThread != curThread {
		if ret, _, _ := procAttachThreadInput.Call(curThread, fgThread, 1); ret != 0 {
			attached = true
		}
	}
	procBringWindowToTop.Call(hwnd)
	procSetForegroundWindow.Call(hwnd)
	if attached {
		procAttachThreadInput.Call(curThread, fgThread, 0)
	}
	time.Sleep(400 * time.Millisecond)
	fg2, _, _ := procGetForegroundWindow.Call()
	return fg2 == hwnd
}

// focusedTarget 镜像产品的 focusedHWND(): 取前台线程的焦点子窗口。
//
// 不退回顶层窗口: 拿顶层窗口顶替会在"产品其实走了按键注入"的场景里给出一份不属于
// 这条通道的结论。产品自 v1.5.6 起的退化逻辑与顶层容器丢 WM_CHAR 的实测见
// docs/invariants.md「文本直投（v1.5.0，WM_CHAR 文本层注入）」。
func focusedTarget() (uintptr, string) {
	fg, _, _ := procGetForegroundWindow.Call()
	if fg == 0 {
		return 0, "无前台窗口"
	}
	tid, _, _ := procGetWindowThreadPID.Call(fg, 0)
	if tid != 0 {
		var gti GUITHREADINFO
		gti.cbSize = uint32(unsafe.Sizeof(gti))
		if ret, _, _ := procGetGUIThreadInfo.Call(tid, uintptr(unsafe.Pointer(&gti))); ret != 0 && gti.hwndFocus != 0 {
			return gti.hwndFocus, "前台线程焦点窗口"
		}
	}
	return 0, "无焦点子窗口(产品会退化为按键注入, WM_CHAR 这条通道没有落点可测)"
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: wmcharprobe <标题子串> [文本] [char|keys]")
		os.Exit(2)
	}
	mode := "char"
	if len(os.Args) >= 4 {
		mode = os.Args[3]
	}
	text := ""
	if len(os.Args) >= 3 {
		text = os.Args[2]
		if strings.HasPrefix(text, "@") {
			raw, err := os.ReadFile(text[1:])
			if err != nil {
				fmt.Fprintf(os.Stderr, "读取文本文件失败: %v\n", err)
				os.Exit(2)
			}
			text = string(raw)
		}
	}
	var paths []string
	targets := findTargets(os.Args[1], &paths)
	if len(targets) == 0 {
		fmt.Println("未找到标题匹配的窗口")
		os.Exit(1)
	}
	fmt.Println("子窗口链(诊断):")
	for _, p := range paths {
		fmt.Printf("  %s\n", p)
	}

	if len(os.Args) >= 3 {
		switch mode {
		case "char": // WM_CHAR 直投: 激活目标 → 镜像产品解析焦点窗口 → 投递
			const GA_ROOT = 2
			for _, t := range targets {
				if !activate(t.hwnd) {
					fmt.Println("  激活失败(窗口未能到前台), 仍按前台焦点解析")
				}
				dst, how := focusedTarget()
				if dst == 0 {
					fmt.Printf("  %s, 放弃 WM_CHAR 投递\n", how)
					continue
				}
				root, _, _ := procGetAncestor.Call(dst, GA_ROOT)
				if root != t.hwnd {
					fmt.Printf("  前台焦点(0x%X)不属于目标窗口, 放弃投递\n", dst)
					continue
				}
				fmt.Printf("投递 → [%s] (%s, class=%s 0x%X)\n", t.title, how, windowClass(dst), dst)
				if failed := sendText(dst, text); failed > 0 {
					fmt.Printf("  %d 个码元投递失败\n", failed)
				}
				time.Sleep(300 * time.Millisecond)
			}
		case "direct": // 字符(含 Tab)走 WM_CHAR, 换行前先 Esc 再发真回车
			const GA_ROOT = 2
			for _, t := range targets {
				if !activate(t.hwnd) {
					fmt.Println("  激活失败(窗口未能到前台), 仍按前台焦点解析")
				}
				dst, how := focusedTarget()
				if dst == 0 {
					fmt.Printf("  %s, 放弃 WM_CHAR 投递\n", how)
					continue
				}
				root, _, _ := procGetAncestor.Call(dst, GA_ROOT)
				if root != t.hwnd {
					fmt.Printf("  前台焦点(0x%X)不属于目标窗口, 放弃投递\n", dst)
					continue
				}
				fmt.Printf("投递 → [%s] (%s, class=%s 0x%X)\n", t.title, how, windowClass(dst), dst)
				if failed := sendDirect(dst, text); failed > 0 {
					fmt.Printf("  %d 个字符注入被拒\n", failed)
				}
				time.Sleep(300 * time.Millisecond)
			}
		case "close": // 关闭匹配窗口(测试收尾用)
			for _, t := range targets {
				fmt.Printf("关闭 → [%s]\n", t.title)
				procPostMessageW.Call(t.hwnd, 0x0010, 0, 0) // WM_CLOSE
			}
			time.Sleep(500 * time.Millisecond)
		case "keys": // SendInput 需要前台: 倒计时给操作者切窗口的机会
			fmt.Println("keys 模式: 2 秒后注入到前台窗口, 请保持目标窗口前台...")
			for i := 2; i > 0; i-- {
				fmt.Printf("  %d\n", i)
				time.Sleep(1 * time.Second)
			}
			fg, _, _ := procGetForegroundWindow.Call()
			if fg == 0 || !strings.Contains(windowText(fg), strings.ToLower(os.Args[1])) {
				fmt.Printf("前台窗口不是目标(0x%X), 放弃注入\n", fg)
				os.Exit(1)
			}
			if rejected := sendKeys(text); rejected > 0 {
				fmt.Printf("  %d 个字符被系统拒绝\n", rejected)
			}
			time.Sleep(300 * time.Millisecond)
		default:
			fmt.Fprintf(os.Stderr, "未知模式 %q (可用: char / direct / keys / close)\n", mode)
			os.Exit(2)
		}
	}

	for _, t := range targets {
		fmt.Printf("标题: %s\n", windowText(t.hwnd))
	}
}
