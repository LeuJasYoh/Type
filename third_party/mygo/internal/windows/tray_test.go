//go:build windows && (amd64 || arm64)

package windows

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// TestTrayNumber numbers icons by their IDs, whatever order they come in,
// apart from the numbers NewTray counts.
func TestTrayNumber(t *testing.T) {
	number := func(ids ...string) map[string]uint32 {
		b := &Backend{trays: map[uint32]*tray{}}
		out := map[string]uint32{}
		for _, id := range ids {
			b.nextTray++
			tr := &tray{b: b, id: b.nextTray}
			b.trays[tr.id] = tr
			tr.SetID(id)
			if b.trays[tr.id] != tr || len(b.trays) != int(b.nextTray) {
				t.Fatalf("%q: the icons are %v", id, b.trays)
			}
			out[id] = tr.id
		}
		return out
	}
	a, b := number("app.cpu", "app.memory"), number("app.memory", "app.cpu")
	for id, n := range a {
		if b[id] != n {
			t.Errorf("%q is %#x, then %#x", id, n, b[id])
		}
		if n&(1<<31) == 0 {
			t.Errorf("%q is %#x, which NewTray may count to", id, n)
		}
	}
	if a["app.cpu"] == a["app.memory"] {
		t.Error("two IDs have one number")
	}
	// Without an ID, or with one another icon has, an icon keeps its own.
	if n := number("")[""]; n != 1 {
		t.Errorf("an icon without an ID is %#x", n)
	}
	if twice := number("app.cpu", "app.cpu"); twice["app.cpu"] != 2 {
		t.Errorf("an icon with the ID of another is %#x", twice["app.cpu"])
	}
}

// TestWhiten draws a template image white on a dark taskbar, as macOS
// tints it, keeping its shape.
func TestWhiten(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	src.SetNRGBA(0, 0, color.NRGBA{0, 0, 0, 255})
	src.SetNRGBA(1, 0, color.NRGBA{0, 0, 0, 56})
	var b bytes.Buffer
	if err := png.Encode(&b, src); err != nil {
		t.Fatal(err)
	}
	out, err := whiten(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	for x, a := range []uint8{255, 56} {
		if c := color.NRGBAModel.Convert(img.At(x, 0)).(color.NRGBA); c != (color.NRGBA{255, 255, 255, a}) {
			t.Errorf("pixel %d is %v", x, c)
		}
	}
}

// TestTraysAppearanceTemporary turns the taskbar dark or light while a
// notification's temporary icon shows: it has no handler to tell.
func TestTraysAppearanceTemporary(t *testing.T) {
	b := &Backend{trays: map[uint32]*tray{}}
	tr := &tray{b: b, id: 1, temporary: true}
	tr.dark = !tr.Dark()
	b.trays[tr.id] = tr
	b.traysAppearance()
}
