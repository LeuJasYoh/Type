package ui

import (
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo/internal/platform"
)

// PopoverMenu gives the element a context menu, as ContextMenu does, which
// MyGo draws in the window with the theme's look, as a popover, rather
// than the system showing one of its own. It opens where the element is
// clicked with the secondary button, or Control-clicked on macOS, and,
// while the element or one inside it has the focus, below it when the
// menu key or Shift+F10 is pressed. build fills it with the items
// ContextMenu's function takes, and the item chosen reports Chosen as
// there:
//
//	row.PopoverMenu(func(m *ui.Menu) {
//		if m.Item("Rename").Shortcut(0, ui.KeyF2).Chosen() {
//			app.renaming = i
//		}
//		if m.Item("Pinned").Checked(note.pinned).Chosen() {
//			note.pinned = !note.pinned
//		}
//		m.Separator()
//		if m.Item("Delete").Disabled(note.locked).Chosen() {
//			app.delete(i)
//		}
//	})
//
// The menu takes the focus as it opens and gives it back as it closes, to
// the element that had it. Up and Down move among its items, past those
// disabled, as do Home, End and typing the first letters of an item;
// Right opens a submenu and Left closes it, the other way round right to
// left (Direction); Enter and Space choose an item, Escape closes the
// submenu on top, and a press outside the menu closes it. It shows below
// and after where it opened, and on the other side where the window has
// no room. Assistive technology sees a menu of items, check box items and
// radio items, as the system's. ContextMenuBase is such a menu without a
// look, for items of a layout of one's own.
//
// PopoverMenuButton draws the menu of a menu button so.
func (e *node) PopoverMenu(build func(m *Menu)) *node {
	e.flags |= flagContextMenu
	e.buildMenu(false, true, build)
	e.c.rt.drawMenu(e, build)
	return e
}

// PopoverMenuButton makes the element a menu button, as Menu does, whose
// menu MyGo draws in the window with the theme's look, as PopoverMenu
// draws a context menu, rather than the system showing one of its own. It
// opens below the element, or above it where the window has no room, as
// the pointer goes down on it, and for Enter, Space or Down while it has
// the focus, which it takes. build fills it with the items Menu's
// function takes, and the item chosen reports Chosen as there:
//
//	more := ui.ButtonBase(c).Label("More").Padding(4).Radius(6)
//	more.Children(func() { ui.Icon(c, moreIcon).Size(16, 16) })
//	more.PopoverMenuButton(func(m *ui.Menu) {
//		if m.Item("Duplicate").Chosen() {
//			app.duplicate()
//		}
//		if m.Item("Delete").Disabled(app.locked).Chosen() {
//			app.delete()
//		}
//	})
//
// The menu has the keys of PopoverMenu's, and takes the focus as it opens
// and gives it back as it closes, as there; a press on the element closes
// it too. Opened by the keyboard, it starts on its first item. Assistive
// technology sees a menu button, expanded while its menu shows, and the
// menu as PopoverMenu's. The function PopoverMenuButton is a button with
// such a menu, as MenuButton is one with a menu of the system's, and
// MenuBase a menu button without a look.
func (e *node) PopoverMenuButton(build func(m *Menu)) *node {
	e.flags |= flagMenuButton | flagClickable | flagFocusable
	if e.role == RoleAuto || e.role == RoleButton {
		e.role = RoleMenuButton
	}
	e.buildMenu(true, true, build)
	e.c.rt.drawMenu(e, build)
	return e
}

// MenuParts are the parts of a menu without a look: MenuBase makes its
// menu button, or ContextMenuBase gives an element a context menu; Popup
// shows the menu, Item and the others its items, and Submenu the
// submenus they open.
type menuParts struct {
	// Trigger opens the menu: the menu button MenuBase makes, which takes
	// children, or the element ContextMenuBase gives the menu to.
	Trigger *node
	c       *context
	open    *bool
	s       *menuShown
	// button is set for a menu button's menu.
	button bool
	// level is the menu being built, 0 for the menu and 1 for a submenu
	// of it, and -1 outside them; built is what this pass built of each.
	// grouping is set while a Group builds its items.
	level    int
	built    []menuBuilt
	grouping bool
	// moved is set while the pointer moved since it last highlighted an
	// item; closed once the menu closes in this pass.
	moved, closed bool
}

