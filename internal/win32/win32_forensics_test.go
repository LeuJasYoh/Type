//go:build windows && (amd64 || arm64)

// ─── 窗口取证手段 (只被用例使用, 不进产品二进制) ─────
// "窗口到底能不能拖大"不能只读源码判定: 样式位要读回, 还要真的问系统一次命中测试

package win32

import "unsafe"

// WS_THICKFRAME 是可拖拽的边框, WS_MAXIMIZEBOX 是最大化按钮(同样让尺寸可变)。
// 库的 SetSize(HintFixed) 会清掉这两个位, 但"库会清"与"产物里真的不可拖大"是
// 两件事(样式位可能被别处改回、边框可能没随样式刷新): 判据见
// docs/invariants.md「其它不变量」
const (
	wsThickFrame  = 0x00040000
	wsMaximizeBox = 0x00010000
)

// htBottomRight 是 WM_NCHITTEST 的 HTBOTTOMRIGHT: 只有它说明右下角是可拖拽的
// 缩放边框(HTCLIENT=1 是客户区, HTBORDER=18 是不可缩放的细边框, 两者都不可拖大)
const htBottomRight = 17

// WM_NCHITTEST: 问系统"这个坐标点命中了窗口的哪个部位", 用来验证右下角
// 到底是不是缩放边框(见 hitTestBottomRight)
const wmNCHitTest = 0x0084

// windowStyle / hitTestBottomRight 是"窗口到底能不能拖大"的取证手段:
// 判据不能只看源码, 样式位要读回, 还要真的问系统一次命中测试; 且用例必须 A/B 两段
// (先证明取证手段认得出可拖大, 再清位证明认不出, 缺阳性对照则断言恒真) ——
// 见 docs/invariants.md「其它不变量」
func windowStyle(hwnd uintptr) uint32 {
	style, _, _ := procGetWindowLongPtrW.Call(hwnd, ^uintptr(15)) // GWL_STYLE = -16
	return uint32(style)
}

// hitTestBottomRight 在**窗口矩形**(含边框)右下角内侧 2px 处做一次 WM_NCHITTEST,
// 返回命中码: htBottomRight(17) 表示缩放边框, 其余(HTCLIENT=1 / HTBORDER=18)不可
// 拖大, 失败返回 -1。采样点必须按窗口矩形算: 按客户区取点落在客户区里面, 任何带
// 边框的窗口都只回 HTCLIENT, 断言于是对**可拖大**的窗口同样成立(2026-10 探针实测:
// 客户区取点 1, 窗口矩形取点 17)
func hitTestBottomRight(hwnd uintptr) int {
	var r rect
	if ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ret == 0 {
		return -1
	}
	// WM_NCHITTEST 的 lParam 是屏幕坐标打包成的 POINTS: 低 16 位 x, 高 16 位 y。
	// **必须带超时**: 窗口若不是由当前线程建的, 同步 SendMessage 会阻塞等对方线程泵
	// 消息; Go 协程会在线程间迁移, 那个线程可能根本不在泵, 于是整条用例永久卡住
	// (实测: TestWindowSizeIsFixed 挂到 go test 的超时守护才被发现)。目标无响应时
	// 放弃并回报 -1, 让断言失败而不是让进程卡死 —— 与产品注入同一条判据
	// SendMessageTimeout 的返回值只说有没有送成(TRUE/FALSE), 命中码在 lpdwResult 里 ——
	// 拿返回值当命中码会恒读到 1(TRUE), 正好又是 HTCLIENT, 于是"可拖大"的阳性对照
	// 被判成失效。产品侧 sendCharUnitsViaWMChar 只用 ret != 0 判成败, 那是正确的;
	// 这里要的是返回值本身, 必须给 lpdwResult 一个真的地址
	lp := int32(r.Bottom-2)<<16 | (r.Right - 2)
	var hit uintptr
	ret, _, _ := procSendMessageTimeoutW.Call(hwnd, wmNCHitTest, 0, uintptr(uint32(lp)),
		smtoAbortIfHung, 1000, uintptr(unsafe.Pointer(&hit)))
	if ret == 0 {
		return -1
	}
	return int(int32(uint32(hit)))
}
