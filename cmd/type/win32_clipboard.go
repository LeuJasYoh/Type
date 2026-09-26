//go:build windows && (amd64 || arm64)

// ─── 剪贴板 (全格式快照/恢复) ─────────────────────────
// Clipboard 接口的 Win32 实现 + 内存块读写辅助函数

package main

import (
	"bytes"
	"time"
	"unicode/utf16"
	"unsafe"
)

// win32Clipboard Clipboard 的 Win32 实现
type win32Clipboard struct{}

const (
	CF_UNICODETEXT = 13
	CF_BITMAP      = 2 // 以下三者 GetClipboardData 返回 GDI 句柄而非 HGLOBAL
	CF_PALETTE     = 9
	CF_ENHMETAFILE = 14
	// CF_METAFILEPICT 块本身就是内存, 但块里装着另一个图形句柄(hmf),
	// EmptyClipboard 之后那个句柄已失效: 照抄会恢复出一个悬空句柄
	CF_METAFILEPICT = 3
	// 所有者绘制与"私有显示"格式: 数据由持有方解释, 形状不保证是内存块
	CF_OWNERDISPLAY    = 0x0080
	CF_DSPTEXT         = 0x0081
	CF_DSPBITMAP       = 0x0082
	CF_DSPMETAFILEPICT = 0x0083
	CF_DSPENHMETAFILE  = 0x008E
	// GDI 对象格式族: 数据是 GDI 句柄, 不是内存块
	CF_GDIOBJFIRST = 0x0300
	CF_GDIOBJLAST  = 0x03FF
	// 程序私有格式族: 通常是内存块, 刻意不跳过(见 skippableFormat)
	CF_PRIVATEFIRST = 0x0200
	CF_PRIVATELAST  = 0x02FF
	GMEM_MOVABLE    = 0x0002
	GMEM_ZEROINIT   = 0x0040
	GHND            = GMEM_MOVABLE | GMEM_ZEROINIT
)

// 打开剪贴板的重试档位。Windows 同一时刻只允许一个程序持有剪贴板, 被其他
// 程序占着是常态而非异常, 因此一律重试。恢复用更耐心的档位: 恢复失败意味着
// 用户原本的剪贴板内容就此丢失, 而占用通常只持续一瞬间, 多等几百毫秒几乎
// 总能成功(这段时间在任务收尾, 用户无感)
const (
	clipboardAttempts     = 4
	clipboardWait         = 50 * time.Millisecond
	clipboardRestoreTries = 8
	clipboardRestoreWait  = 100 * time.Millisecond
)

// openClipboardWithRetry 打开剪贴板(读写档位), 被其他程序占用时重试
func openClipboardWithRetry() bool { return openClipboardRetry(clipboardAttempts, clipboardWait) }

// openClipboardRestoreRetry 恢复档位: 比读写更耐心, 见上面的说明
func openClipboardRestoreRetry() bool {
	return openClipboardRetry(clipboardRestoreTries, clipboardRestoreWait)
}

func openClipboardRetry(attempts int, wait time.Duration) bool {
	for attempt := 0; attempt < attempts; attempt++ {
		ret, _, _ := procOpenClipboard.Call(0)
		if ret != 0 {
			return true
		}
		time.Sleep(wait)
	}
	return false
}

// encodedText CF_UNICODETEXT 的字节表示: UTF-16LE + 结尾 NUL
func encodedText(text string) []byte {
	units := utf16.Encode([]rune(text + "\x00"))
	out := make([]byte, len(units)*2)
	for i, u := range units {
		out[i*2] = byte(u)
		out[i*2+1] = byte(u >> 8)
	}
	return out
}

