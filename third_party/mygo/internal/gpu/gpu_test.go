package gpu

import (
	"bytes"
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/egoist/mygo/internal/gpu/gputest"
	"github.com/egoist/mygo/internal/scene"
)

func TestAffineInstancesPreserveGeometryAndWideColors(t *testing.T) {
	m := scene.Translation(30, 10).Mul(scene.Rotation(20))
	s := &scene.Scene{Width: 80, Height: 60, Clear: scene.Color{B: 120, A: 80}, Ops: []scene.Op{{Kind: scene.OpFill, Rect: scene.Rect{W: 25, H: 15}, Color: scene.Color{R: 255, A: 150}, Transform: m, Wide: 1}}, Wide: []scene.WideColors{{Set: scene.WideColor, Color: [4]float32{1.2, -0.1, 0.2, 0.8}}}}
	var b Builder
	b.Wide = true
	defer b.Release()
	uploads := 0
	shown := b.Build(s, func(*scene.Image) uintptr { uploads++; return 1 })
	if shown != s || uploads != 0 || len(b.Instances) != 1 {
		t.Fatal("transformed fill was rasterized or uploaded as an image")
	}
	in := b.Instances[0]
	if in.Params[0] != 0 || in.Rect != [4]float32{0, 0, 25, 15} || in.Color != s.Wide[0].Color {
		t.Fatal("local geometry or wide color was lost")
	}
	if in.Transform0 != [4]float32{m.A, m.C, m.X, 1} || in.Transform1 != [4]float32{m.B, m.D, m.Y, 0} {
		t.Fatal("vertex transform does not match scene geometry")
	}
	s.Ops[0].Transform = scene.Scaling(0, 1)
	b.Build(s, func(*scene.Image) uintptr { return 1 })
	if len(b.Instances) != 0 {
		t.Fatal("singular transform emitted GPU geometry")
	}
}

func TestAffineClipMaskCacheFollowsClipInsteadOfContent(t *testing.T) {
	s := &scene.Scene{Width: 100, Height: 80, Ops: []scene.Op{
		{Kind: scene.OpPushClip, Rect: scene.Rect{X: 10, Y: 10, W: 70, H: 50}, Radii: [4]float32{8, 8, 8, 8}},
		{Kind: scene.OpFill, Rect: scene.Rect{W: 25, H: 15}, Color: scene.Color{R: 255, A: 255}, Transform: scene.Translation(20, 15).Mul(scene.Rotation(10))},
		{Kind: scene.OpPopClip},
	}}
	var b Builder
	defer b.Release()
	var mask *scene.Image
	build := func() { b.Build(s, func(m *scene.Image) uintptr { mask = m; return 1 }) }
	build()
	if mask == nil || b.Batches[0].ClipMask != 1 || b.Instances[0].Transform1[3] != 1 {
		t.Fatal("nested clip was not bound as a mask")
	}
	id, version := mask.ID(), mask.Version()
	pixels := append([]byte(nil), mask.Pix...)
	s.Ops[1].Transform = scene.Translation(35, 25).Mul(scene.Rotation(10))
	build()
	if mask.ID() != id || mask.Version() != version || !bytes.Equal(mask.Pix, pixels) {
		t.Fatal("moving content rerasterized its stationary clip")
	}
	s.Ops[0].Radii = [4]float32{12, 12, 12, 12}
	build()
	if mask.ID() != id || mask.Version() == version {
		t.Fatal("changing clip did not update the retained mask")
	}
	s.Ops[0].Rect.W = 60
	build()
	if mask.ID() != id || mask.W != 60 {
		t.Fatal("resizing clip accumulated a new cached image")
	}
	clipped := s.Ops
	s.Ops = s.Ops[1:2]
	build()
	if len(b.maskSlots) != 1 {
		t.Fatal("a removed clip did not keep its mask")
	}
	version = mask.Version()
	s.Ops = clipped
	build()
	if mask.ID() != id || mask.Version() != version {
		t.Fatal("a clip coming back rasterized its mask again")
	}
}

func TestTransformComingAndGoingKeepsMasks(t *testing.T) {
	// A badge toggling between scaled and not, as in #255, beside a
	// rounded card holding a transformed clip of its own while it is.
	card := scene.Op{Kind: scene.OpPushClip, Rect: scene.Rect{X: 10, Y: 10, W: 200, H: 150}, Radii: [4]float32{12, 12, 12, 12}}
	inner := scene.Op{Kind: scene.OpPushClip, Rect: scene.Rect{W: 80, H: 40}, Transform: scene.Translation(30, 30).Mul(scene.Rotation(5))}
	fill := scene.Op{Kind: scene.OpFill, Rect: scene.Rect{W: 80, H: 40}, Color: scene.Color{A: 255}, Transform: inner.Transform}
	other := scene.Op{Kind: scene.OpPushClip, Rect: scene.Rect{X: 220, Y: 10, W: 60, H: 60}, Radii: [4]float32{6, 6, 6, 6}}
	pop := scene.Op{Kind: scene.OpPopClip}
	shown := &scene.Scene{Width: 300, Height: 200, Ops: []scene.Op{card, inner, fill, pop, pop}}
	elsewhere := &scene.Scene{Width: 300, Height: 200, Ops: []scene.Op{other, fill, pop}}
	hidden := &scene.Scene{Width: 300, Height: 200, Ops: []scene.Op{card, pop}}
	var b Builder
	defer b.Release()
	versions := map[uint64]uint64{}
	rasterized := 0
	build := func(s *scene.Scene) {
		b.Build(s, func(m *scene.Image) uintptr {
			if v, ok := versions[m.ID()]; !ok || v != m.Version() {
				rasterized++
				versions[m.ID()] = m.Version()
			}
			return uintptr(m.ID())
		})
	}
	build(shown)
	first := rasterized
	for range 5 {
		build(hidden)
		build(elsewhere)
		build(shown)
	}
	if first == 0 || rasterized != first+1 {
		t.Fatalf("rasterized %d masks after the first frame's %d, want only the other clip's", rasterized-first, first)
	}
}

