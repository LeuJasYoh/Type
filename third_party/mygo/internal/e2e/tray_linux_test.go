//go:build linux

package e2e

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/internal/linux"
)

// TestTrayActivation checks that a panel's activation, the double click on
// GNOME's AppIndicator icons, reaches Tray.OnClick, and that an icon whose
// clicks have nowhere to go answers no activation, so that a panel opens
// its menu on the first click.
func TestTrayActivation(t *testing.T) {
	tray, err := mygo.NewTray(mygo.TrayOptions{Icon: squarePNG(), ToolTip: "activation"})
	if err != nil {
		t.Skip("no tray: ", err)
	}
	defer tray.Destroy()
	var activates bool
	mygo.RunOnMain(func() { activates = linux.TestTrayActivates() })
	if !activates {
		t.Skip("this libayatana-appindicator3 has no activate signal")
	}

	var activated bool
	mygo.RunOnMain(func() { activated = linux.TestActivateTray() })
	if activated {
		t.Fatal("an icon with no click listener answered activation")
	}

	clicked := make(chan struct{}, 1)
	off := tray.OnClick(func() { clicked <- struct{}{} })
	mygo.RunOnMain(func() { activated = linux.TestActivateTray() })
	if !activated {
		t.Fatal("an icon with a click listener answered no activation")
	}
	select {
	case <-clicked:
	case <-time.After(5 * time.Second):
		t.Fatal("OnClick did not run when the icon was activated")
	}

	off()
	mygo.RunOnMain(func() { activated = linux.TestActivateTray() })
	if activated {
		t.Fatal("an icon whose last click listener left answered activation")
	}
}