// menuBuilt is a menu, or a submenu, built in a pass: its panel, its items
// in order, the submenus of those opening one (Submenu), and the groups of
// them (Group), each the place of its first item and of the one after its
// last. chosen is the item clicked, and first is set once the pass
// highlighted the first item that can be chosen (menuLevel.first).
type menuBuilt struct {
	panel  *node
	items  []*node
	subs   []submenu
	groups [][2]int
	chosen *node
	first  bool
}

// submenu is the submenu of item, which fn builds.
type submenu struct {
	item *node
	fn   func(panel *node)
}

// menuShown is what a menu drawn in the window keeps while it shows, as
// MenuBase's and PopoverMenu's: where it is, the menu and the submenus
// open, the level to give the focus to, and where the pointer was.
type menuShown struct {
	// below puts a menu button's menu below the button; the others open at
	// a point, dx and dy from the top left corner of their trigger's box.
	below  bool
	dx, dy float32
	// opened is the frame it opened in, whose presses do not close it, as
	// the press opening it.
	opened uint64
	// levels are the menu and its submenus that show, outermost first.
	levels []menuLevel
	// focus is the level to give the focus to once the menu is built, -1
	// for none.
	focus int
	// pointer is where the pointer was when it last moved the highlight:
	// it moves it again once it moves.
	pointer [2]float32
}

// menuLevel is a menu that shows: the item, by its element, whose submenu
// it is, 0 for the menu, and the item highlighted, 0 for none, or the
// first that can be chosen once built when first is set; it scrolls into
// view when reveal is set, as the keys moved to it.
type menuLevel struct {
	sub, hi       uint64
	first, reveal bool
}

type menuKey struct{}

// openAt opens the menu of e where the engine was asked to (askMenu): below
// it, or at the point it was right-clicked, on its first item when the
// keyboard opened it.
func (s *menuShown) openAt(rt *engine, e *node) {
	mr := &rt.menu
	// From the element's whole box, which the menu attaches to, not its
	// visible part: one scrolled partly out of view would put it off the
	// point by as much (#894).
	b := e.Bounds()
	*s = menuShown{below: mr.below, dx: mr.x - b.X, dy: mr.y - b.Y, opened: rt.frame,
		levels: append(s.levels[:0], menuLevel{first: mr.key}), pointer: [2]float32{rt.pointerX, rt.pointerY}}
}

// MenuBase creates a menu button without a look, whose menu shows while
// *open is true: a button that takes the focus and opens the menu as the
// pointer goes down on it, or for Enter, Space or Down while it has the
// focus, and closes it when pressed again. Give the trigger children, and
// build the menu in Popup with Item, CheckItem, RadioItem and
// SubmenuItem, and the submenus with Submenu; an item's Clicked reports
// its choice, which closes the menu:
//
//	m := ui.MenuBase(c, &app.menuOpen)
//	m.Trigger.Padding(6).Radius(6).Children(func() { ui.Icon(c, dots) })
//	m.Popup(func(panel ui.Element) {
//		panel.Padding(4).Radius(8).Background(t.Background).Border(1, t.Border)
//		item := m.Item().Padding(6, 10).Radius(4)
//		if item.Highlighted() {
//			item.Background(t.Accent).TextColor(t.AccentText)
//		}
//		item.Children(func() { ui.Text(c, "Duplicate") })
//		if item.Clicked() {
//			app.duplicate()
//		}
//	})
//
// The menu has the keys of PopoverMenu's: it takes the focus as it opens,
// on its first item when the keyboard opened it, and gives it back as it
// closes; Up and Down move the highlight among its items, past those
// disabled, as do Home, End and typing the first letters of an item;
// Right opens a submenu and Left closes it, the other way round right to
// left (Direction); Enter and Space choose an item, Escape closes the
// submenu on top, and Tab and a press outside the menu close it. The
// pointer highlights the item it moves onto, opening its submenu.
// Assistive technology sees a menu button, expanded while its menu shows,
// and a menu of items, check box items and radio items. PopoverMenuButton
// draws its menu with these parts, in the theme's look.
func coreMenuBase(c *context, open *bool) *menuParts {
	b := coreButtonBase(c)
	b.widget, b.role = "MenuBase", RoleMenuButton
	b.flags |= flagMenuButton
	m := newMenu(c, b, open, true)
	b.expanded = *open
	return m
}

