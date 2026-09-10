//go:build windows && (amd64 || arm64)

// ─── 剪贴板 (全格式快照/恢复) ─────────────────────────

package main

import (
	"time"
	"unicode/utf16"
	"unsafe"
)

const (
	CF_UNICODETEXT = 13
	CF_BITMAP      = 2 // 以下三者 GetClipboardData 返回 GDI 句柄而非 HGLOBAL
	CF_PALETTE     = 9
	CF_ENHMETAFILE = 14
	GMEM_MOVABLE   = 0x0002
	GMEM_ZEROINIT  = 0x0040
	GHND           = GMEM_MOVABLE | GMEM_ZEROINIT
)

// openClipboardWithRetry 打开剪贴板, 被其他程序占用时重试
func openClipboardWithRetry() bool {
	for attempt := 0; attempt < 4; attempt++ {
		ret, _, _ := procOpenClipboard.Call(0)
		if ret != 0 {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func clipboardSetText(text string) bool {
	if !openClipboardWithRetry() {
		return false
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()

	encoded := utf16.Encode([]rune(text + "\x00"))
	size := len(encoded) * 2
	hMem, _, _ := procGlobalAlloc.Call(GHND, uintptr(size))
	if hMem == 0 {
		return false
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem) // 加锁失败必须释放, 否则泄漏
		return false
	}
	// RtlMoveMemory 批量拷贝: 源为 Go 切片指针 (Pointer->uintptr 单向转换, vet 认可),
	// 目标为 Windows 返回的 uintptr 直接传入, 避免 uintptr->unsafe.Pointer 转换
	procRtlMoveMemory.Call(ptr, uintptr(unsafe.Pointer(unsafe.SliceData(encoded))), uintptr(size))
	procGlobalUnlock.Call(hMem)
	ret, _, _ := procSetClipboardData.Call(CF_UNICODETEXT, hMem)
	if ret == 0 {
		procGlobalFree.Call(hMem) // 系统未接管所有权时由调用方释放
	}
	return ret != 0
}

func clipboardGetText() string {
	if !openClipboardWithRetry() {
		return ""
	}
	defer procCloseClipboard.Call()
	hMem, _, _ := procGetClipboardData.Call(CF_UNICODETEXT)
	if hMem == 0 {
		return ""
	}
	size, _, _ := procGlobalSize.Call(hMem)
	if size == 0 {
		return ""
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		return ""
	}
	buf := make([]uint16, int(size)/2)
	// 拷贝长度不超过缓冲区容量, 防止 GlobalSize 返回奇数时越界
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(unsafe.SliceData(buf))), ptr, uintptr(len(buf)*2))
	procGlobalUnlock.Call(hMem)

	// 以 \x00 截断(块大小可能含填充)
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return string(utf16.Decode(buf[:n]))
}

// clipboardClear 清空剪贴板内容
func clipboardClear() bool {
	if !openClipboardWithRetry() {
		return false
	}
	defer procCloseClipboard.Call()
	ret, _, _ := procEmptyClipboard.Call()
	return ret != 0
}

// handleFormats: GetClipboardData 返回 GDI 句柄而非 HGLOBAL 内存块的标准格式,
// 无法按字节复制, 快照时跳过 (延迟渲染格式 GetClipboardData 返回 0, 同样跳过)
var handleFormats = map[uint32]struct{}{
	CF_BITMAP:      {},
	CF_PALETTE:     {},
	CF_ENHMETAFILE: {},
}

// clipFormat 剪贴板单一格式的原始字节快照
type clipFormat struct {
	fmt  uint32
	data []byte
}

// clipboardSnapshot 复制当前剪贴板的全部内存块型格式(文本/图片 CF_DIB/文件
// CF_HDROP/HTML Format 等)。返回 nil 表示剪贴板打开失败(原状态未知, 调用方应
// 放弃恢复); 返回空切片表示剪贴板原本为空, 恢复时执行清空
func clipboardSnapshot() []clipFormat {
	if !openClipboardWithRetry() {
		return nil
	}
	defer procCloseClipboard.Call()

	snap := make([]clipFormat, 0, 8)
	for fmt := uint32(0); ; {
		next, _, _ := procEnumClipboardFormats.Call(uintptr(fmt))
		if next == 0 {
			break
		}
		fmt = uint32(next)
		if _, skip := handleFormats[fmt]; skip {
			continue
		}
		if data, ok := readClipboardFormat(fmt); ok {
			snap = append(snap, clipFormat{fmt: fmt, data: data})
		}
	}
	return snap
}

// readClipboardFormat 读取单一格式的数据块(剪贴板已打开时调用)
func readClipboardFormat(fmt uint32) ([]byte, bool) {
	hMem, _, _ := procGetClipboardData.Call(uintptr(fmt))
	if hMem == 0 {
		return nil, false // 延迟渲染或读取失败
	}
	size, _, _ := procGlobalSize.Call(hMem)
	if size == 0 {
		return nil, false
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		return nil, false
	}
	defer procGlobalUnlock.Call(hMem)
	data := make([]byte, int(size))
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(unsafe.SliceData(data))), ptr, uintptr(size))
	return data, true
}

// writeClipboardFormats 将快照按原格式顺序写回(剪贴板已打开且已清空时调用)
func writeClipboardFormats(snap []clipFormat) {
	for _, cf := range snap {
		hMem, _, _ := procGlobalAlloc.Call(GHND, uintptr(len(cf.data)))
		if hMem == 0 {
			continue
		}
		ptr, _, _ := procGlobalLock.Call(hMem)
		if ptr == 0 {
			procGlobalFree.Call(hMem)
			continue
		}
		procRtlMoveMemory.Call(ptr, uintptr(unsafe.Pointer(unsafe.SliceData(cf.data))), uintptr(len(cf.data)))
		procGlobalUnlock.Call(hMem)
		if ret, _, _ := procSetClipboardData.Call(uintptr(cf.fmt), hMem); ret == 0 {
			procGlobalFree.Call(hMem) // 系统未接管所有权时由调用方释放
		}
	}
}

// restoreSnapshotRaw 无条件写回快照(调用方需确认剪贴板未被用户改动)
func restoreSnapshotRaw(snap []clipFormat) {
	if snap == nil {
		return // 快照失败, 原状态未知, 不动剪贴板
	}
	if len(snap) == 0 {
		clipboardClear()
		return
	}
	if !openClipboardWithRetry() {
		return
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	writeClipboardFormats(snap)
}
