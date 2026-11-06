package ui

import (
	"strings"
	"testing"
)

// Issue #425: Find walks the element tree without checking geometry, so it
// can return an element the window never shows. Clicking it must report the
// miss instead of silently clicking nothing, while an on-window element
// still clicks as before.
func TestClickOffWindowElement(t *testing.T) {
	var entered string
	view := func(c *Context) {
		Row(c).Translate(1400, 100).Children(func() {
			Button(c, "off").OnClick(func() { t.Error("off-window button must not be clicked") })
		})
		Button(c, "on").OnClick(func() { entered = "on" })
	}

	tt := NewTester(view, 1200, 800)
	tt.Frame()

	if err := tt.Click("off"); err == nil {
		t.Fatal("Click on an off-window element returned nil")
	} else if !strings.Contains(err.Error(), "outside the window") {
		t.Fatalf("Click error does not name the cause: %v", err)
	}

	if err := tt.ClickWith(Shift, "off"); err == nil {
		t.Fatal("ClickWith on an off-window element returned nil")
	}

	if err := tt.RightClick("off"); err == nil {
		t.Fatal("RightClick on an off-window element returned nil")
	}

	if err := tt.Click("on"); err != nil {
		t.Fatalf("Click on an on-window element failed: %v", err)
	}
	if entered != "on" {
		t.Fatal("Click on an on-window element did not reach the button")
	}
}

// A rejected named click must send no input: the empty clipped rectangle
// can sit over another control, and a covered label still has a visible box.
func TestClickRejectsClippedOrCoveredElement(t *testing.T) {
	actions := []struct {
		name string
		run  func(*Tester, string) error
	}{
		{"Click", (*Tester).Click},
		{"ClickWith", func(tt *Tester, s string) error { return tt.ClickWith(Shift, s) }},
		{"RightClick", (*Tester).RightClick},
	}
	for _, fixture := range []string{"clipped", "scrolled", "covered", "ignored", "inset", "nested", "webview"} {
		for _, action := range actions {
			t.Run(fixture+"/"+action.name, func(t *testing.T) {
				clicks, presses := 0, 0
				tt := NewTester(func(c *Context) {
					target := func() {
						Button(c, "target").Label("target").Absolute().Top(60).Left(0).Size(100, 40).
							OnClick(func() { clicks++ }).
							ContextMenu(func(m *Menu) { m.Item("target-menu") })
					}
					switch fixture {
					case "clipped":
						Box(c).Size(100, 40).Clip().Children(target)
					case "scrolled":
						Scroll(c).Size(100, 40).Children(func() {
							Box(c).Size(100, 60).Shrink(0)
							Button(c, "target").Label("target").Size(100, 40).Shrink(0)
						})
					case "ignored":
						Button(c, "target").Label("target").Absolute().Top(60).Left(0).Size(100, 40).IgnoreInput(true)
					case "inset":
						Button(c, "target").Label("target").Absolute().Top(60).Left(0).Size(100, 40).
							ClipInsetPercent(0, 80, 0, 0, 0)
					case "nested":
						Button(c, "").Label("target").Absolute().Top(60).Left(0).Size(100, 40).
							Children(func() {
								Button(c, "cover").Absolute().Fill().OnClick(func() { clicks++ }).
									ContextMenu(func(m *Menu) { m.Item("cover-menu") })
							})
					default:
						target()
					}
					if fixture == "webview" {
						WebView(c, &testWebView{"cover"}).Absolute().Top(60).Left(0).Size(100, 40)
					} else if fixture != "nested" && fixture != "inset" {
						Button(c, "cover").Absolute().Top(60).Left(0).Size(100, 40).
							OnClick(func() { clicks++ }).
							ContextMenu(func(m *Menu) { m.Item("cover-menu") }).
							HandleInput(func(ev InputEvent) bool {
								if ev.Kind == InputPointerDown {
									presses++
								}
								return false
							})
					}
				}, 200, 200)
				defer tt.rt.close()
				beforeX, beforeY, beforeFocus := tt.rt.pointerX, tt.rt.pointerY, tt.rt.focused
				if err := action.run(tt, "target"); err == nil {
					t.Error("click on an unreachable target returned nil")
				}
				if clicks != 0 || presses != 0 || len(tt.Menu()) != 0 {
					t.Errorf("rejected click sent input: clicks=%d presses=%d menu=%v", clicks, presses, tt.Menu())
				}
				if tt.rt.pointerX != beforeX || tt.rt.pointerY != beforeY || tt.rt.focused != beforeFocus {
					t.Error("rejected click changed the pointer or focus")
				}
			})
		}
	}
}