func TestIdleMasksStayWithinBudget(t *testing.T) {
	// Two masks of 24 MiB each in a frame, then none: one idle mask fits
	// the budget, two don't.
	clip := func(x float32) []scene.Op {
		return []scene.Op{
			{Kind: scene.OpPushClip, Rect: scene.Rect{X: x, W: 3000, H: 2100}, Transform: scene.Rotation(0.01)},
			{Kind: scene.OpFill, Rect: scene.Rect{X: x, W: 10, H: 10}, Color: scene.Color{A: 255}},
			{Kind: scene.OpPopClip},
		}
	}
	var b Builder
	defer b.Release()
	build := func(s *scene.Scene) { b.Build(s, func(*scene.Image) uintptr { return 1 }) }
	build(&scene.Scene{Width: 6200, Height: 2100, Ops: append(clip(0), clip(3100)...)})
	if len(b.maskSlots) != 2 {
		t.Fatalf("kept %d masks, want 2", len(b.maskSlots))
	}
	build(&scene.Scene{Width: 10, Height: 10})
	if len(b.maskSlots) != 1 {
		t.Fatalf("kept %d idle masks, want 1 within the budget", len(b.maskSlots))
	}
}

func TestTransformOutsideAClipLeavesItUnmasked(t *testing.T) {
	// A scaled badge beside a rounded card of clipped rows: nothing in the
	// card is transformed, so its clips stay in the shader.
	s := &scene.Scene{Width: 400, Height: 300, Ops: []scene.Op{
		{Kind: scene.OpFill, Rect: scene.Rect{W: 40, H: 40}, Color: scene.Color{B: 255, A: 255}, Transform: scene.Scaling(1.2, 1.2)},
		{Kind: scene.OpPushClip, Rect: scene.Rect{Y: 50, W: 400, H: 250}, Radii: [4]float32{11, 11, 11, 11}},
		{Kind: scene.OpPushClip, Rect: scene.Rect{Y: 50, W: 200, H: 32}},
		{Kind: scene.OpFill, Rect: scene.Rect{Y: 50, W: 300, H: 32}, Color: scene.Color{A: 255}},
		{Kind: scene.OpPopClip},
		{Kind: scene.OpPopClip},
	}}
	var b Builder
	defer b.Release()
	masks := 0
	b.Build(s, func(*scene.Image) uintptr { masks++; return 1 })
	if masks != 0 || len(b.maskSlots) != 0 {
		t.Fatalf("rasterized %d clip masks for clips with no transform inside", masks)
	}
	if in := b.Instances[1]; in.Transform1[3] != 0 || in.Clip != [4]float32{0, 50, 200, 32} {
		t.Fatalf("row clip %v, mask mode %v", in.Clip, in.Transform1[3])
	}
}

func TestRoundedClipMasksOnlyTheBranchWithATransform(t *testing.T) {
	// A zoomed image beside a list in the same rounded card: the image's
	// clip keeps the card's corners, the list's rows stay in the shader.
	s := &scene.Scene{Width: 400, Height: 300, Ops: []scene.Op{
		{Kind: scene.OpPushClip, Rect: scene.Rect{W: 400, H: 300}, Radii: [4]float32{11, 11, 11, 11}},
		{Kind: scene.OpPushClip, Rect: scene.Rect{W: 200, H: 300}},
		{Kind: scene.OpFill, Rect: scene.Rect{W: 200, H: 300}, Color: scene.Color{A: 255}, Transform: scene.Scaling(1.5, 1.5)},
		{Kind: scene.OpPopClip},
		{Kind: scene.OpPushClip, Rect: scene.Rect{X: 200, W: 200, H: 32}},
		{Kind: scene.OpFill, Rect: scene.Rect{X: 200, W: 200, H: 32}, Color: scene.Color{A: 255}},
		{Kind: scene.OpPopClip},
		{Kind: scene.OpPopClip},
	}}
	var b Builder
	defer b.Release()
	b.Build(s, func(*scene.Image) uintptr { return 1 })
	if b.Instances[0].Transform1[3] != 1 {
		t.Fatal("zoomed content lost its rounded ancestor's mask")
	}
	if b.Instances[1].Transform1[3] != 0 || b.Batches[len(b.Batches)-1].ClipMask != 0 {
		t.Fatal("untransformed row was masked")
	}
}

func TestTranslatedContentLeavesRoundedClipsUnmasked(t *testing.T) {
	// #1002: labels moved by a fraction of a pixel, which glyphs are not
	// folded by, in the clipped panes of a rounded frame. Moved, they sit
	// elsewhere in the clips, which the shader cuts in the frame's
	// coordinates as for labels that are not; turned, they keep the
	// frame's corners, with masks.
	scene.FoldTranslations() // as package ui does
	atlas := scene.NewAtlas(1, 16, 16)
	build := func(m scene.Affine) (*Builder, int) {
		s := &scene.Scene{Width: 1280, Height: 800, MaskAtlas: atlas, Ops: []scene.Op{
			{Kind: scene.OpPushClip, Rect: scene.Rect{X: 18, Y: 18, W: 1244, H: 764}, Radii: scene.Uniform(14)},
		}}
		for i, x := range []float32{18, 458, 982} {
			start := int32(len(s.Glyphs))
			s.Glyphs = append(s.Glyphs, scene.Glyph{X: x + 16, Y: 34, W: 8, H: 12, UW: 8, VH: 12, Color: scene.Color{A: 255}},
				scene.Glyph{X: x + 24.5, Y: 34, W: 8, H: 12, UW: 8, VH: 12, Color: scene.Color{A: 255}})
			s.Ops = append(s.Ops,
				scene.Op{Kind: scene.OpPushClip, Rect: scene.Rect{X: x, Y: 18, W: []float32{440, 524, 280}[i], H: 764}},
				scene.Op{Kind: scene.OpGlyphs, Start: start, End: int32(len(s.Glyphs)), Transform: m},
				scene.Op{Kind: scene.OpPopClip})
		}
		s.Ops = append(s.Ops, scene.Op{Kind: scene.OpPopClip})
		b := &Builder{}
		masks := 0
		b.Build(s, func(*scene.Image) uintptr { masks++; return 1 })
		return b, masks
	}
	for _, m := range []scene.Affine{scene.Translation(0, -2.54), scene.Translation(0.5, 0.25)} {
		b, masks := build(m)
		if masks != 0 || len(b.maskSlots) != 0 {
			t.Errorf("translation %v: rasterized %d clip masks", m, masks)
		}
		for i, in := range b.Instances {
			if in.Transform1[3] != 0 || in.Transform0[3] != 1 || in.Transform1[2] != m.Y {
				t.Errorf("translation %v: glyph %d clip mode %v, transform %v %v", m, i, in.Transform1[3], in.Transform0, in.Transform1)
			}
		}
		b.Release()
	}
	b, masks := build(scene.Rotation(3))
	defer b.Release()
	if masks == 0 || len(b.Instances) == 0 || b.Instances[0].Transform1[3] != 1 {
		t.Fatalf("turned glyphs lost their rounded ancestor's mask: %d masks", masks)
	}
}