// ContextMenuBase gives target a context menu without a look, which shows
// while *open is true, as MenuBase's: it opens where target is clicked
// with the secondary button, or Control-clicked on macOS, and, while
// target or an element inside it has the focus, below it when the menu
// key or Shift+F10 is pressed. Build it in Popup, as MenuBase's; it has
// the same keys, and assistive technology sees it so. PopoverMenu draws
// its menu with these parts, in the theme's look.
//
//	m := ui.ContextMenuBase(c, row, &app.rowMenu)
//	m.Popup(func(panel ui.Element) { ... })
func coreContextMenuBase(c *context, target *node, open *bool) *menuParts {
	target.flags |= flagContextMenu
	return newMenu(c, target, open, false)
}

// newMenu makes the parts of the menu of e, a menu button's when button
// is set, opening or closing it as the engine asks (askMenu).
func newMenu(c *context, e *node, open *bool, button bool) *menuParts {
	rt := c.rt
	if !e.hasState() {
		return &menuParts{Trigger: e, c: c, open: open, s: &menuShown{}, level: -1}
	}
	s := coreLocal(e, menuKey{}, func() menuShown { return menuShown{} })
	m := &menuParts{Trigger: e, c: c, open: open, s: s, button: button, level: -1}
	if mr := &rt.menu; mr.asked == e.id && mr.button == button {
		mr.asked = 0
		switch {
		case e.disabled():
		case *open && button && mr.below && s.below:
			// The button pressed again while its menu shows.
			*open = false
		default:
			*open = true
			s.openAt(rt, e)
		}
		rt.consumed = true
	}
	switch {
	case *open && e.disabled():
		*open = false
		fallthrough
	case !*open:
		s.levels = s.levels[:0]
	case len(s.levels) == 0:
		// The view opened it: below its trigger.
		*s = menuShown{below: true, opened: rt.frame, levels: append(s.levels, menuLevel{}), pointer: [2]float32{rt.pointerX, rt.pointerY}}
	}
	return m
}

// Open reports whether the menu shows.
func (m *menuParts) Open() bool { return *m.open }

// Popup shows the menu while it is open: below the trigger of a menu
// button, lined up with its start, or above it where the window has no
// room, and a context menu after where it opened, or before it, as from a
// corner. fn styles the panel, a column that scrolls the items past the
// room the window has, and builds the items in it. It returns the panel,
// or nil while the menu is closed.
func (m *menuParts) Popup(fn func(panel *node)) *node {
	s := m.s
	if !*m.open || len(s.levels) == 0 || m.level >= 0 || !m.Trigger.hasState() || m.c.inert {
		return nil
	}
	c, rt := m.c, m.c.rt
	m.moved = [2]float32{rt.pointerX, rt.pointerY} != s.pointer
	m.built = m.built[:0]
	var root *node
	coreOverlay(c, func() {
		// A context menu goes at the point where it opened, which follows
		// the element as it moves.
		anchor := m.Trigger
		if !s.below {
			anchor = coreBox(c).Key(mix(m.Trigger.id, 0)).Size(0, 0).AttachTo(m.Trigger, AnchorTopLeft, AnchorTopLeft).Margin(s.dy, 0, 0, s.dx)
		}
		root = m.panel(0, anchor, fn)
		for level := 1; level < len(s.levels) && !m.closed; level++ {
			subs := m.built[level-1].subs
			i := slices.IndexFunc(subs, func(sub submenu) bool { return sub.item.id == s.levels[level].sub })
			if i < 0 || subs[i].item.IsDisabled() {
				// The item is gone, or can no longer be chosen.
				s.levels = s.levels[:level]
				break
			}
			m.panel(level, subs[i].item, subs[i].fn)
		}
	})
	if m.closed {
		*m.open = false
		s.levels = s.levels[:0]
	} else if f := s.focus; f >= 0 && f < len(m.built) {
		// After the overlay noted the focus it came with, to give it back.
		m.built[f].panel.Focus()
		s.focus = -1
	}
	if m.button {
		m.Trigger.expanded = *m.open
	}
	return root
}

