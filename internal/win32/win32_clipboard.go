//go:build windows && (amd64 || arm64)

// ─── 剪贴板 (全格式快照/恢复) ─────────────────────────
// Clipboard 接口的 Win32 实现 + 内存块读写辅助函数

package win32

import (
	"bytes"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/LeuJasYoh/type/internal/typing"
)

// Clipboard Clipboard 接口的 Win32 实现
type Clipboard struct{}

const (
	cfUnicodeText = 13
	cfBitmap      = 2 // 以下三者 GetClipboardData 返回 GDI 句柄而非 HGLOBAL
	cfPalette     = 9
	cfEnhMetafile = 14
	// CF_METAFILEPICT 块本身就是内存, 但块里装着另一个图形句柄(hmf),
	// EmptyClipboard 之后那个句柄已失效: 照抄会恢复出一个悬空句柄
	cfMetafilePict = 3
	// 所有者绘制与"私有显示"格式: 数据由持有方解释, 形状不保证是内存块
	cfOwnerDisplay    = 0x0080
	cfDspText         = 0x0081
	cfDspBitmap       = 0x0082
	cfDspMetafilePict = 0x0083
	cfDspEnhMetafile  = 0x008E
	// GDI 对象格式族: 数据是 GDI 句柄, 不是内存块
	cfGdiObjFirst = 0x0300
	cfGdiObjLast  = 0x03FF
	// 程序私有格式族: 通常是内存块, 刻意不跳过(见 skippableFormat)
	cfPrivateFirst = 0x0200
	cfPrivateLast  = 0x02FF
	gmemMovable    = 0x0002
	gmemZeroInit   = 0x0040
	ghnd           = gmemMovable | gmemZeroInit
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

func (Clipboard) SetText(text string, snap []typing.ClipboardFormat) (written bool, preserved bool) {
	hMem := prepareClipboardData(encodedText(text))
	if hMem == 0 {
		return false, true
	}
	defer func() {
		if hMem != 0 {
			procGlobalFree.Call(hMem)
		}
	}()
	if !openClipboardWithRetry() {
		return false, true
	}
	defer procCloseClipboard.Call()
	if ret, _, _ := procEmptyClipboard.Call(); ret == 0 {
		return false, true
	}
	if ret, _, _ := procSetClipboardData.Call(cfUnicodeText, hMem); ret == 0 {
		return false, restoreClipboardFormats(snap)
	}
	hMem = 0 // SetClipboardData 成功后, 内存所有权归系统。
	return true, true
}

func prepareClipboardData(data []byte) uintptr {
	hMem, _, _ := procGlobalAlloc.Call(ghnd, uintptr(len(data)))
	if hMem == 0 {
		return 0
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem)
		return 0
	}
	procRtlMoveMemory.Call(ptr, uintptr(unsafe.Pointer(unsafe.SliceData(data))), uintptr(len(data)))
	procGlobalUnlock.Call(hMem)
	return hMem
}

func (Clipboard) GetText() string {
	if !openClipboardWithRetry() {
		return ""
	}
	defer procCloseClipboard.Call()
	hMem, _, _ := procGetClipboardData.Call(cfUnicodeText)
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

// clipboardHoldsText 在已打开的剪贴板内逐字节比较, 不截断内嵌 NUL。
func clipboardHoldsText(text string) (holds bool, known bool) {
	hMem, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if hMem == 0 {
		// 我们自己 SetClipboardData 过 CF_UNICODETEXT, 取不到即说明剪贴板已被重写
		return false, true
	}
	size, _, _ := procGlobalSize.Call(hMem)
	if size == 0 {
		// 分不清"这是别人的空块"与"GlobalSize 本身失败", 按读不到处理:
		// 判成"已改动"会静默跳过恢复, 那正是这条守卫要堵的窟窿
		return false, false
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		return false, false
	}
	buf := make([]byte, int(size))
	// 拷贝长度不超过缓冲区容量, 防止 GlobalSize 返回奇数时越界
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(unsafe.SliceData(buf))), ptr, uintptr(len(buf)))
	procGlobalUnlock.Call(hMem)
	return bytes.Equal(buf, encodedText(text)), true
}

// handleFormats: GetClipboardData 返回 GDI 句柄而非 HGLOBAL 内存块的标准格式,
// 无法按字节复制, 快照时跳过 (延迟渲染格式 GetClipboardData 返回 0, 同样跳过)
var handleFormats = map[uint32]struct{}{
	cfBitmap:      {},
	cfPalette:     {},
	cfEnhMetafile: {},
}