// shadowFragments is the area, in pixels, the scissor rectangles of the
// batches of the shadows (kind 1) leave to the renderer to shade.
func shadowFragments(b *Builder) (area int64, instances int) {
	for _, bt := range b.Batches {
		for _, in := range b.Instances[bt.Start : bt.Start+bt.Count] {
			if in.Params[0] == 1 {
				area += bt.Scissor.area()
				instances++
			}
		}
	}
	return
}

// TestShadowSplitShrinksTheShaded checks that a large shadow cast by a box is
// drawn around that box: the scissor rectangles of its pieces tile the window
// less the core of the box, so no fragment of the core is shaded, and a small
// shadow stays one instance.
func TestShadowSplitShrinksTheShaded(t *testing.T) {
	for i, c := range gputest.ShadowCases() {
		s := gputest.ShadowPanelScene(c)
		var split, whole Builder
		whole.NoShadowSplit = true
		split.Build(s, nil)
		whole.Build(s, nil)
		wa, wn := shadowFragments(&whole)
		sa, sn := shadowFragments(&split)
		if wn != len(c.Blur) {
			t.Fatalf("case %d: %d shadow instances without the split, want %d", i, wn, len(c.Blur))
		}
		if sn <= wn || sa >= wa {
			t.Errorf("case %d: split shades %d px in %d instances, whole %d px in %d", i, sa, sn, wa, wn)
		}
		// The pieces of one shadow are disjoint and stay out of the core.
		core := shadowCore(scene.Rect{X: 70, Y: 60, W: 340, H: 220}, [4]float32{c.Radius, c.Radius, c.Radius, c.Radius}, c.Continuous)
		var pieces []Scissor
		for _, bt := range split.Batches {
			for _, in := range split.Instances[bt.Start : bt.Start+bt.Count] {
				if in.Params[0] == 1 {
					pieces = append(pieces, bt.Scissor)
				}
			}
		}
		for x, p := range pieces {
			if p != (Scissor{0, 0, int32(s.Width), int32(s.Height)}) && p.intersect(core).area() != 0 { // whole: not split
				t.Errorf("case %d: piece %d %v reaches the core %v", i, x, p, core)
			}
		}
	}
	// Small shadows, and shadows without a caster, are one instance.
	small := &scene.Scene{Width: 200, Height: 100, Ops: []scene.Op{
		{Kind: scene.OpShadow, Rect: scene.Rect{X: 20, Y: 20, W: 40, H: 30}, Color: scene.Color{A: 80}, Blur: 8,
			Cast: scene.Rect{X: 20, Y: 20, W: 40, H: 30}},
		{Kind: scene.OpShadow, Rect: scene.Rect{X: 20, Y: 20, W: 150, H: 80}, Color: scene.Color{A: 80}, Blur: 8},
	}}
	var b Builder
	b.Build(small, nil)
	if len(b.Instances) != 2 || len(b.Batches) != 1 {
		t.Errorf("small shadows: %d instances in %d batches", len(b.Instances), len(b.Batches))
	}
}

// TestCullLeavesOutCoveredOps checks how many ops the builder leaves out of
// scenes where opaque fills cover others, and that it leaves none out when
// told not to.
func TestCullLeavesOutCoveredOps(t *testing.T) {
	for _, c := range gputest.CullCases() {
		var culled, whole Builder
		whole.NoCull = true
		tex := func(*scene.Image) uintptr { return 1 }
		culled.Build(c.Scene, tex)
		whole.Build(c.Scene, tex)
		if whole.Culled != 0 {
			t.Errorf("%s: %d ops left out with NoCull", c.Name, whole.Culled)
		}
		if culled.Culled != c.Culled {
			t.Errorf("%s: %d ops left out, want %d", c.Name, culled.Culled, c.Culled)
		}
		if c.Culled == 0 && len(culled.Instances) != len(whole.Instances) {
			t.Errorf("%s: %d instances, want %d", c.Name, len(culled.Instances), len(whole.Instances))
		}
		if c.Culled > 0 && len(culled.Instances) >= len(whole.Instances) {
			t.Errorf("%s: %d instances, not fewer than %d", c.Name, len(culled.Instances), len(whole.Instances))
		}
	}
}

// TestCullPlanIsBounded checks that a scene of many fills, none of them
// hidden by a bounded number of occluders, is planned in a linear number of
// steps (here: it only has to finish quickly and leave out what is hidden).
func TestCullPlanIsBounded(t *testing.T) {
	s := &scene.Scene{Width: 4000, Height: 4000}
	for i := range 20000 {
		x := float32(i%100) * 40
		y := float32(i/100) * 20
		s.Ops = append(s.Ops, scene.Op{Kind: scene.OpFill, Rect: scene.Rect{X: x, Y: y, W: 30, H: 15}, Color: scene.Color{R: 1, A: 255}})
	}
	// One panel over the first cells.
	s.Ops = append(s.Ops, scene.Op{Kind: scene.OpFill, Rect: scene.Rect{X: 0, Y: 0, W: 400, H: 200}, Color: scene.Color{A: 255}})
	var b Builder
	b.Build(s, nil)
	if b.Culled != 100 { // 10 columns by 10 rows of cells lie inside the panel's core
		t.Errorf("%d ops left out, want 100", b.Culled)
	}
}

// ringFragments returns the area of the scissor rectangles of the
// border-only fills with a solid color that b built, and how many there are.
func ringFragments(b *Builder) (area int64, n int) {
	for _, bt := range b.Batches {
		for _, in := range b.Instances[bt.Start : bt.Start+bt.Count] {
			if in.Params[0] == 0 && in.Params[2] == 0 && in.Color[3] == 0 && scene.HasBorder(in.UV) {
				area += bt.Scissor.area()
				n++
			}
		}
	}
	return area, n
}

