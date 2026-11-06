//go:build windows && (amd64 || arm64)

// 装配层: MyGo 接管窗口与页面, 平台能力注入业务层。
// 仅 64 位: INPUT 结构体的手工填充(40 字节)仅匹配 64 位 ABI, 386 下 SendInput
// 会静默注入乱码, 因此直接禁止 32 位编译。
package main

import (
	"fmt"
	"sync"

	"github.com/LeuJasYoh/type/internal/typing"
	"github.com/LeuJasYoh/type/internal/web"
	"github.com/LeuJasYoh/type/internal/win32"

	"github.com/egoist/mygo"
)

var version = "2.0.2"

// 窗口的设计尺寸(DIP)。Width/Height 是客户区尺寸, Min/Max 给 FitContent 兜界。
// minWindowW 同时是前端 --ui-scale 的基准宽度, 由契约测试双端核对。
const (
	minWindowW = 540
	minWindowH = 480
	maxWindowW = 648
	maxWindowH = 540
)

// preloadShim 把 MyGo 的 Service.Method 调用映射回冻结的 window.* 全局函数。
// 注入发生在 document start、window.mygo 就绪之后, 前端产物因此一字节不改。
const preloadShim = `
window.startTyping = function(){ return window.mygo.call.apply(window.mygo, ["Type.StartTyping"].concat(Array.prototype.slice.call(arguments))); };
window.cancelTyping = function(){ return window.mygo.call("Type.CancelTyping"); };
window.toggleTopmost = function(){ return window.mygo.call("Type.ToggleTopmost"); };
window.getTopmost = function(){ return window.mygo.call("Type.GetTopmost"); };
window.getTypingStatus = function(){ return window.mygo.call("Type.GetTypingStatus"); };
`

// typeService 是前端可调用的绑定服务。Bind 必须早于 App.Run, 而窗口到
// WhenReady 才存在, 所以依赖在 attach 里补齐, 方法先查就绪再转发。
type typeService struct {
	mu  sync.Mutex
	svc *typing.TypingService
	win *mygo.Window
}

func (s *typeService) attach(win *mygo.Window) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.win = win
	// 前台探测带自身窗口句柄, 用于"焦点仍在 Type 自身"的判定。
	s.svc = typing.NewTypingService(win32.Injector{}, win32.Clipboard{},
		win32.Foreground{HWND: win.NativeHandle()})
}

func (s *typeService) service() (*typing.TypingService, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.svc == nil {
		return nil, fmt.Errorf("界面尚未就绪")
	}
	return s.svc, nil
}

func (s *typeService) window() (*mygo.Window, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.win == nil {
		return nil, fmt.Errorf("界面尚未就绪")
	}
	return s.win, nil
}

// 以下五个方法对应冻结的跨端绑定名, 参数顺序与返回类型由 contract_test.go 钉住。

func (s *typeService) StartTyping(text string, delay int, forceRaw bool, textDirect bool) (string, error) {
	svc, err := s.service()
	if err != nil {
		return "", err
	}
	return svc.Start(text, delay, forceRaw, textDirect)
}

func (s *typeService) CancelTyping() (string, error) {
	svc, err := s.service()
	if err != nil {
		return "", err
	}
	return svc.Cancel()
}

func (s *typeService) GetTypingStatus() (*typing.TypingStatus, error) {
	svc, err := s.service()
	if err != nil {
		return nil, err
	}
	return svc.Status(), nil
}

func (s *typeService) ToggleTopmost() (bool, error) {
	win, err := s.window()
	if err != nil {
		return false, err
	}
	win.SetAlwaysOnTop(!win.IsAlwaysOnTop())
	return win.IsAlwaysOnTop(), nil
}

func (s *typeService) GetTopmost() (bool, error) {
	win, err := s.window()
	if err != nil {
		return false, err
	}
	return win.IsAlwaysOnTop(), nil
}

func main() {
	// 单实例: 第二个实例把参数交给这里之后自行退出, 避免两个实例争抢
	// 剪贴板与键盘焦点。回调只闪任务栏, 不抢前台窗口。
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}

	// GUI 子系统没有控制台, WebView2 起不来时先给用户一句能看懂的提示。
	if !webViewAvailable() {
		win32.PromptWebView2Unusable(win32.MsgWebView2Missing)
	}

	svc := &typeService{}
	bindService(svc)

	mygo.App.OnSecondInstance(func(args []string, workingDir string) {
		// Type 靠前台窗口锁定注入目标, 此刻把已有窗口顶到前台会触发
		// 漂移中止或"焦点仍在 Type"; 只闪一下任务栏即可。
		if win, err := svc.window(); err == nil {
			win.FlashFrame(true)
		}
	})

	mygo.App.WhenReady(func() {
		// 控制器异步创建失败时框架可能 panic; 接住后走原生提示框。
		defer func() {
			if recover() != nil {
				win32.PromptWebView2Unusable(win32.MsgWebView2InitFailed)
			}
		}()

		// 启动时把页面缩放复位到 100%: 用户数据目录可能残留 per-origin zoom,
		// 让首帧就不是设计尺寸。Ctrl+滚轮仍可用, 低视口兜底保证放大后不裁切。
		// 详见 docs/invariants.md「内容缩放与界面几何」。
		pageOpts := mygo.PageOptions{
			DevTools:             devTools(),
			BackgroundThrottling: mygo.ThrottlingOff,
			PreloadScript:        preloadShim,
			ZoomFactor:           1,
		}
		if u := devURL(); u != "" {
			pageOpts.TrustedOrigins = []string{u}
		}

		win := mygo.NewWindow(mygo.WindowOptions{
			Title:          "Type " + version,
			Width:          minWindowW,
			Height:         minWindowH,
			UseContentSize: true,
			MinWidth:       minWindowW,
			MinHeight:      minWindowH,
			MaxWidth:       maxWindowW,
			MaxHeight:      maxWindowH,
			FitContent:     true,
			DisableResize:  true,
			Page:           pageOpts,
			// 页面绘制前窗口显示的颜色: 不给的话是白色, 暗色主题下会先闪一段白。
			// 两值取自 style.css 的 --bg (oklch 96.5%/16.5%), 框架按系统深浅色选一个,
			// 与页面里 prefers-color-scheme 的判定同源
			BackgroundColor: "light-dark(#edf4fd, #0c0f12)",
		})
		svc.attach(win)
		win.OnClosed(func() { mygo.App.Quit() })

		page := win.Page()
		if u := devURL(); u != "" {
			_ = page.LoadURL(u)
		} else {
			page.LoadHTML(web.IndexHTML, "")
		}
	})

	if err := mygo.App.Run(); err != nil {
		win32.PromptWebView2Unusable(win32.MsgWebView2InitFailed)
	}
}

// webViewAvailable 包住框架预检: 后端初始化异常时不让 GUI 进程静默退出。
func webViewAvailable() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return mygo.WebViewAvailable()
}

// bindService 复用原来的"绑定失败也要有提示"语义。Bind 出错是 panic,
// 没有 error 可判, 只能 recover。
func bindService(s *typeService) {
	defer func() {
		if recover() != nil {
			win32.PromptWebView2Unusable(win32.MsgBindFailed)
		}
	}()
	mygo.BindAs("Type", s)
}