func (win32Clipboard) SetText(text string) bool {
	if !openClipboardWithRetry() {
		return false
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()

	encoded := encodedText(text)
	size := len(encoded)
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

func (win32Clipboard) GetText() string {
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

// HoldsText 判断剪贴板当前文本是否就是 text。
// 按原始字节比对而非 GetText 的字符串: CF_UNICODETEXT 内嵌 NUL 时 GetText 会截断,
// 字符串比较将误判为"用户已改动"而跳过恢复
func (win32Clipboard) HoldsText(text string) bool {
	if !openClipboardWithRetry() {
		return false
	}
	defer procCloseClipboard.Call()
	hMem, _, _ := procGetClipboardData.Call(CF_UNICODETEXT)
	if hMem == 0 {
		return false
	}
	size, _, _ := procGlobalSize.Call(hMem)
	if size == 0 {
		return false
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		return false
	}
	buf := make([]byte, int(size))
	// 拷贝长度不超过缓冲区容量, 防止 GlobalSize 返回奇数时越界
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(unsafe.SliceData(buf))), ptr, uintptr(len(buf)))
	procGlobalUnlock.Call(hMem)
	return bytes.Equal(buf, encodedText(text))
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

// skippableFormat 该格式是否不适合按"一整块内存"快照。
// 判据是"这块数据是不是普通内存块": 句柄型格式照抄下来, 恢复时写回的是失效
// 句柄或垃圾字节, 受害的是系统里其它程序。三类被排除 ——
// ① GetClipboardData 直接返回 GDI 句柄的格式;
// ② CF_METAFILEPICT: 内存块里装着图形句柄, 恢复时那个句柄已随 EmptyClipboard 失效;
// ③ 所有者绘制 / 私有显示 / GDI 对象格式族: 数据由持有方解释, 形状不保证是内存块。
// CF_PRIVATEFIRST..LAST(0x0200-0x02FF, 程序私有格式)不在此列: 它们通常是内存块,
// 跳过反而会丢内容, 照抄更划算
func skippableFormat(fmt uint32) bool {
	if _, skip := handleFormats[fmt]; skip {
		return true
	}
	switch fmt {
	case CF_METAFILEPICT, CF_OWNERDISPLAY,
		CF_DSPTEXT, CF_DSPBITMAP, CF_DSPMETAFILEPICT, CF_DSPENHMETAFILE:
		return true
	}
	return fmt >= CF_GDIOBJFIRST && fmt <= CF_GDIOBJLAST
}

// Snapshot 复制当前剪贴板的全部内存块型格式(文本/图片 CF_DIB/文件
// CF_HDROP/HTML Format 等)。返回 nil 表示剪贴板打开失败(原状态未知, 调用方应
// 放弃恢复); 返回空切片表示剪贴板原本为空, 恢复时执行清空
func (win32Clipboard) Snapshot() []ClipboardFormat {
	if !openClipboardWithRetry() {
		return nil
	}
	defer procCloseClipboard.Call()

	snap := make([]ClipboardFormat, 0, 8)
	for fmt := uint32(0); ; {
		next, _, _ := procEnumClipboardFormats.Call(uintptr(fmt))
		if next == 0 {
			break
		}
		fmt = uint32(next)
		if skippableFormat(fmt) {
			continue
		}
		if data, ok := readClipboardFormat(fmt); ok {
			snap = append(snap, ClipboardFormat{Fmt: fmt, Data: data})
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

// writeClipboardFormats 将快照按原格式顺序写回(剪贴板已打开且已清空时调用)。
// 返回是否全部写回成功: 任一格式分配/写入失败都算没恢复干净, 调用方据此告诉
// 用户"剪贴板没恢复", 而不是让他以为原内容还在
func writeClipboardFormats(snap []ClipboardFormat) bool {
	all := true
	for _, cf := range snap {
		hMem, _, _ := procGlobalAlloc.Call(GHND, uintptr(len(cf.Data)))
		if hMem == 0 {
			all = false
			continue
		}
		ptr, _, _ := procGlobalLock.Call(hMem)
		if ptr == 0 {
			procGlobalFree.Call(hMem)
			all = false
			continue
		}
		procRtlMoveMemory.Call(ptr, uintptr(unsafe.Pointer(unsafe.SliceData(cf.Data))), uintptr(len(cf.Data)))
		procGlobalUnlock.Call(hMem)
		if ret, _, _ := procSetClipboardData.Call(uintptr(cf.Fmt), hMem); ret == 0 {
			procGlobalFree.Call(hMem) // 系统未接管所有权时由调用方释放
			all = false
		}
	}
	return all
}

// RestoreSnapshotRaw 无条件写回快照(调用方需确认剪贴板未被用户改动),
// 返回剪贴板是否真的回到了快照状态。恢复用比读写更耐心的重试档位:
// 失败意味着用户原本的内容丢失, 值得多等一会儿
func (win32Clipboard) RestoreSnapshotRaw(snap []ClipboardFormat) bool {
	if snap == nil {
		return false // 快照失败, 原状态未知, 不动剪贴板(也没恢复成任何东西)
	}
	if len(snap) == 0 {
		return clipboardClear()
	}
	if !openClipboardRestoreRetry() {
		return false
	}
	defer procCloseClipboard.Call()
	if ret, _, _ := procEmptyClipboard.Call(); ret == 0 {
		return false
	}
	return writeClipboardFormats(snap)
}
