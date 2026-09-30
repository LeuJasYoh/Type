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
	// 互斥体不在 Global\ 命名空间: 多用户同时登录时各自可开一个实例。
	// 名字必须固定且不带 PID: 跨进程互斥恰恰依赖两个进程认领同一个名字,
	// 一旦带上 PID, 每个进程的名字都互不相同, 第二个实例永远撞不上,
	// 守卫形同虚设(初版就栽在这里)
	instanceMutexName    = `Local\Type-KeyboardInputSimulator`
	ERROR_ALREADY_EXISTS = 183
	// ERROR_ACCESS_DENIED 见 claimInstanceMutex 的说明: 名字已被占用但当前
	// 进程无权打开时, CreateMutexW 返回的是它而不是 ERROR_ALREADY_EXISTS
	ERROR_ACCESS_DENIED = 5
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
			MB_OK|MB_ICONINFORMATION|MB_SETFOREGROUND|MB_TOPMOST)
		return false
	}
	return true
}

// mutexAlreadyHeld 判断 CreateMutexW 的结果意味着"已经有实例占着这个名字"。
// 两个错误码都是这个意思, 成因不同, 抽出来是为了能直接测:
//
//   - ERROR_ALREADY_EXISTS: 名字已被占用, 且本进程有权打开 -> 返回句柄 + 此码;
//   - ERROR_ACCESS_DENIED: 名字已被占用, 但本进程拿不到访问权。
//     CreateMutexW 的文档并没有逐字写这一支, 它只说明"名字命中已存在的对象时
//     请求 MUTEX_ALL_ACCESS"以及"失败返回 NULL"; ACCESS_DENIED 是从这两句推得
//     的(访问检查只在对象存在时才会做)。它与"根本没建成"共用返回值 0, 语义却
//     相反 —— 只按键值判断就会放行。README 建议需要向提权窗口注入的用户以管理
//     员身份运行, 于是"先管理员开的实例、后普通权限的实例"是很自然的用法,
//     漏掉这一支就等于让两个实例同时抢剪贴板与键盘焦点
//
// 这一支本机造不出来(要两个不同完整性级别的进程), 所以用单测钉的是分类本身
func mutexAlreadyHeld(h uintptr, callErr error) bool {
	if errors.Is(callErr, syscall.Errno(ERROR_ALREADY_EXISTS)) {
		return true
	}
	return h == 0 && errors.Is(callErr, syscall.Errno(ERROR_ACCESS_DENIED))
}

// claimInstanceMutex 尝试以 name 认领单实例互斥体, 返回"可否继续启动"。
// 名字由调用方传入: 生产用固定名, 测试传入带 PID 的名字, 既复现"两个进程
// 认领同一个名字"的场景, 又不会与真正在运行的 Type 相互干扰。
// 与提示框分开则是为了让测试能验证判定本身: messageBox 是模态对话框,
// 在无头 CI 上没有人点确定, 一旦被测试触发就会一直阻塞到 go test 超时
func claimInstanceMutex(name string) (proceed bool) {
	ptr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return true // 构造名字都失败时放行, 不因守卫本身挡住启动
	}
	// 必须用 LazyProc.Call 返回的 err 判断"已存在": 它是系统调用返回瞬间
	// 取的 GetLastError, 而事后再调 syscall.GetLastError() 已被 Go 运行时
	// 清零(实测恒为 0), 那样守卫会永远放行、形同虚设
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
