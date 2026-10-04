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

func (Clipboard) SetText(text string) bool {
	if !openClipboardWithRetry() {
		return false
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()

	encoded := encodedText(text)
	size := len(encoded)
	hMem, _, _ := procGlobalAlloc.Call(ghnd, uintptr(size))
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
	ret, _, _ := procSetClipboardData.Call(cfUnicodeText, hMem)
	if ret == 0 {
		procGlobalFree.Call(hMem) // 系统未接管所有权时由调用方释放
	}
	return ret != 0
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

// HoldsText 判断剪贴板当前文本是否就是 text, 并回报这次比对有没有得出结论。
// 按原始字节比对而非 GetText 的字符串: CF_UNICODETEXT 内嵌 NUL 时 GetText 会在
// NUL 处截断, 字符串比较将误判为"用户已改动"而跳过恢复。
//
// 三个落点各是一种不同的事实, 不许压成一个 bool(旧签名就是那样, 结果"读不到"
// 被并进"用户已改动", 于是原内容已经丢了还报"输入完成"):
//   - 打开剪贴板失败、GlobalLock 失败、GlobalSize 返回 0: 读不到 —— known=false,
//     调用方必须按"没恢复"上报;
//   - 打开成功但没有 CF_UNICODETEXT: 我们写进去的那份已经不在了(剪贴板被别的
//     程序或用户重新写过), holds=false、known=true —— 跳过恢复正合期望;
//   - 读到数据: 按原始字节给出 holds。
func (Clipboard) HoldsText(text string) (holds bool, known bool) {
	if !openClipboardWithRetry() {
		return false, false
	}
	defer procCloseClipboard.Call()
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
	cfBitmap:      {},
	cfPalette:     {},
	cfEnhMetafile: {},
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
	case cfMetafilePict, cfOwnerDisplay,
		cfDspText, cfDspBitmap, cfDspMetafilePict, cfDspEnhMetafile:
		return true
	}
	return fmt >= cfGdiObjFirst && fmt <= cfGdiObjLast
}

// Snapshot 复制当前剪贴板的全部内存块型格式(文本/图片 CF_DIB/文件
// CF_HDROP/HTML Format 等)。
//
// Complete 的含义只有一条: 这份快照能不能拿来恢复。读某个格式失败时如实报
// false —— 恢复流程会先清空剪贴板, 拿残缺的快照去恢复等于把没抄到的那些格式
// 永久销毁, 而用户看到的会是"输入完成"。调用方据此放弃剪贴板这条路(见
// internal/typing 的 ClipboardSnapshot)。
//
// 两个容易误读的地方:
//   - 打开失败与"一个格式都没读到"同样报 Complete=false。这两种情况分不清
//     (枚举不到也可能只是剪贴板本来就空), 对调用方也没区别, 都按无从恢复处理;
//   - skippableFormat 跳过的那些格式不计入 Complete。那是既定的取舍: 句柄型
//     与含句柄的格式照抄下来恢复时会写回失效句柄或垃圾字节, 受害的是系统里
//     别的程序, 宁可不恢复(见该函数的说明)。所以"带截图/位图的剪贴板"照样是
//     完整快照, 不会因此把整条粘贴路径踢掉
func (Clipboard) Snapshot() typing.ClipboardSnapshot {
	if !openClipboardWithRetry() {
		return typing.ClipboardSnapshot{} // 原状态未知, 无从恢复
	}
	defer procCloseClipboard.Call()

	snap := make([]typing.ClipboardFormat, 0, 8)
	complete := true
	for fmt := uint32(0); ; {
		next, _, _ := procEnumClipboardFormats.Call(uintptr(fmt))
		if next == 0 {
			break
		}
		fmt = uint32(next)
		if skippableFormat(fmt) {
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
		// 一个格式都没读到: 分不清"剪贴板本来就空"与"全部读失败",
		// 按无从恢复处理, 免得把没能照抄的内容当成空剪贴板清掉
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
		hMem, _, _ := procGlobalAlloc.Call(ghnd, uintptr(len(cf.Data)))
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
func (Clipboard) RestoreSnapshotRaw(snap []typing.ClipboardFormat) bool {
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