// TestRingSplitShrinksTheShaded checks that a border-only fill the size of a
// panel is drawn in pieces that shade less than it drawn whole, and that
// other fills are not split.
func TestRingSplitShrinksTheShaded(t *testing.T) {
	for _, c := range gputest.RingCases() {
		var split, whole Builder
		whole.NoRingSplit = true
		split.NoCull, whole.NoCull = true, true
		tex := func(*scene.Image) uintptr { return 1 }
		split.Build(c.Scene, tex)
		whole.Build(c.Scene, tex)
		sa, sn := ringFragments(&split)
		wa, wn := ringFragments(&whole)
		if !c.Split {
			if len(split.Instances) != len(whole.Instances) {
				t.Errorf("%s: %d instances, want %d (not split)", c.Name, len(split.Instances), len(whole.Instances))
			}
			continue
		}
		if wn == 0 || sa >= wa {
			t.Errorf("%s: split shades %d px in %d instances, whole %d px in %d", c.Name, sa, sn, wa, wn)
		}
		t.Logf("%s: split shades %d px in %d instances, whole %d px in %d", c.Name, sa, sn, wa, wn)
	}
	// The pieces of the rounded case's ring lie outside its core, which the
	// band across its middle leaves out.
	c := gputest.RingCases()[2] // rounded
	var b Builder
	b.Build(c.Scene, nil)
	core := ringCore(scene.Rect{X: 20, Y: 20, W: 440, H: 320}, [4]float32{16, 16, 16, 16}, scene.Uniform(2), false)
	for _, bt := range b.Batches {
		for _, in := range b.Instances[bt.Start : bt.Start+bt.Count] {
			if in.Params[0] == 0 && in.Color[3] == 0 && scene.HasBorder(in.UV) && bt.Scissor.intersect(core).area() != 0 {
				t.Errorf("piece %v reaches the core %v", bt.Scissor, core)
			}
		}
	}
}

// sdRound is the shaders' sdRoundRect: the distance from (x, y) to the edge
// of the rectangle r with circular corners of radii, negative inside.
func sdRound(x, y float32, r scene.Rect, radii [4]float32) float32 {
	hx, hy := r.W/2, r.H/2
	qx, qy := x-r.X-hx, y-r.Y-hy
	rad := radii[3]
	switch {
	case qx < 0 && qy < 0:
		rad = radii[0]
	case qy < 0:
		rad = radii[1]
	case qx >= 0:
		rad = radii[2]
	}
	ax, ay := abs32(qx)-hx+rad, abs32(qy)-hy+rad
	return float32(math.Hypot(float64(max(ax, 0)), float64(max(ay, 0)))) + min(max(ax, ay), 0) - rad
}

func coverageOf(d float32) float32 { return min(max(0.5-d, 0), 1) }

// TestBandsCoverWhatDraws checks, pixel by pixel, that the pieces of each
// ring and shadow with round corners the builder splits are apart from each
// other and hold every pixel where it may draw anything, as the shaders
// compute it a pixel nearer to drawing: a ring between its edges and a
// shadow outside the box casting it, within its clip's scissor rectangle
// and its quad.
func TestBandsCoverWhatDraws(t *testing.T) {
	var cases []gputest.RingCase
	for _, c := range gputest.RingCases() {
		if c.Split {
			cases = append(cases, c)
		}
	}
	for i, c := range gputest.ShadowCases() {
		if !c.Continuous {
			cases = append(cases, gputest.RingCase{Name: fmt.Sprint("shadow case ", i), Scene: gputest.ShadowPanelScene(c)})
		}
	}
	for _, c := range cases {
		var split, whole Builder
		whole.NoRingSplit, whole.NoShadowSplit = true, true
		split.NoCull, whole.NoCull = true, true
		split.Build(c.Scene, nil)
		whole.Build(c.Scene, nil)
		type shape struct {
			in     Instance
			region Scissor // its quad within its clip's scissor rectangle
			pieces []Scissor
		}
		var shapes []*shape
		find := func(in Instance) *shape {
			for _, sh := range shapes {
				if sh.in.Rect == in.Rect && sh.in.UV == in.UV && sh.in.Params == in.Params {
					return sh
				}
			}
			return nil
		}
		ringOrShadow := func(in Instance) bool {
			ring := in.Params[0] == 0 && in.Color[3] == 0 && scene.HasBorder(in.UV)
			shadow := in.Params[0] == 1 && in.Params[1] == 0 && in.UV[2] > 0
			return (ring || shadow) && !continuous(in.Radii) && !continuous(in.Inner)
		}
		for _, bt := range whole.Batches {
			for _, in := range whole.Instances[bt.Start : bt.Start+bt.Count] {
				if !ringOrShadow(in) {
					continue
				}
				e := float32(1)
				if in.Params[0] == 1 {
					e += 3 * in.Params[2]
				}
				quad := Scissor{int32(math.Floor(float64(in.Rect[0] - e))), int32(math.Floor(float64(in.Rect[1] - e))),
					int32(math.Ceil(float64(in.Rect[0] + in.Rect[2] + e))), int32(math.Ceil(float64(in.Rect[1] + in.Rect[3] + e)))}
				shapes = append(shapes, &shape{in: in, region: quad.intersect(bt.Scissor)})
			}
		}
		for _, bt := range split.Batches {
			for _, in := range split.Instances[bt.Start : bt.Start+bt.Count] {
				if sh := find(in); sh != nil && ringOrShadow(in) {
					sh.pieces = append(sh.pieces, bt.Scissor)
				}
			}
		}
		for _, sh := range shapes {
			in := sh.in
			for i, p := range sh.pieces {
				for _, o := range sh.pieces[i+1:] {
					if p.intersect(o).area() != 0 {
						t.Errorf("%s: pieces %v and %v of %v overlap", c.Name, p, o, in.Rect)
					}
				}
			}
			r := scene.Rect{X: in.Rect[0], Y: in.Rect[1], W: in.Rect[2], H: in.Rect[3]}
			missed := 0
			for y := sh.region.Top; y < sh.region.Bottom; y++ {
				for x := sh.region.Left; x < sh.region.Right; x++ {
					px, py := float32(x)+0.5, float32(y)+0.5
					var draws bool
					if in.Params[0] == 1 {
						cast := scene.Rect{X: in.UV[0], Y: in.UV[1], W: in.UV[2], H: in.UV[3]}
						draws = coverageOf(sdRound(px, py, cast, in.Inner)+1) < 1
					} else {
						bw := in.UV
						ir := scene.Rect{X: r.X + bw[3], Y: r.Y + bw[0], W: r.W - bw[1] - bw[3], H: r.H - bw[0] - bw[2]}
						inner := float32(0)
						if ir.W > 0 && ir.H > 0 {
							inner = coverageOf(sdRound(px, py, ir, in.Inner) + 1)
						}
						draws = coverageOf(sdRound(px, py, r, in.Radii)-1)-inner > 0
					}
					if !draws {
						continue
					}
					held := false
					for _, p := range sh.pieces {
						if x >= p.Left && x < p.Right && y >= p.Top && y < p.Bottom {
							held = true
							break
						}
					}
					if !held {
						if missed < 3 {
							t.Errorf("%s: pixel %d,%d of %v is in no piece", c.Name, x, y, in.Rect)
						}
						missed++
					}
				}
			}
		}
	}
}