func TestClickVisibleElement(t *testing.T) {
	for _, fixture := range []string{"text", "partial", "zoom", "disabled", "sibling", "field"} {
		t.Run(fixture, func(t *testing.T) {
			clicks := 0
			tt := NewTester(func(c *Context) {
				if fixture == "field" {
					query := "search"
					SearchField(c, &query).Label("target").Size(200, 40)
					return
				}
				build := func() {
					b := Button(c, "target").Size(100, 40).Disabled(fixture == "disabled").
						OnClick(func() { clicks++ })
					if fixture == "sibling" {
						b.Children(func() {
							Text(c, "target")
							Box(c).Absolute().Fill()
						})
					}
				}
				if fixture == "partial" {
					Box(c).Size(60, 30).Clip().Children(build)
				} else {
					build()
				}
			}, 240, 160)
			defer tt.rt.close()
			if fixture == "zoom" {
				tt.SetZoom(2)
			}
			if err := tt.Click("target"); err != nil {
				t.Fatal(err)
			}
			want := 1
			if fixture == "disabled" {
				want = 0
			}
			if fixture == "field" {
				if !tt.Focused("target") {
					t.Error("the field did not focus its input")
				}
			} else if clicks != want {
				t.Errorf("clicks=%d, want %d", clicks, want)
			}
		})
	}
}

func TestClickRejectsVisibleScrollbarOverElement(t *testing.T) {
	clicks := 0
	tt := NewTester(func(c *Context) {
		Scroll(c).Size(100, 80).KeepScrollbars(true).Children(func() {
			Button(c, "").Label("target").Absolute().Left(94).Top(10).Size(6, 40).
				OnClick(func() { clicks++ })
			Box(c).Size(100, 400).Shrink(0)
		})
	}, 200, 200)
	defer tt.rt.close()
	if err := tt.Click("target"); err == nil {
		t.Error("a click intercepted by the visible scrollbar returned nil")
	}
	if clicks != 0 || tt.rt.scrollDrag.st != nil {
		t.Errorf("rejected click changed input state: clicks=%d drag=%v", clicks, tt.rt.scrollDrag.st)
	}
	for _, st := range tt.rt.states {
		if st.scrollY != 0 {
			t.Errorf("rejected click scrolled to %v", st.scrollY)
		}
	}
}

// A child that only defines a context menu still leaves primary clicks
// to its button, while secondary clicks must resolve that child's menu.
func TestNamedClickUsesTheRequestedButton(t *testing.T) {
	clicks := 0
	tt := NewTester(func(c *Context) {
		Button(c, "").Label("target").Size(100, 40).OnClick(func() { clicks++ }).
			ContextMenu(func(m *Menu) { m.Item("button-menu") }).
			Children(func() {
				Box(c).Absolute().Fill().ContextMenu(func(m *Menu) { m.Item("child-menu") })
			})
	}, 200, 100)
	defer tt.rt.close()
	if err := tt.Click("target"); err != nil {
		t.Error(err)
	}
	if clicks != 1 {
		t.Errorf("primary click did not reach the button: %d", clicks)
	}
	if err := tt.RightClick("target"); err == nil {
		t.Error("secondary click targeting a different menu returned nil")
	}
	if len(tt.Menu()) != 0 {
		t.Errorf("rejected secondary click opened %v", tt.Menu())
	}
}

// IgnoreInput does not stop the system from delivering pointer input to a
// native page, including one nested in a container that ignores input.
func TestNamedClickOnWebViewIgnoringInput(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "container"}[nested], func(t *testing.T) {
			page := &testWebView{"page"}
			tt := NewTester(func(c *Context) {
				if nested {
					Box(c).Size(100, 80).IgnoreInput(true).Children(func() {
						WebView(c, page).Label("page").Fill()
					})
				} else {
					WebView(c, page).Label("page").Size(100, 80).IgnoreInput(true)
				}
			}, 200, 100)
			defer tt.rt.close()
			if tt.WebViewAt(50, 40) != page {
				t.Fatal("the native page does not take the pointer")
			}
			for _, click := range []func() error{
				func() error { return tt.Click("page") },
				func() error { return tt.ClickWith(Shift, "page") },
				func() error { return tt.RightClick("page") },
			} {
				if err := click(); err != nil {
					t.Error(err)
				}
			}
		})
	}
}
