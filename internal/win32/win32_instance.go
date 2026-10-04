//go:build windows && (amd64 || arm64)

// ─── 单实例守卫 ───────────────────────────────────────
// 两个实例同时运行会争抢剪贴板与键盘焦点: 快照/恢复互相交错,
// 可能把用户数据写丢, 注入内容也会串到错误的目标窗口。
// 用本会话作用域的具名互斥体挡住第二个实例。

package win32

import (
	"errors"
	"syscall"
	"unsafe"
)

const (
	// 名字必须固定且不带 PID: 跨进程互斥依赖两个进程认领同一个名字, 带上 PID 就
	// 永远撞不上, 守卫形同虚设(初版栽过)。不在 Global\ 命名空间: 多用户同时登录
	// 时各自可以开一个实例
	instanceMutexName  = `Local\Type-KeyboardInputSimulator`
	errorAlreadyExists = 183
	// 名字已被占用但无权打开时返回它, 而不是 ERROR_ALREADY_EXISTS(判据见 mutexAlreadyHeld)
	errorAccessDenied = 5
)

// instanceMutex 句柄常驻至进程结束, 不释放: 系统在进程退出时回收。
// 提前释放会让第二个实例有机会在第一个退出的瞬间挤进来
var instanceMutex uintptr

// GuardSingleInstance 确保本会话中只有一个实例; 已有实例时提示并返回 false。
// 返回值语义: true = 本实例可以继续启动(main 据此决定是否退出)
func GuardSingleInstance() bool {
	if !claimInstanceMutex(instanceMutexName) {
		messageBox("Type 已在运行",
			"另一个 Type 窗口已经打开，请使用那个窗口。\n\n"+
				"两个实例同时输入会互相干扰：剪贴板内容可能被覆盖或丢失。",
			mbOK|mbIconInformation|mbSetForeground|mbTopmost)
		return false
	}
	return true
}

// mutexAlreadyHeld 判断 CreateMutexW 的结果意味着"已经有实例占着这个名字":
// ERROR_ALREADY_EXISTS(有权打开, 返回句柄)与 ERROR_ACCESS_DENIED(无权打开,
// 返回 NULL)都算, 后者与"根本没建成"共用返回值 0、语义却相反, 漏掉就形同虚设。
// 文档依据与本机造不出后一支的原因见 docs/invariants.md「其它不变量」
func mutexAlreadyHeld(h uintptr, callErr error) bool {
	if errors.Is(callErr, syscall.Errno(errorAlreadyExists)) {
		return true
	}
	return h == 0 && errors.Is(callErr, syscall.Errno(errorAccessDenied))
}

// claimInstanceMutex 以 name 认领单实例互斥体, 返回"可否继续启动"。
// 名字由调用方传入: 生产用固定名, 测试传带 PID 的名字 —— 既复现"两个进程认领
// 同一个名字", 又不干扰真正在运行的 Type。判定与提示框分开则是为了可测:
// messageBox 是模态框, 无头 CI 上没人点确定会一直阻塞到 go test 超时(已实际发生过)
func claimInstanceMutex(name string) (proceed bool) {
	ptr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return true // 构造名字都失败时放行, 不因守卫本身挡住启动
	}
	// 必须用 LazyProc.Call 返回的 err 判断"已存在": 它是系统调用返回瞬间取的
	// GetLastError; 事后再调 syscall.GetLastError() 已被运行时清零(实测恒为 0),
	// 那样守卫会永远放行、形同虚设
	h, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(ptr)))
	if mutexAlreadyHeld(h, callErr) {
		if h != 0 {
			// 已存在时 CreateMutexW 仍返回现有互斥体的有效句柄: 不认领就用完即关
			procCloseHandle.Call(h)
		}
		return false
	}
	if h == 0 {
		return true // 其余创建失败(如命名空间受限)时放行, 不因守卫本身挡住启动
	}
	instanceMutex = h
	return true
}