// TestOpaqueCores checks which fills the builder draws as opaque cores with
// blended edges around them: a layer as large as the frame is one opaque
// instance without edges, a card has a core and four edges that tile the
// frame less it, and small, translucent and gradient fills stay blended.
func TestOpaqueCores(t *testing.T) {
	white := scene.Color{R: 255, G: 255, B: 255, A: 255}
	s := &scene.Scene{Width: 800, Height: 600, Ops: []scene.Op{
		{Kind: scene.OpFill, Rect: scene.Rect{W: 800, H: 600}, Color: white},
		{Kind: scene.OpFill, Rect: scene.Rect{X: 100, Y: 100, W: 600, H: 400}, Radii: [4]float32{16, 16, 16, 16}, Color: white},
		{Kind: scene.OpFill, Rect: scene.Rect{X: 10, Y: 10, W: 50, H: 50}, Color: white},
		{Kind: scene.OpFill, Rect: scene.Rect{X: 10, Y: 10, W: 500, H: 500}, Color: scene.Color{R: 1, A: 254}},
	}}
	var b Builder
	b.NoCull, b.Opaque = true, true
	b.Build(s, nil)
	if b.OpaqueCores != 2 {
		t.Fatalf("%d opaque cores, want 2", b.OpaqueCores)
	}
	var core, blended int64
	for _, bt := range b.Batches {
		if bt.Opaque {
			if bt.Count != 1 {
				t.Errorf("an opaque batch has %d instances", bt.Count)
			}
			core += bt.Scissor.area()
		} else {
			blended += bt.Scissor.area() * int64(bt.Count)
		}
	}
	card := shadowCore(scene.Rect{X: 100, Y: 100, W: 600, H: 400}, [4]float32{16, 16, 16, 16}, false)
	if want := int64(800*600) + card.area(); core != want {
		t.Errorf("opaque cores cover %d px, want %d", core, want)
	}
	// Blended: the card's four edges tile the frame less its core, and the
	// small and the translucent fill are one instance each, scissored to the frame.
	if want := int64(800*600) - card.area() + 2*800*600; blended != want {
		t.Errorf("blended scissor area %d, want %d", blended, want)
	}
	b.Opaque = false
	b.Build(s, nil)
	for _, bt := range b.Batches {
		if bt.Opaque {
			t.Error("an opaque batch without Opaque")
		}
	}
}

// TestBatchClasses checks the class of each batch: plain fills (with and
// without continuous corners, in one batch), other fills, shadows, glyphs and
// images, and that transformed instances take the general shader.
func TestBatchClasses(t *testing.T) {
	red := scene.Color{R: 255, A: 255}
	atlas := scene.NewAtlas(1, 64, 32)
	img := scene.NewImageRGBA(2, 2, make([]byte, 16))
	s := &scene.Scene{Width: 200, Height: 100, MaskAtlas: atlas}
	s.Glyphs = []scene.Glyph{{X: 10, Y: 20, W: 8, H: 9, U: 16, V: 8, UW: 8, VH: 9, Color: red}}
	r := func(x float32) scene.Rect { return scene.Rect{X: x, Y: 10, W: 20, H: 20} }
	s.Ops = []scene.Op{
		{Kind: scene.OpFill, Rect: r(0), Color: red},
		{Kind: scene.OpFill, Rect: r(30), Radii: [4]float32{4, 4, 4, 4}, Continuous: true, Color: red},
		{Kind: scene.OpFill, Rect: r(60), Radii: [4]float32{4, 4, 4, 4}, Color: red},
		{Kind: scene.OpFill, Rect: r(90), Color: red, Border: scene.Uniform(1), BorderColor: red},
		{Kind: scene.OpShadow, Rect: r(0), Color: red, Blur: 8},
		{Kind: scene.OpGlyphs, Start: 0, End: 1},
		{Kind: scene.OpImage, Rect: r(0), Image: img, Src: scene.Rect{W: 2, H: 2}},
		{Kind: scene.OpFill, Rect: r(120), Color: red, Transform: scene.Translation(1, 1).Mul(scene.Scaling(1.01, 1))},
	}
	b := Builder{Classes: true, NoJoin: true}
	b.Build(s, func(*scene.Image) uintptr { return 1 })
	want := []Class{ClassPlain, ClassFill, ClassShadow, ClassGlyph, ClassImage, ClassGeneral}
	if len(b.Batches) != len(want) {
		t.Fatalf("%d batches: %+v", len(b.Batches), b.Batches)
	}
	for i, c := range want {
		if b.Batches[i].Class != c {
			t.Errorf("batch %d: class %d, want %d", i, b.Batches[i].Class, c)
		}
	}
	if b.Batches[0].Count != 3 {
		t.Errorf("the plain fills share a batch of %d", b.Batches[0].Count)
	}
	b.NoClasses = true
	b.Build(s, func(*scene.Image) uintptr { return 1 })
	for i, bt := range b.Batches {
		if bt.Class != ClassGeneral {
			t.Errorf("NoClasses: batch %d has class %d", i, bt.Class)
		}
	}
}

// TestJoinBatches checks that runs of small batches of other classes join
// into one batch holding their classes, that batches covering many pixels
// keep theirs, and that batches drawing with other state stay apart.
func TestJoinBatches(t *testing.T) {
	red := scene.Color{R: 255, A: 255}
	box := func(x, y float32) scene.Rect { return scene.Rect{X: x, Y: y, W: 20, H: 20} }
	s := &scene.Scene{Width: 400, Height: 300}
	for i := range 4 {
		x := float32(i * 30)
		s.Ops = append(s.Ops, scene.Op{Kind: scene.OpShadow, Rect: box(x, 10), Color: scene.Color{A: 60}, Blur: 4},
			scene.Op{Kind: scene.OpFill, Rect: box(x, 10), Radii: scene.Uniform(4), Color: red})
	}
	// A large fill keeps its class, the fill of the last box sharing its
	// batch, and the small batches after it join.
	s.Ops = append(s.Ops, scene.Op{Kind: scene.OpFill, Rect: scene.Rect{X: 0, Y: 50, W: 300, H: 200}, Color: scene.Color{G: 200, A: 200}},
		scene.Op{Kind: scene.OpFill, Rect: box(10, 60), Color: red, Border: scene.Uniform(1), BorderColor: red},
		scene.Op{Kind: scene.OpShadow, Rect: box(40, 60), Color: scene.Color{A: 60}, Blur: 4},
		// A clip: another scissor rectangle.
		scene.Op{Kind: scene.OpPushClip, Rect: scene.Rect{X: 100, Y: 100, W: 50, H: 50}},
		scene.Op{Kind: scene.OpFill, Rect: box(110, 110), Color: red},
		scene.Op{Kind: scene.OpPopClip},
		// A transformed fill joins as the general class: every path.
		scene.Op{Kind: scene.OpFill, Rect: box(200, 60), Color: red},
		scene.Op{Kind: scene.OpFill, Rect: box(230, 60), Color: red, Transform: scene.Rotation(0.1)})
	b := Builder{Classes: true}
	b.Build(s, func(*scene.Image) uintptr { return 1 })
	type got struct {
		count  int
		class  Class
		joined uint32
	}
	want := []got{{7, ClassGeneral, 1<<ClassShadow | 1<<ClassPlain}, {2, ClassPlain, 0}, {2, ClassGeneral, 1<<ClassFill | 1<<ClassShadow},
		{1, ClassPlain, 0}, {2, ClassGeneral, 0}}
	var have []got
	for _, bt := range b.Batches {
		have = append(have, got{bt.Count, bt.Class, bt.Joined})
	}
	if !slices.Equal(have, want) {
		t.Errorf("batches %+v, want %+v", have, want)
	}
	for _, off := range []func(*Builder){func(b *Builder) { b.NoJoin = true }, func(b *Builder) { b.NoClasses = true }, func(b *Builder) { b.Classes = false }} {
		b := Builder{Classes: true}
		off(&b)
		b.Build(s, func(*scene.Image) uintptr { return 1 })
		for _, bt := range b.Batches {
			if bt.Joined != 0 {
				t.Errorf("a batch joined classes: %+v", bt)
			}
		}
	}
}

