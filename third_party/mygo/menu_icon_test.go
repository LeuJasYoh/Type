//go:build !js

package mygo

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"testing"

	"github.com/egoist/mygo/internal/platform"
)

// flag returns a PNG image of w by h pixels in one color.
func flag(t *testing.T, w, h int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// lastUpdate returns the last update the backend got of an item.
func lastUpdate(t *testing.T, it *MenuItem) *platform.MenuItem {
	t.Helper()
	onMain(func() {}) // the update reaches the backend on the main thread
	updates := fb.MenuUpdates()
	for i := len(updates) - 1; i >= 0; i-- {
		if updates[i].ID == it.uid {
			return updates[i]
		}
	}
	t.Fatalf("no update of %q", it.Label)
	return nil
}

// icon makes a menu icon.
func icon(t *testing.T, data []byte, template bool) *MenuIcon {
	t.Helper()
	ic, err := NewMenuIcon(data, template)
	if err != nil {
		t.Fatal(err)
	}
	return ic
}

func TestMenuItemIcon(t *testing.T) {
	red := flag(t, 24, 16, color.NRGBA{255, 0, 0, 255})
	blue := flag(t, 24, 16, color.NRGBA{0, 0, 255, 255})
	japan := icon(t, red, false)
	menu := NewMenu([]*MenuItem{
		{ID: "jp", Label: "Tokyo", Type: MenuItemRadio, Checked: true, Icon: japan},
		{ID: "us", Label: "New York", Type: MenuItemRadio, Icon: icon(t, bytes.Clone(red), false)},
		{ID: "none", Label: "Direct", Type: MenuItemRadio},
		{ID: "tmpl", Label: "Template", Icon: icon(t, red, true)},
	})
	snap := menu.snapshot()
	jp, us := snap.Items[0], snap.Items[1]
	if jp.Icon == nil || !bytes.Equal(jp.Icon.PNG, red) || jp.Icon.Template {
		t.Fatalf("icon not in the snapshot: %+v", jp.Icon)
	}
	if !jp.Checked {
		t.Error("an item with an icon lost its check")
	}
	if us.Icon != jp.Icon {
		t.Error("icons of the same image do not share it, which backends decode once")
	}
	if snap.Items[2].Icon != nil {
		t.Error("an item without an image has an icon")
	}
	if tmpl := snap.Items[3].Icon; tmpl == nil || !tmpl.Template || tmpl == jp.Icon {
		t.Errorf("template icon = %+v, want its own, a template", tmpl)
	}
	if again := menu.snapshot(); again.Items[0].Icon != jp.Icon {
		t.Error("a snapshot made again has another icon: backends would decode it again")
	}
	// An app that makes its icons and items again shares the image too.
	rebuilt := NewMenu([]*MenuItem{{Label: "Tokyo", Icon: icon(t, bytes.Clone(red), false)}})
	if rebuilt.snapshot().Items[0].Icon != jp.Icon {
		t.Error("an icon made again of the same image does not share it")
	}

	// Replacing the image updates the item, with the new icon.
	tokyo := menu.ItemByID("jp")
	tokyo.SetIcon(icon(t, blue, false))
	up := lastUpdate(t, tokyo)
	if up.Icon == nil || up.Icon == jp.Icon || !bytes.Equal(up.Icon.PNG, blue) {
		t.Errorf("SetIcon not propagated: %+v", up.Icon)
	}
	if !up.Checked {
		t.Error("SetIcon unchecked the item")
	}
	replaced := up.Icon
	// Other changes keep the icon, so backends leave it be.
	tokyo.SetLabel("Tokyo 2")
	if up := lastUpdate(t, tokyo); up.Icon != replaced || up.Label != "Tokyo 2" {
		t.Errorf("SetLabel changed the icon: %p, want %p", up.Icon, replaced)
	}
	// Checking another radio item sends this one, unchecked, with its icon.
	menu.ItemByID("none").SetChecked(true)
	if up := lastUpdate(t, tokyo); up.Checked || up.Icon != replaced {
		t.Errorf("radio sibling update = checked %v, icon %p, want unchecked with %p", up.Checked, up.Icon, replaced)
	}

	// Clearing removes it.
	tokyo.SetIcon(nil)
	if up := lastUpdate(t, tokyo); up.Icon != nil {
		t.Error("SetIcon(nil) left the icon")
	}
	if menu.snapshot().Items[0].Icon != nil {
		t.Error("the item still has its image")
	}
	runtime.KeepAlive(rebuilt)
}

func TestNewMenuIcon(t *testing.T) {
	// The data is copied: changing the caller's slice changes nothing.
	blue := flag(t, 4, 4, color.NRGBA{0, 0, 255, 255})
	buf := bytes.Clone(blue)
	ic := icon(t, buf, false)
	buf[len(buf)-1] ^= 0xFF
	if !bytes.Equal(ic.p.PNG, blue) {
		t.Error("the icon shares the caller's slice")
	}
	for _, data := range [][]byte{nil, []byte("GIF89a"), blue[:20]} {
		if ic, err := NewMenuIcon(data, false); err == nil || ic != nil {
			t.Errorf("NewMenuIcon(%q) = %v, %v, want an error", data, ic, err)
		}
	}
}

// The images no icon shows any more are forgotten.
func TestMenuIconsForgotten(t *testing.T) {
	for i := range 512 {
		icon(t, flag(t, 1+i/256, 1, color.NRGBA{uint8(i), 0, 0, 255}), false)
		if i%16 == 0 {
			runtime.GC()
		}
	}
	menuMu.Lock()
	n := len(menuIcons)
	menuMu.Unlock()
	if n >= 256 {
		t.Errorf("%d images kept of 512 no icon shows", n)
	}
}
