//go:build windows && (amd64 || arm64)

package windows

import (
	"testing"
	"unsafe"
)

// The highlights of menu items cover their pixels inside fully, those past
// the rounded corners not at all, and those the corners cross partly.
func TestRoundedCoverage(t *testing.T) {
	r := &rect{0, 0, 40, 20}
	for _, c := range []struct {
		x, y     float64
		min, max float64
	}{
		{20.5, 10.5, 1, 1}, // the middle
		{0.5, 10.5, 1, 1},  // the left edge, between the corners
		{20.5, 0.5, 1, 1},  // the top edge
		{0.5, 0.5, 0, 0},   // past the top-left corner's curve
		{39.5, 19.5, 0, 0}, // past the bottom-right one's
		{1.5, 1.5, 0.01, 0.99},
	} {
		if got := roundedCoverage(c.x, c.y, r, 4); got < c.min || got > c.max {
			t.Errorf("coverage at (%v, %v) = %v, want %v to %v", c.x, c.y, got, c.min, c.max)
		}
	}
}

func TestMenuColors(t *testing.T) {
	if got := dibColor(0x332211); got != 0x112233 {
		t.Errorf("dibColor(0x332211) = %#x, want 0x112233", got)
	}
	if got := blend(0xFFFFFF, 0x000000, 0.5); got != 0x808080 {
		t.Errorf("blend halfway = %#x, want 0x808080", got)
	}
	if got := blend(0x102030, 0xA0B0C0, 0); got != 0xA0B0C0 {
		t.Errorf("blend with none = %#x, want the background", got)
	}
}

// A label of a menu item drawn as wide as it measures shows all its ink,
// that past its advance included: Microsoft YaHei UI's, the menu font of
// Chinese Windows, reaches a pixel past it in 粘贴, 删除 and 全选 (#1028).
func TestMenuLabelInk(t *testing.T) {
	dpi := dpiOf(0)
	const face = "Microsoft YaHei UI"
	// In ClearType, as menus draw where it is on, whose ink reaches
	// further than grayscale's.
	const clearTypeQuality = 5
	lf := logFont{Height: -toPx(14, dpi), Weight: 400, Quality: clearTypeQuality}
	copy(lf.FaceName[:], utf16z(face))
	text, _, _ := procCreateFontIndirectW.Call(uintptr(unsafe.Pointer(&lf)))
	defer procDeleteObject.Call(text)
	if !fontFace(text, face) {
		t.Skip(face + " is not installed")
	}
	glyphs := createFont(-toPx(menuGlyphSize, dpi), "Segoe UI")
	defer procDeleteObject.Call(glyphs)
	b := &Backend{themeSource: "light", menuFonts: map[int]menuFonts{dpi: {text: text, glyphs: glyphs}}}
	p := b.menuPalette()
	back := dibColor(p.back)
	screen, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, screen)
	for _, label := range []string{"撤销", "剪切", "复制", "粘贴", "删除", "全选", "查看歌手：房东的猫"} {
		// Its ink drawn with room around it.
		const pad = 50
		w := textWidth(screen, text, label) + 2*pad
		h := toPx(menuItemHeight, dpi)
		img := newBitmap(screen, w, h)
		for i := range img.px {
			img.px[i] = back
		}
		old, _, _ := procSelectObject.Call(img.dc, text)
		procSetBkMode.Call(img.dc, 1) // TRANSPARENT
		procSetTextColor.Call(img.dc, uintptr(p.text))
		at := rect{pad, 0, w, h}
		procDrawTextW.Call(img.dc, uintptr(unsafe.Pointer(u16(label))), ^uintptr(0), uintptr(unsafe.Pointer(&at)), dtSingleLine|dtVCenter|dtNoClip)
		procGdiFlush.Call()
		procSelectObject.Call(img.dc, old)
		want := inkLast(img, w, back)
		want -= pad
		img.free()

		// The item, as wide as it needs.
		l := &menuLook{label: label}
		w = b.itemWidth(l, dpi)
		img = newBitmap(screen, w, h)
		b.drawItem(0, l, &drawItemStruct{CtlType: odtMenu, DC: img.dc, Item: rect{0, 0, w, h}})
		procGdiFlush.Call()
		got := inkLast(img, w, back)
		got -= toPx(menuInset+menuPadding, dpi)
		img.free()
		if want < 0 || got != want {
			t.Errorf("%q: the item shows its ink up to column %d of the label, want %d", label, got, want)
		}
	}
}

// inkLast returns the last column of a bitmap w pixels wide holding
// pixels other than back, -1 for none.
func inkLast(img *bitmap, w int32, back uint32) int32 {
	last := int32(-1)
	for i, p := range img.px {
		if p&0xFFFFFF != back {
			last = max(last, int32(i)%w)
		}
	}
	return last
}