// panel builds the panel of the menu at level, attached to anchor, which fn
// styles and fills.
func (m *menuParts) panel(level int, anchor *node, fn func(panel *node)) *node {
	c, s := m.c, m.s
	panel := coreColumn(c).Key(mix(m.Trigger.id, uint64(level)+1)).Role(RoleMenu)
	panel.widget = "Menu"
	// Presses on it stay in it; items past the room the window has scroll.
	panel.flags |= flagFocusable | flagClickable | flagTypeSelect | flagOwnRing | flagScrollY | flagHover
	switch {
	case level == 0 && s.below:
		panel.AttachTo(anchor, AnchorBottomStart, AnchorTopStart)
	case level == 0:
		panel.AttachTo(anchor, AnchorTopStart, AnchorTopStart)
	default:
		// After the item opening it, its first item lined up with it.
		panel.AttachTo(anchor, AnchorTopEnd, AnchorTopStart)
	}
	m.built = append(m.built, menuBuilt{panel: panel})
	saved := m.level
	m.level = level
	panel.Children(func() { fn(panel) })
	m.level = saved
	if level > 0 && panel.label == "" {
		// Named by the item opening it.
		panel.Label(menuItemName(anchor))
	}
	m.finish(level)
	return panel
}

// Item creates an item of the menu being built in Popup or Submenu: a row
// that is Highlighted while the pointer or the keys are on it, and whose
// Clicked reports its choice, by the pointer, Enter or Space, which closes
// the menu, unless KeepOpen. Give it children; Disabled leaves it out of
// the keys' way and of the pointer's. Assistive technology sees a menu
// item.
func (m *menuParts) Item() *node { return m.item(RoleMenuItem, false) }

// CheckItem creates an item, as Item does, that assistive technology sees
// as a check box item, checked while checked is set: flip what it shows
// when it is Clicked.
func (m *menuParts) CheckItem(checked bool) *node {
	return m.item(RoleMenuItemCheckBox, false).Checked(checked)
}

// RadioItem creates an item, as Item does, that assistive technology sees
// as a radio item, the one chosen of its group while selected is set.
func (m *menuParts) RadioItem(selected bool) *node {
	return m.item(RoleMenuItemRadio, false).Checked(selected)
}

// SubmenuItem creates an item, as Item does, that opens a submenu instead
// of being chosen: as the pointer moves onto it or clicks it, and for
// Right, Enter or Space while it is highlighted. Build the submenu with
// Submenu. Assistive technology sees a menu item that expands, expanded
// while its submenu shows.
func (m *menuParts) SubmenuItem() *node { return m.item(RoleMenuItem, true) }

// Group creates a group of the items that fn builds in the menu being
// built, a row that fn styles: the keys take it as one item, Up and Down
// moving onto it at its item checked, else its first, and Left and Right
// move among its items, round from the last to the first, the other way
// round right to left (Direction), instead of opening and closing
// submenus. Its items show, are highlighted by the pointer and are chosen
// as the others are; a row of radio items that KeepOpen, as a theme's
// Light, Dark and System, switches without closing the menu:
//
//	m.Group(func(row ui.Element) {
//		row.Gap(4).Label("Theme")
//		for _, th := range []string{"Light", "Dark", "System"} {
//			item := m.RadioItem(app.theme == th).KeepOpen().Padding(4, 8)
//			item.Children(func() { ui.Text(c, th) })
//			if item.Clicked() {
//				app.theme = th
//			}
//		}
//	})
//
// Assistive technology sees a group, named by its Label, of the items in
// it, which it counts on their own. It returns the group.
func (m *menuParts) Group(fn func(group *node)) *node {
	g := coreRow(m.c).Role(RoleGroup)
	g.widget = "MenuGroup"
	if m.level < 0 || m.grouping {
		// Outside a menu, or inside a group, whose items it takes.
		g.Children(func() { fn(g) })
		return g
	}
	level, start := m.level, len(m.built[m.level].items)
	m.grouping = true
	g.Children(func() { fn(g) })
	m.grouping = false
	if b := &m.built[level]; len(b.items) > start {
		b.groups = append(b.groups, [2]int{start, len(b.items)})
	}
	return g
}

// KeepOpen keeps a menu open as its item is chosen (MenuBase,
// ContextMenuBase): by the pointer, Enter or Space, the item reports
// Clicked and stays highlighted, as a check box item flipping a setting
// one may flip again, or a radio item of a Group switching the theme
// while the menu shows it.
func (e *node) KeepOpen() *node {
	e.keepOpen = true
	return e
}