// TestCullKeepsTransformedShadows checks that the planner does not leave out a
// shadow under a transform whose blur reaches out of the cover over it: the blur
// grows with the scale, so a shadow of blur 20 at scale 4 reaches about 120
// device pixels from its box, and a cover of the box and 100 pixels around it
// does not hold it. A cover of the frame does.
func TestCullKeepsTransformedShadows(t *testing.T) {
	shadow := scene.Op{Kind: scene.OpShadow, Rect: scene.Rect{X: 50, Y: 50, W: 40, H: 40}, Color: scene.Color{A: 255}, Blur: 20, Transform: scene.Scaling(4, 4)}
	cover := func(r scene.Rect) scene.Op {
		return scene.Op{Kind: scene.OpFill, Rect: r, Color: scene.Color{R: 1, A: 255}}
	}
	for _, c := range []struct {
		name   string
		cover  scene.Rect
		culled int
	}{
		{"cover of the box and 100 px", scene.Rect{X: 154, Y: 154, W: 252, H: 252}, 0},
		{"cover of the frame", scene.Rect{W: 640, H: 640}, 1},
	} {
		var b Builder
		b.Build(&scene.Scene{Width: 640, Height: 640, Ops: []scene.Op{shadow, cover(c.cover)}}, nil)
		if b.Culled != c.culled {
			t.Errorf("%s: %d ops left out, want %d", c.name, b.Culled, c.culled)
		}
	}
}

// TestFillsInsideTheirClipSkipItsCoverage checks which fills the builder
// tells the shaders need no clip coverage: those not transformed whose quad
// lies well inside a clip that is a plain rectangle.
func TestFillsInsideTheirClipSkipItsCoverage(t *testing.T) {
	s := &scene.Scene{Width: 200, Height: 200}
	fill := func(r scene.Rect) {
		s.Ops = append(s.Ops, scene.Op{Kind: scene.OpFill, Rect: r, Color: scene.Color{A: 255}})
	}
	fill(scene.Rect{X: 10, Y: 10, W: 50, H: 50}) // 0: inside the root clip
	fill(scene.Rect{X: 0, Y: 0, W: 200, H: 200}) // 1: the frame
	s.Ops = append(s.Ops, scene.Op{Kind: scene.OpPushClip, Rect: scene.Rect{X: 20, Y: 20, W: 100, H: 100}})
	fill(scene.Rect{X: 40, Y: 40, W: 20, H: 20}) // 2: inside the clip
	fill(scene.Rect{X: 19, Y: 40, W: 20, H: 20}) // 3: cut by it
	fill(scene.Rect{X: 21, Y: 40, W: 20, H: 20}) // 4: a pixel from its edge, within the antialiasing's reach
	s.Ops = append(s.Ops, scene.Op{Kind: scene.OpPopClip})
	s.Ops = append(s.Ops, scene.Op{Kind: scene.OpPushClip, Rect: scene.Rect{X: 20, Y: 20, W: 100, H: 100}, Radii: [4]float32{8, 8, 8, 8}})
	fill(scene.Rect{X: 50, Y: 50, W: 10, H: 10}) // 5: under a rounded clip
	s.Ops = append(s.Ops, scene.Op{Kind: scene.OpPopClip})
	s.Ops = append(s.Ops, scene.Op{Kind: scene.OpFill, Rect: scene.Rect{X: 10, Y: 10, W: 50, H: 50}, Color: scene.Color{A: 255}, Transform: scene.Translation(3, 3).Mul(scene.Scaling(1.01, 1))}) // 6
	want := map[int]bool{0: true, 1: true, 2: true}                                                                                                                                               // 1, the frame, lies in the root clip, which is unbounded
	tex := func(*scene.Image) uintptr { return 1 }
	var b Builder
	b.NoCull = true
	b.ClipSkip = true
	b.Build(s, tex)
	if len(b.Instances) != 7 {
		t.Fatalf("%d instances", len(b.Instances))
	}
	for k, in := range b.Instances {
		if got := in.Transform1[3] == 3; got != want[k] {
			t.Errorf("fill %d: no clip coverage is %v, want %v", k, got, want[k])
		}
	}
	b.NoClipSkip = true
	b.Build(s, tex)
	for k, in := range b.Instances {
		if in.Transform1[3] == 3 {
			t.Errorf("instance %d skips the clip with NoClipSkip", k)
		}
	}
}

// Renderers that have not opted in retain the general shader contract.
func TestShaderCapabilitiesAreOptIn(t *testing.T) {
	s := &scene.Scene{Width: 100, Height: 100, Ops: []scene.Op{
		{Kind: scene.OpFill, Rect: scene.Rect{X: 10, Y: 10, W: 20, H: 20}, Color: scene.Color{A: 255}},
		{Kind: scene.OpShadow, Rect: scene.Rect{X: 50, Y: 50, W: 20, H: 20}, Color: scene.Color{A: 128}, Blur: 4},
	}}
	var b Builder
	b.Build(s, nil)
	if len(b.Batches) != 1 || b.Batches[0].Class != ClassGeneral {
		t.Fatalf("default builder split shader classes: %+v", b.Batches)
	}
	for _, in := range b.Instances {
		if in.Transform1[3] == 3 {
			t.Fatal("default builder emitted a Metal-only clip flag")
		}
	}
}

