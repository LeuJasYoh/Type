package scene

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestAffineCompositionInverseAndSingular(t *testing.T) {
	m := Translation(30, 20).Mul(Rotation(90)).Mul(Scaling(2, -3))
	x, y := m.Point(4, 5)
	if math.Abs(float64(x-45)) > 1e-4 || math.Abs(float64(y-28)) > 1e-4 {
		t.Fatalf("composed point %v,%v", x, y)
	}
	i, ok := m.Inverse()
	if !ok {
		t.Fatal("invertible matrix rejected")
	}
	x, y = i.Point(x, y)
	if math.Abs(float64(x-4)) > 1e-4 || math.Abs(float64(y-5)) > 1e-4 {
		t.Fatalf("inverse point %v,%v", x, y)
	}
	if _, ok := Scaling(0, 1).Inverse(); ok {
		t.Fatal("singular matrix inverted")
	}
	b := m.Bounds(Rect{W: 4, H: 5})
	if math.Abs(float64(b.W-15)) > 1e-4 || math.Abs(float64(b.H-8)) > 1e-4 {
		t.Fatalf("bounds %v", b)
	}
	if (Affine{}).Mul(m) != m || m.Mul(Affine{}) != m {
		t.Fatal("zero value is not identity")
	}
}

// Bounds and Mul of maps that only move or scale take shortcuts: they
// must give what the corners and the general product give.
func TestAffineShortcutsMatch(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	v := func() float32 {
		switch r.IntN(4) {
		case 0:
			return 0
		case 1:
			return float32(r.IntN(200) - 100)
		}
		return (r.Float32() - 0.5) * 2000
	}
	for range 100000 {
		m := Matrix(1, 0, 0, 1, v(), v())
		if r.IntN(2) == 0 {
			m = Matrix(v()/100, 0, 0, v()/100, v(), v())
		}
		n := Translation(v(), v())
		rect := Rect{v(), v(), v(), v()}
		got, want := m.Bounds(rect), cornerBounds(m, rect)
		if got != want {
			t.Fatalf("%v.Bounds(%v) = %v, corners give %v", m, rect, got, want)
		}
		if m.moves() && n.Set {
			want := Matrix(m.A*n.A+m.C*n.B, m.B*n.A+m.D*n.B, m.A*n.C+m.C*n.D, m.B*n.C+m.D*n.D,
				m.A*n.X+m.C*n.Y+m.X, m.B*n.X+m.D*n.Y+m.Y)
			if got := m.Mul(n); got != want {
				t.Fatalf("%v.Mul(%v) = %v, want %v", m, n, got, want)
			}
		}
	}
}

// cornerBounds is Bounds from the corners, for maps without perspective.
func cornerBounds(m Affine, r Rect) Rect {
	if !m.Set {
		return r
	}
	corners := [4][2]float32{{r.X, r.Y}, {r.X + r.W, r.Y}, {r.X, r.Y + r.H}, {r.X + r.W, r.Y + r.H}}
	x0, y0 := m.Point(r.X, r.Y)
	x1, y1 := x0, y0
	for _, p := range corners[1:] {
		x, y := m.Point(p[0], p[1])
		x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
	}
	return Rect{x0, y0, x1 - x0, y1 - y0}
}

func TestMovedClip(t *testing.T) {
	r := Rect{X: 10, Y: 20, W: 100, H: 50}
	if got, m := MovedClip(r, Translation(5, -8)); got != (Rect{X: 15, Y: 12, W: 100, H: 50}) || m.Set {
		t.Errorf("moved: %v, left %v", got, m)
	}
	if got, m := MovedClip(r, Affine{}); got != r || m.Set {
		t.Errorf("untransformed: %v, left %v", got, m)
	}
	for _, m := range []Affine{Scaling(2, 2), Rotation(10), Translation(5, 5).Mul(Scaling(1.5, 1))} {
		if got, left := MovedClip(r, m); got != r || left != m {
			t.Errorf("%v: %v, left %v, want the clip as it was", m, got, left)
		}
	}
}

// Reshaped tells drawing a transform only moves from drawing it scales or
// turns, its own transform or its glyphs', as of an effect masked by
// glyphs, which draws them with their transforms too.
func TestReshaped(t *testing.T) {
	s := &Scene{
		Glyphs:  []Glyph{{W: 8, H: 12}, {W: 8, H: 12, Transform: Rotation(3)}, {W: 8, H: 12, Transform: Translation(0.5, 0.25)}},
		Effects: []EffectOp{{Glyphs: [2]int32{0, 1}}, {Glyphs: [2]int32{1, 2}}, {Glyphs: [2]int32{2, 3}}},
	}
	for _, c := range []struct {
		name string
		op   Op
		want bool
	}{
		{"fill moved", Op{Kind: OpFill, Transform: Translation(0, -1.27)}, false},
		{"fill turned", Op{Kind: OpFill, Transform: Rotation(3)}, true},
		{"fill scaled", Op{Kind: OpFill, Transform: Matrix(2, 0, 0, 2, 0, 0)}, true},
		{"glyphs moved", Op{Kind: OpGlyphs, Start: 0, End: 1, Transform: Translation(0, -2.54)}, false},
		{"glyph moved", Op{Kind: OpGlyphs, Start: 2, End: 3}, false},
		{"glyph turned", Op{Kind: OpGlyphs, Start: 0, End: 2, Transform: Translation(0, -2.54)}, true},
		{"effect moved", Op{Kind: OpEffect, Start: 0, Transform: Translation(0.5, 0)}, false},
		{"effect's glyph moved", Op{Kind: OpEffect, Start: 2, Transform: Translation(0.5, 0)}, false},
		{"effect's glyph turned", Op{Kind: OpEffect, Start: 1, Transform: Translation(0.5, 0)}, true},
	} {
		if got := Reshaped(s, &c.op); got != c.want {
			t.Errorf("%s: Reshaped %v, want %v", c.name, got, c.want)
		}
	}
}