// Submenu builds the submenu of item, a SubmenuItem of the menu being
// built, while it shows: fn styles its panel, after the item and lined up
// with it, and builds its items, as Popup's.
func (m *menuParts) Submenu(item *node, fn func(panel *node)) {
	if m.level < 0 || item == nil || !item.expandable {
		return
	}
	b := &m.built[m.level]
	b.subs = append(b.subs, submenu{item, fn})
}

// item creates an item of the menu being built, with role, opening a
// submenu when sub is set.
func (m *menuParts) item(role Role, sub bool) *node {
	c := m.c
	row := coreRow(c).Role(role)
	row.flags |= flagClickable | flagHover
	if m.level < 0 {
		return row // outside a menu: an item of none
	}
	rt, s, level := c.rt, m.s, m.level
	b := &m.built[level]
	b.items = append(b.items, row)
	lv := &s.levels[level]
	if lv.first && !row.disabled() {
		lv.hi, lv.first, b.first = row.id, false, true
	}
	open := sub && level+1 < len(s.levels) && s.levels[level+1].sub == row.id
	if sub {
		row.expandable, row.expanded = true, open
	}
	row.highlighted = lv.hi == row.id
	if row.disabled() || m.closed {
		return row
	}
	if m.moved && row.Hovered() {
		// The pointer moved onto it: it is highlighted, and its submenu
		// opens while those of the others close. The keys go on from it.
		m.moved = false
		s.pointer = [2]float32{rt.pointerX, rt.pointerY}
		switch {
		case sub && !open:
			m.highlight(level, row.id)
			m.openSubmenu(level, row.id, false)
		case !sub && (lv.hi != row.id || len(s.levels) > level+1):
			m.highlight(level, row.id)
		}
		s.focus = level
	}
	if row.Clicked() {
		if sub {
			m.highlight(level, row.id)
			m.openSubmenu(level, row.id, false)
		} else {
			// The menu closes once its items are built (finish), unless
			// the item keeps it open (KeepOpen), which comes after.
			b.chosen = row
			rt.consumed = true
		}
	}
	return row
}

// finish ends the menu at level once its items are built: its highlight,
// the place of each item, and its keys.
func (m *menuParts) finish(level int) {
	rt, s := m.c.rt, m.s
	b := &m.built[level]
	panel, lv := b.panel, &s.levels[level]
	if b.chosen != nil && !b.chosen.keepOpen {
		m.closed = true
	}
	// Each item's group, -1 for none, and its place among the items of
	// its group, or among those of none.
	group := make([]int, len(b.items))
	loose := len(b.items)
	for i := range group {
		group[i] = -1
	}
	for g, r := range b.groups {
		for i := r[0]; i < r[1]; i++ {
			group[i] = g
			b.items[i].setPos, b.items[i].setSize = i-r[0]+1, r[1]-r[0]
		}
		loose -= r[1] - r[0]
	}
	var choices []*node // those the keys move among
	var groups []int    // the group of each
	var hi *node
	pos := 0
	for i, it := range b.items {
		if group[i] < 0 {
			pos++
			it.setPos, it.setSize = pos, loose
		}
		if !it.IsDisabled() {
			choices = append(choices, it)
			groups = append(groups, group[i])
		}
		if it.id == lv.hi {
			hi = it
		}
	}
	stops := menuStops(choices, groups)
	if hi != nil && hi.IsDisabled() || lv.first || b.first {
		// The first item to be chosen, which an item disabled after it was
		// built is not, and of a group its item checked; the items build
		// again unless it is the one highlighted as they were built.
		hi, lv.hi, lv.first = nil, 0, false
		if len(stops) > 0 {
			hi = choices[stops[0]]
			lv.hi = hi.id
		}
		if hi == nil || !hi.highlighted {
			rt.consumed = true
		}
	}
	if hi != nil {
		panel.activeDescendant = hi
		if lv.reveal {
			hi.ScrollIntoView()
		}
	}
	lv.reveal = false
	if !m.closed {
		m.keys(level, panel, choices, groups, stops)
	}
	if level == 0 && s.opened != rt.frame && panel.PressedOutside() {
		m.closed = true
	}
}