func TestCullDoesNotPlanSpriteOnlyScenes(t *testing.T) {
	var b Builder
	s := gputest.SpriteScene(1920, 1080, 1.5, 3, 1000)
	b.Build(s, func(*scene.Image) uintptr { return 1 })
	if b.Culled != 0 || len(b.Instances) != len(s.Ops) {
		t.Fatalf("sprite-only scene: %d culled, %d instances for %d ops", b.Culled, len(b.Instances), len(s.Ops))
	}
	if cap(b.plan) != 0 {
		t.Fatalf("sprite-only scene allocated %d planning records", cap(b.plan))
	}
	// A later covered frame must still plan, and returning to sprites must
	// clear old culling decisions even though its planning pass is skipped.
	covered := &scene.Scene{Width: 1920, Height: 1080, Ops: []scene.Op{
		{Kind: scene.OpFill, Rect: scene.Rect{W: 1920, H: 1080}, Color: scene.Color{A: 255}},
		{Kind: scene.OpFill, Rect: scene.Rect{W: 1920, H: 1080}, Color: scene.Color{R: 100, A: 255}},
	}}
	b.Build(covered, nil)
	if b.Culled != 1 {
		t.Fatalf("covered frame culled %d ops", b.Culled)
	}
	b.Build(s, func(*scene.Image) uintptr { return 1 })
	if b.Culled != 0 || len(b.Instances) != len(s.Ops) {
		t.Fatal("sprite frame retained the previous culling plan")
	}
}

// A shader's effect draws with its wide uniforms for a wide gamut, and
// with its own otherwise.
func TestShaderWideUniforms(t *testing.T) {
	fx := &scene.Effect{Name: "shader", Shader: &scene.ShaderLayout{Lanes: []int32{0}, Size: 16}}
	narrow, wide := []uint32{1}, []uint32{2}
	s := &scene.Scene{Width: 40, Height: 40,
		Ops:     []scene.Op{{Kind: scene.OpEffect, Rect: scene.Rect{W: 20, H: 20}}},
		Effects: []scene.EffectOp{{Effect: fx, Uniforms: narrow, WideUniforms: wide}}}
	for _, w := range []bool{false, true} {
		var b Builder
		b.Wide = w
		b.Build(s, func(*scene.Image) uintptr { return 1 })
		want := narrow
		if w {
			want = wide
		}
		if len(b.Batches) != 1 || &b.Batches[0].Uniforms[0] != &want[0] {
			t.Errorf("wide %v: the batch draws with %v, want %v", w, b.Batches[0].Uniforms, want)
		}
		b.Release()
	}
}

// Boxes that translations move build the instances and batches of the same
// boxes placed without transforms: the shaders' own classes, not the
// general one.
func TestTranslationsFold(t *testing.T) {
	build := func(s *scene.Scene, noFold bool) Builder {
		b := Builder{Classes: true, ClipSkip: true, NoFold: noFold}
		b.Build(s, func(*scene.Image) uintptr { return 1 })
		return b
	}
	translated := gputest.BoxesScene(400, 300, 60, true)
	want, got := build(gputest.BoxesScene(400, 300, 60, false), false), build(translated, false)
	if !slices.Equal(got.Instances, want.Instances) || !slices.EqualFunc(got.Batches, want.Batches, func(a, b Batch) bool {
		return a.Start == b.Start && a.Count == b.Count && a.Class == b.Class && a.Scissor == b.Scissor
	}) {
		t.Fatal("translated boxes build other instances than boxes in their places")
	}
	for _, bt := range got.Batches {
		if bt.Class == ClassGeneral && bt.Joined == 0 {
			t.Fatalf("a batch of translated boxes takes the general shader: %+v", bt)
		}
	}
	if translated.Ops[0].Transform.Set == false {
		t.Fatal("building changed the scene")
	}
	unfolded := build(translated, true)
	if unfolded.Batches[0].Class != ClassGeneral {
		t.Fatal("NoFold folded the translations")
	}
}

// TestCornerSplitShrinksTheCurved checks that a card with continuous
// corners and the shadows it casts are drawn as nine-patches: the pieces of
// each tile what it drew whole, those drawn with the curves lie in the
// corners' boxes, which hold a fraction of the pixels, and the others have
// square corners, in batches without continuous corners.
func TestCornerSplitShrinksTheCurved(t *testing.T) {
	card := scene.Rect{X: 100.5, Y: 80.25, W: 600, H: 400}
	r := scene.Uniform(24)
	s := &scene.Scene{Width: 800, Height: 600, Ops: []scene.Op{
		{Kind: scene.OpShadow, Rect: scene.Rect{X: card.X, Y: card.Y + 4, W: card.W, H: card.H}, Radii: r, Color: scene.Color{A: 60}, Blur: 24,
			Cast: card, CastRadii: r, Continuous: true},
		{Kind: scene.OpShadow, Rect: card, Radii: r, Color: scene.Color{A: 60}, Blur: 4, Continuous: true},
		{Kind: scene.OpFill, Rect: card, Radii: r, Color: scene.Color{R: 255, A: 200}, Border: scene.Uniform(2), BorderColor: scene.Color{B: 255, A: 255},
			Continuous: true},
	}}
	var split, whole Builder
	split.Classes, whole.Classes = true, true
	split.SplitCorners()
	whole.NoCornerSplit = true
	split.Build(s, nil)
	whole.Build(s, nil)
	if len(whole.Instances) != 6 { // the first shadow in four pieces around its caster
		t.Fatalf("%d instances whole, want 6", len(whole.Instances))
	}
	frame := Scissor{0, 0, 800, 600}
	// The pixels of an instance's quad, as the vertex shader grows it.
	quadOf := func(in Instance) Scissor {
		e := float32(1)
		if in.Params[0] == 1 {
			e += 3 * in.Params[2]
		}
		return Scissor{int32(math.Floor(float64(in.Rect[0] - e))), int32(math.Floor(float64(in.Rect[1] - e))),
			int32(math.Ceil(float64(in.Rect[0] + in.Rect[2] + e))), int32(math.Ceil(float64(in.Rect[1] + in.Rect[3] + e)))}
	}
	for _, kind := range []float32{1, 0} {
		var curved, all int64
		type cut struct {
			rect [4]float32
			free Scissor
		}
		var cuts []cut
		for _, in := range whole.Instances {
			if in.Params[0] == kind {
				free, _, _, _ := cornersOf(&in, scene.Affine{})
				cuts = append(cuts, cut{in.Rect, free})
			}
		}
		for _, bt := range split.Batches {
			for _, in := range split.Instances[bt.Start : bt.Start+bt.Count] {
				if in.Params[0] != kind {
					continue
				}
				all += bt.Scissor.intersect(quadOf(in)).area()
				if in.Radii == [4]float32{} {
					if bt.Continuous {
						t.Errorf("kind %v: a piece with square corners in a batch with continuous ones", kind)
					}
					continue
				}
				if !bt.Continuous {
					t.Errorf("kind %v: a piece with continuous corners in a batch without", kind)
				}
				curved += bt.Scissor.intersect(quadOf(in)).area()
				// In the corners' boxes: out of the columns and the rows between them.
				for _, c := range cuts {
					cols := Scissor{c.free.Left, frame.Top, c.free.Right, frame.Bottom}
					rows := Scissor{frame.Left, c.free.Top, frame.Right, c.free.Bottom}
					if bt.Scissor.intersect(cols).area() != 0 && bt.Scissor.intersect(rows).area() != 0 &&
						c.rect == in.Rect {
						t.Errorf("kind %v: curved piece %v between the corners %v", kind, bt.Scissor, c.free)
					}
				}
			}
		}
		var want int64
		for _, bt := range whole.Batches {
			for _, in := range whole.Instances[bt.Start : bt.Start+bt.Count] {
				if in.Params[0] == kind {
					want += bt.Scissor.intersect(quadOf(in)).area()
				}
			}
		}
		if all != want {
			t.Errorf("kind %v: pieces cover %d px, whole %d", kind, all, want)
		}
		if 3*curved > want {
			t.Errorf("kind %v: %d of %d px drawn with the curves", kind, curved, want)
		}
		t.Logf("kind %v: %d of %d px drawn with the curves", kind, curved, want)
	}
}