// skippableFormat 该格式是否不适合按"一整块内存"快照: 判据是"这块数据是不是普通
// 内存块", 不是"格式少见"。句柄型格式(GetClipboardData 直接返回 GDI 句柄)、
// CF_METAFILEPICT(块里的图形句柄已随 EmptyClipboard 失效)、所有者绘制/私有显示/
// GDI 对象格式族照抄下来恢复时会写回失效句柄或垃圾字节, 受害的是系统里其它程序。
// CF_PRIVATEFIRST..LAST(0x0200-0x02FF)通常是内存块, 刻意不跳过
func skippableFormat(fmt uint32) bool {
	if _, skip := handleFormats[fmt]; skip {
		return true
	}
	switch fmt {
	case cfMetafilePict, cfOwnerDisplay,
		cfDspText, cfDspBitmap, cfDspMetafilePict, cfDspEnhMetafile:
		return true
	}
	return fmt >= cfGdiObjFirst && fmt <= cfGdiObjLast
}

// Snapshot 复制当前剪贴板的全部内存块型格式(文本 / CF_DIB / CF_HDROP / HTML Format)。
// Complete 的唯一含义是"这份快照能不能拿去恢复": 恢复会先清空剪贴板, 拿残缺快照
// 去恢复等于把没抄到的格式永久销毁。跳过的格式不计入 Complete, 取舍见 skippableFormat。
// 剪贴板为空时返回**非 nil 空切片 + Complete**(表示"原本为空", 可恢复成空); 打开失败、
// 有格式没抄到、或只枚举到被跳过的格式时返回 nil(原状态未知, 不许碰剪贴板)。
// 见 docs/invariants.md「剪贴板快照与恢复」
func (Clipboard) Snapshot() typing.ClipboardSnapshot {
	if !openClipboardWithRetry() {
		return typing.ClipboardSnapshot{} // 原状态未知, 无从恢复
	}
	defer procCloseClipboard.Call()

	snap := make([]typing.ClipboardFormat, 0, 8)
	complete := true
	sawSkippable := false
	for fmt := uint32(0); ; {
		next, _, _ := procEnumClipboardFormats.Call(uintptr(fmt))
		if next == 0 {
			break
		}
		fmt = uint32(next)
		if skippableFormat(fmt) {
			// 记下"枚举到过不抄的格式": 这种剪贴板不能当成空剪贴板, 否则恢复时会
			// 先清空, 把用户的位图/文件永久清掉
			sawSkippable = true
			continue
		}
		data, ok := readClipboardFormat(fmt)
		if !ok {
			// 没能照抄下来的格式: 恢复时写不回去, 这份快照就不算完整
			complete = false
			continue
		}
		snap = append(snap, typing.ClipboardFormat{Fmt: fmt, Data: data})
	}
	if len(snap) == 0 {
		// 打开成功、枚举到 0 个格式、也没有被跳过的格式: 剪贴板确实为空。
		// 用非 nil 空切片表达"原本为空"(可恢复成空), 与 nil 的"原状态未知"区分;
		// 其余情况(没抄到 / 只有被跳过的格式)一律按无从恢复处理。
		if complete && !sawSkippable {
			return typing.ClipboardSnapshot{Formats: make([]typing.ClipboardFormat, 0), Complete: true}
		}
		return typing.ClipboardSnapshot{}
	}
	return typing.ClipboardSnapshot{Formats: snap, Complete: complete}
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
func writeClipboardFormats(snap []typing.ClipboardFormat) bool {
	all := true
	for _, cf := range snap {
		hMem := prepareClipboardData(cf.Data)
		if hMem == 0 {
			all = false
			continue
		}
		if ret, _, _ := procSetClipboardData.Call(uintptr(cf.Fmt), hMem); ret == 0 {
			procGlobalFree.Call(hMem) // 系统未接管所有权时由调用方释放
			all = false
		}
	}
	return all
}

func (Clipboard) RestoreSnapshot(snap []typing.ClipboardFormat, injected string) bool {
	if snap == nil {
		return false
	}
	if !openClipboardRestoreRetry() {
		return false
	}
	defer procCloseClipboard.Call()
	holds, known := clipboardHoldsText(injected)
	if !known {
		return false
	}
	if !holds {
		return true
	}
	return restoreClipboardFormats(snap)
}

// RestoreSnapshotRaw 无条件写回快照, 调用方须确保期间没有别人改动剪贴板。
func (Clipboard) RestoreSnapshotRaw(snap []typing.ClipboardFormat) bool {
	if snap == nil {
		return false
	}
	if !openClipboardRestoreRetry() {
		return false
	}
	defer procCloseClipboard.Call()
	return restoreClipboardFormats(snap)
}

// restoreClipboardFormats 只在持有剪贴板期间调用, 回滚前不释放给其他程序。
func restoreClipboardFormats(snap []typing.ClipboardFormat) bool {
	if snap == nil {
		return false
	}
	if ret, _, _ := procEmptyClipboard.Call(); ret == 0 {
		return false
	}
	return writeClipboardFormats(snap)
}