// menuItemName returns the name of a menu item: its Label, else its text.
func menuItemName(it *node) string {
	if it.label != "" {
		return it.label
	}
	return it.innerText()
}

// highlight highlights the item id of the menu at level, closing the
// submenus open in it.
func (m *menuParts) highlight(level int, id uint64) {
	s := m.s
	s.levels[level].hi = id
	s.levels = s.levels[:level+1]
	m.c.rt.consumed = true
}

// openSubmenu opens the submenu of item id of the menu at level; the
// keyboard opening it, it takes the focus on its first item.
func (m *menuParts) openSubmenu(level int, id uint64, keyboard bool) {
	s := m.s
	s.levels = append(s.levels[:level+1], menuLevel{sub: id, first: keyboard})
	if keyboard {
		s.focus = level + 1
	}
	m.c.rt.consumed = true
}

// closeLevel closes the submenu at level, giving the focus to the menu
// holding it.
func (m *menuParts) closeLevel(level int) {
	s := m.s
	s.levels = s.levels[:level]
	s.focus = level - 1
	m.c.rt.consumed = true
}

// menuStops returns the stops of the keys among choices, whose groups
// (Group) are in groups, -1 for none: the place in choices of each item
// in no group, and of each group's item checked, else its first.
func menuStops(choices []*node, groups []int) []int {
	var stops []int
	for i, it := range choices {
		switch g := groups[i]; {
		case g < 0 || i == 0 || groups[i-1] != g:
			stops = append(stops, i)
		case it.checked == 2 && choices[stops[len(stops)-1]].checked != 2:
			stops[len(stops)-1] = i
		}
	}
	return stops
}

// keys moves the highlight among choices, the items of the menu at level
// that can be chosen, opens and closes submenus and chooses items, for the
// keys pressed while the panel has the focus. groups are the groups of
// choices, and stops the places of those Up and Down stop at (menuStops).
func (m *menuParts) keys(level int, panel *node, choices []*node, groups, stops []int) {
	rt, s := m.c.rt, m.s
	n := len(choices)
	at := slices.IndexFunc(choices, func(it *node) bool { return it.id == s.levels[level].hi })
	move := func(i int) {
		if n > 0 {
			m.highlight(level, choices[(i%n+n)%n].id)
			s.levels[level].reveal = true
		}
	}
	// The stop of the item highlighted: its own, or its group's.
	stop := -1
	if at >= 0 {
		stop = slices.IndexFunc(stops, func(i int) bool { return i == at || groups[i] >= 0 && groups[i] == groups[at] })
	}
	ns := len(stops)
	moveStop := func(i int) {
		if ns > 0 {
			move(stops[(i%ns+ns)%ns])
		}
	}
	// Within the group of the item highlighted, by d, round.
	grouped := at >= 0 && groups[at] >= 0
	within := func(d int) {
		lo, hi := at, at+1
		for lo > 0 && groups[lo-1] == groups[at] {
			lo--
		}
		for hi < n && groups[hi] == groups[at] {
			hi++
		}
		k := hi - lo
		move(lo + ((at-lo+d)%k+k)%k)
	}
	into, out := KeyRight, KeyLeft
	if panel.rtl() {
		into, out = out, into
	}
	switch {
	case panel.Shortcut(0, KeyDown):
		moveStop(stop + 1) // from none, the first
	case panel.Shortcut(0, KeyUp):
		if stop < 0 {
			stop = 0
		}
		moveStop(stop - 1)
	case panel.Shortcut(0, KeyHome):
		moveStop(0)
	case panel.Shortcut(0, KeyEnd):
		moveStop(ns - 1)
	case grouped && panel.Shortcut(0, into):
		within(1)
	case grouped && panel.Shortcut(0, out):
		within(-1)
	case panel.Shortcut(0, into):
		if at >= 0 && choices[at].expandable {
			m.openSubmenu(level, choices[at].id, true)
		}
	case panel.Shortcut(0, out):
		if level > 0 {
			m.closeLevel(level)
		}
	case panel.Shortcut(0, KeyEnter), panel.Shortcut(0, KeySpace):
		switch {
		case at < 0:
		case choices[at].expandable:
			m.openSubmenu(level, choices[at].id, true)
		default:
			// The item reports Clicked in the next pass, which builds it
			// again, as a click of the pointer's.
			rt.clickLater = append(rt.clickLater, choices[at].id)
			rt.consumed = true
		}
	case panel.Shortcut(0, KeyTab), panel.Shortcut(Shift, KeyTab):
		m.closed = true
	}
	if st := panel.st; st.typing && st.typed != "" && n > 0 {
		// From the item after the one highlighted, round to it: the same
		// letter again goes to the next item starting with it.
		start := at
		if len([]rune(st.typed)) > 1 && at >= 0 {
			start = at - 1
		}
		typed := strings.ToLower(st.typed)
		for k := 1; k <= n; k++ {
			if strings.HasPrefix(strings.ToLower(menuItemName(choices[(start+k+n)%n])), typed) {
				move((start + k + n) % n)
				break
			}
		}
	}
	if panel.OverlayShortcut(0, KeyEscape) {
		if level == 0 {
			m.closed = true
		} else {
			m.closeLevel(level)
		}
	}
	if m.closed {
		rt.consumed = true
	}
}

