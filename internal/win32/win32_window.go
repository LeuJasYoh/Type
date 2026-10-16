//go:build windows && (amd64 || arm64)

// ─── 窗口置顶 ─────────────────────────────────────────
// 尺寸与 DPI / 内容缩放在 win32_size.go, 图标在 win32_icon.go,
// 前台窗口探测在 win32_foreground.go, 只被用例使用的取证手段在
// win32_forensics_test.go

package win32

const (
	hwndTopmost     = ^uintptr(0) // -1
	hwndNotTopmost  = ^uintptr(1) // -2
	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020
	swpShowWindow   = 0x0040
)

func SetTopmost(hwnd uintptr, topmost bool) bool {
	insertAfter := hwndNotTopmost
	if topmost {
		insertAfter = hwndTopmost
	}
	ret, _, _ := procSetWindowPos.Call(
		hwnd, insertAfter, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpShowWindow|swpNoActivate,
	)
	return ret != 0
}