// TestCornerSplitPaysForItsDrawCalls counts the instances and batches of
// scenes with continuous corners drawn as nine-patches and whole: a grid
// of small shadowed boxes, whose pixels are all near their corners, stays
// as it is whole (a shape and its shadow joined into few batches), and a
// dashboard of large cards adds a few draw calls for each, no more.
func TestCornerSplitPaysForItsDrawCalls(t *testing.T) {
	r4 := func(r float32) [4]float32 { return [4]float32{r, r, r, r} }
	// 600 boxes of 24 DIPs at twice the density, moved by translations.
	boxes := &scene.Scene{Width: 1800, Height: 1280}
	for i := range 600 {
		box := scene.Rect{W: 48, H: 48}
		m := scene.Translation(16+float32(i%30)*58+float32(i%7)*0.5, 16+float32(i/30)*62+float32(i%5)*0.4)
		boxes.Ops = append(boxes.Ops,
			scene.Op{Kind: scene.OpShadow, Rect: scene.Rect{Y: 4, W: 48, H: 48}, Radii: r4(12), Color: scene.Color{A: 70}, Blur: 16,
				Cast: box, CastRadii: r4(12), Continuous: true, Transform: m},
			scene.Op{Kind: scene.OpFill, Rect: box, Radii: r4(12), Color: scene.Color{R: uint8(i), G: 120, B: 200, A: 255}, Continuous: true, Transform: m})
	}
	// 12 cards of 290×235 DIPs at twice the density, casting two shadows.
	cards := &scene.Scene{Width: 2560, Height: 1600}
	for i := range 12 {
		card := scene.Rect{X: 48 + float32(i%4)*620, Y: 48 + float32(i/4)*510, W: 580, H: 470}
		sh := card
		sh.Y += 4
		cards.Ops = append(cards.Ops,
			scene.Op{Kind: scene.OpShadow, Rect: sh, Radii: r4(24), Color: scene.Color{A: 40}, Blur: 24, Cast: card, CastRadii: r4(24), Continuous: true},
			scene.Op{Kind: scene.OpShadow, Rect: card, Radii: r4(24), Color: scene.Color{A: 30}, Blur: 4, Cast: card, CastRadii: r4(24), Continuous: true},
			scene.Op{Kind: scene.OpFill, Rect: card, Radii: r4(24), Color: scene.Color{R: 255, G: 255, B: 255, A: 255}, Continuous: true})
	}
	for _, c := range []struct {
		name               string
		s                  *scene.Scene
		instances, batches int // nine-patches
	}{
		{"boxes", boxes, 1200, 1},
		{"cards", cards, 252, 252},
	} {
		var split, whole Builder
		for _, b := range []*Builder{&split, &whole} {
			b.Classes, b.Opaque, b.ClipSkip = true, true, true
			b.SplitCorners()
		}
		whole.NoCornerSplit = true
		split.Build(c.s, nil)
		whole.Build(c.s, nil)
		t.Logf("%s: %d instances in %d batches, whole %d in %d", c.name, len(split.Instances), len(split.Batches),
			len(whole.Instances), len(whole.Batches))
		if len(split.Instances) != c.instances || len(split.Batches) != c.batches {
			t.Errorf("%s: %d instances in %d batches, want %d in %d", c.name, len(split.Instances), len(split.Batches), c.instances, c.batches)
		}
	}
}

func TestMovedClipStaysInTheShader(t *testing.T) {
	// A panel sliding in, its content in a rounded card scrolled by a
	// clip, both only moved: no mask, and the clips cut where they moved.
	move := scene.Translation(0, 37.5)
	s := &scene.Scene{Width: 400, Height: 300, Ops: []scene.Op{
		{Kind: scene.OpPushClip, Rect: scene.Rect{W: 400, H: 300}, Radii: [4]float32{12, 12, 12, 12}, Transform: move},
		{Kind: scene.OpPushClip, Rect: scene.Rect{X: 20, Y: 20, W: 200, H: 100}, Transform: move},
		{Kind: scene.OpFill, Rect: scene.Rect{X: 20, Y: 20, W: 300, H: 32}, Color: scene.Color{A: 255}},
		{Kind: scene.OpPopClip},
		{Kind: scene.OpPopClip},
	}}
	var b Builder
	defer b.Release()
	masks := 0
	b.Build(s, func(*scene.Image) uintptr { masks++; return 1 })
	if masks != 0 || len(b.maskSlots) != 0 {
		t.Fatalf("rasterized %d clip masks for clips only moved", masks)
	}
	if in := b.Instances[0]; in.Transform1[3] != 0 || in.Clip != [4]float32{20, 57.5, 200, 100} {
		t.Fatalf("clip %v, mask mode %v", in.Clip, in.Transform1[3])
	}
}