// drawnMenu is a menu drawn in the window (PopoverMenu, PopoverMenuButton)
// that shows, of the element id: text is the text it opened on, built and
// pass the frame and the pass that last built it.
type drawnMenu struct {
	id    uint64
	text  uint64
	built uint64
	pass  int
	menu  menuShown
}

// openDrawnMenu opens the drawn menu of e, at the point asked for.
func (rt *engine) openDrawnMenu(e *node) {
	if !e.hasState() {
		return
	}
	d := &drawnMenu{id: e.id, text: rt.menu.text}
	d.menu.openAt(rt, e)
	rt.menu.drawn = d
}

// drawMenu builds the menu drawn for e while it shows, as MenuBase or
// ContextMenuBase with the theme's look: a panel for the menu and each
// submenu open, holding the items build adds.
func (rt *engine) drawMenu(e *node, build func(m *Menu)) {
	d := rt.menu.drawn
	if d == nil || d.id != e.id || !e.hasState() || e.c.inert {
		return
	}
	d.built, d.pass = rt.frame, rt.pass
	if e.disabled() {
		rt.menu.drawn = nil
		return
	}
	run := &menuRun{collect: true}
	pm := &platform.Menu{}
	build(&Menu{run: run, menu: pm, rt: rt, text: rt.states[d.text]})
	if !slices.ContainsFunc(pm.Items, func(it *platform.MenuItem) bool { return it.Visible && it.Type != platform.MenuItemSeparator }) {
		rt.menu.drawn = nil
		return
	}
	c := e.c
	t := c.theme
	open := true
	m := &menuParts{Trigger: e, c: c, open: &open, s: &d.menu, level: -1}
	var chosen *platform.MenuItem
	style := func(panel *node) {
		panel.widget = "PopoverMenu"
		panel.MinWidth(t.Space(45)).Padding(t.Space(1)).Radius(t.Radius+2).Background(t.Background).Border(1, t.Border)
		panel.Shadow(0, 6, 20, 0, RGBA(0, 0, 0, 0.18))
	}
	var items func(list []*platform.MenuItem)
	items = func(list []*platform.MenuItem) {
		marks := slices.ContainsFunc(list, func(it *platform.MenuItem) bool {
			return it.Type == platform.MenuItemCheckbox || it.Type == platform.MenuItemRadio
		})
		for _, it := range list {
			if !it.Visible {
				continue
			}
			if it.Type == platform.MenuItemSeparator {
				coreBox(c).Height(1).Margin(t.Space(1), t.Space(2)).Background(t.Border).Shrink(0)
				continue
			}
			row := m.drawnItem(it, marks)
			if sub := it.Submenu; it.Type == platform.MenuItemSubmenu && sub != nil {
				m.Submenu(row, func(panel *node) {
					style(panel)
					panel.Margin(-t.Space(1)-1, 0, 0, 0)
					items(sub.Items)
				})
			} else if row.Clicked() {
				chosen = it
			}
		}
	}
	m.Popup(func(panel *node) {
		style(panel)
		if d.menu.below {
			// Below the menu button, or above it where the window has no
			// room, lined up with its start.
			panel.Margin(t.Space(1), 0, 0, 0)
		}
		items(pm.Items)
	})
	if !open {
		rt.menu.drawn = nil
	} else if d.menu.below {
		e.expanded = true
	}
	if it := chosen; it != nil {
		if name := run.commands[it.ID-1]; name != "" {
			target := d.text
			if target == 0 {
				target = d.id
			}
			rt.menuCommand(target, name)
			rt.requestFrame()
		} else {
			rt.menuChoice(build, it.ID, it.Label, d.text)
		}
	}
}

