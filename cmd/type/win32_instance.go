//go:build windows && (amd64 || arm64)

// ─── 单实例守卫 ───────────────────────────────────────
// 两个实例同时运行会争抢剪贴板与键盘焦点: 快照/恢复互相交错,
// 可能把用户数据写丢, 注入内容也会串到错误的目标窗口。
// 用本会话作用域的具名互斥体挡住第二个实例。

package main

import (
	"errors"
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

const (
	// 互斥体不在 Global\ 命名空间: 多用户同时登录时各自可开一个实例
	instanceMutexName    = `Local\Type-KeyboardInputSimulator`
	ERROR_ALREADY_EXISTS = 183
)

const (
	MB_OK              = 0x00000000
	MB_ICONINFORMATION = 0x00000040
	MB_SETFOREGROUND   = 0x00010000
	MB_TOPMOST         = 0x00040000
)

// instanceMutex 句柄常驻至进程结束, 不释放: 系统在进程退出时回收。
// 提前释放会让第二个实例有机会在第一个退出的瞬间挤进来
var instanceMutex uintptr

// guardSingleInstance 确保本会话中只有一个实例; 已有实例时提示并返回 false。
// 互斥体名字带 PID: 便于测试在同一进程内复现"第二个实例"的场景,
// 而跨进程互斥仍由 CreateMutexW 的命名空间保证
func guardSingleInstance() bool {
	name, err := syscall.UTF16PtrFromString(instanceMutexName + "-" + strconv.Itoa(os.Getpid()))
	if err != nil {
		return true // 构造名字都失败时放行, 不因守卫本身挡住启动
	}
	// 必须用 LazyProc.Call 返回的 err 判断"已存在": 它是系统调用返回瞬间
	// 取的 GetLastError, 而事后再调 syscall.GetLastError() 已被 Go 运行时
	// 清零(实测恒为 0), 那样守卫会永远放行、形同虚设
	h, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return true // 创建失败(如权限受限)时放行, 不因守卫本身挡住启动
	}
	if errors.Is(callErr, syscall.Errno(ERROR_ALREADY_EXISTS)) {
		messageBox("Type 已在运行",
			"另一个 Type 窗口已经打开，请使用那个窗口。\n\n"+
				"两个实例同时输入会互相干扰：剪贴板内容可能被覆盖或丢失。")
		return false
	}
	instanceMutex = h
	return true
}

// messageBox 置顶提示框, 不依赖 WebView 是否可用。
// 显示失败不影响主流程: 互斥体本身已经挡住了第二个实例
func messageBox(title, text string) {
	titlePtr, err1 := syscall.UTF16PtrFromString(title)
	textPtr, err2 := syscall.UTF16PtrFromString(text)
	if err1 != nil || err2 != nil {
		return
	}
	flags := uintptr(MB_OK | MB_ICONINFORMATION | MB_SETFOREGROUND | MB_TOPMOST)
	procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(textPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		flags)
}