// drawnItem builds item it of the menu being drawn, with room for the
// marks of check box and radio items when marks is set.
func (m *menuParts) drawnItem(it *platform.MenuItem, marks bool) *node {
	c := m.c
	t := c.theme
	sub := it.Type == platform.MenuItemSubmenu && it.Submenu != nil
	var row *node
	switch {
	case sub:
		row = m.SubmenuItem()
	case it.Type == platform.MenuItemCheckbox:
		row = m.CheckItem(it.Checked)
	case it.Type == platform.MenuItemRadio:
		row = m.RadioItem(it.Checked)
	default:
		row = m.Item()
	}
	row.AlignItems(Center).Gap(t.Space(2)).Padding(t.Space(1.5), t.Space(2)).Radius(t.Radius).Shrink(0)
	row.Label(it.Label).Disabled(!it.Enabled)
	keys := shortcutText(it.Accelerator)
	row.description = keys
	color, muted := t.Text, t.TextMuted
	switch {
	case !it.Enabled:
		color = t.TextMuted
		row.TextColor(color)
	case row.highlighted && row.expanded && m.s.levels[m.level+1].hi != 0:
		// The way to the item highlighted in the submenu.
		row.Background(t.SurfaceHover)
	case row.highlighted:
		color, muted = t.AccentText, t.AccentText
		row.Background(t.Accent).TextColor(color)
	}
	rtl := row.rtl()
	row.Children(func() {
		if marks {
			mark := coreBox(c).Size(t.Space(4), t.Space(4)).Shrink(0)
			if it.Checked {
				radio := it.Type == platform.MenuItemRadio
				mark.Draw(func(p *Painter, r Rect) {
					if radio {
						d := r.W / 4
						p.Fill(Rect{X: r.X + r.W/2 - d/2, Y: r.Y + r.H/2 - d/2, W: d, H: d}, color, d/2)
					} else {
						p.StrokePath(checkPath(r), 1.5, color)
					}
				})
			}
		}
		coreText(c, it.Label).SingleLine().Grow(1)
		if keys != "" {
			coreText(c, keys).SingleLine().TextColor(muted).MarginStart(t.Space(4))
		}
		if sub {
			coreBox(c).Size(t.Space(4), t.Space(4)).Shrink(0).Draw(func(p *Painter, r Rect) {
				p.StrokePath(disclosurePath(r, 0, rtl), 1.5, muted)
			})
		}
	})
	return row
}

// shortcutText writes an accelerator (accelerator) as a menu shows it by
// its item: with the symbols of macOS there, as "⇧⌘Z", and as "Ctrl+Shift+Z"
// elsewhere.
func shortcutText(a string) string {
	if a == "" {
		return ""
	}
	parts := strings.Split(a, "+")
	key, mods := parts[len(parts)-1], parts[:len(parts)-1]
	if len(key) == 1 {
		key = strings.ToUpper(key)
	}
	var b strings.Builder
	if runtime.GOOS == "darwin" {
		// In the order of macOS's menus.
		for _, m := range [...][2]string{{"Ctrl", "⌃"}, {"Alt", "⌥"}, {"Shift", "⇧"}, {"Super", "⌘"}} {
			if slices.Contains(mods, m[0]) {
				b.WriteString(m[1])
			}
		}
		switch key {
		case "Enter":
			key = "↩"
		case "Escape":
			key = "⎋"
		case "Backspace":
			key = "⌫"
		case "Delete":
			key = "⌦"
		case "Tab":
			key = "⇥"
		case "Left":
			key = "←"
		case "Right":
			key = "→"
		case "Up":
			key = "↑"
		case "Down":
			key = "↓"
		}
		b.WriteString(key)
		return b.String()
	}
	super := "Super"
	if runtime.GOOS == "windows" {
		super = "Win"
	}
	for _, m := range mods {
		if m == "Super" {
			m = super
		}
		b.WriteString(m)
		b.WriteByte('+')
	}
	b.WriteString(key)
	return b.String()
}
