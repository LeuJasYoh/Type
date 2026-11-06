package scene

import (
	"math"
	"slices"
)

// ClipShape is a clip in the coordinate system in which it was pushed.
type ClipShape struct {
	Rect       Rect
	Radii      [4]float32
	Transform  Affine
	Continuous bool
	// Paint, unless PaintSolid, is a gradient whose alpha masks the clip,
	// with Gradient, Color, Color2 and Stops, as an OpFill's.
	Paint         Paint
	Gradient      [4]float32
	Color, Color2 Color
	Stops         []Stop
	// Mask, unless nil, is an image whose alpha in MaskSrc, drawn over
	// Rect, masks the clip, as it was at MaskVersion.
	Mask        *Image
	MaskSrc     Rect
	MaskVersion uint64
}

// ClipShapeOf returns the shape of op, an OpPushClip of a scene with
// stops, moved as MovedClipOp moves it: its Stops are a part of stops.
func ClipShapeOf(op *Op, stops []Stop) ClipShape {
	o := MovedClipOp(op)
	c := ClipShape{Rect: o.Rect, Radii: o.Radii, Transform: o.Transform, Continuous: o.Continuous}
	if o.Paint != PaintSolid {
		c.Paint, c.Gradient, c.Color, c.Color2 = o.Paint, o.Gradient, o.Color, o.Color2
		if o.Paint.HasStops() {
			if a, b := int(o.Stops[0]), int(o.Stops[1]); a >= 0 && b <= len(stops) && a < b {
				c.Stops = stops[a:min(b, a+MaxStops)]
			} else {
				// No stops: the gradient lets nothing through, as it
				// fills nothing.
				c.Stops = []Stop{}
			}
		}
	}
	if o.Image != nil {
		c.Mask, c.MaskSrc, c.MaskVersion = o.Image, o.Src, o.Image.Version()
	}
	return c
}

// Masks reports whether a gradient or an image masks the clip, which
// then lets through less than all of what it holds inside its rounded
// rectangle.
func (c *ClipShape) Masks() bool { return c.Paint != PaintSolid || c.Mask != nil }

// Same reports whether the clips are the same, their stops compared by
// what they hold.
func (c *ClipShape) Same(o *ClipShape) bool {
	return c.Rect == o.Rect && c.Radii == o.Radii && c.Transform == o.Transform && c.Continuous == o.Continuous &&
		c.Paint == o.Paint && c.Gradient == o.Gradient && c.Color == o.Color && c.Color2 == o.Color2 &&
		c.Mask == o.Mask && c.MaskSrc == o.MaskSrc && c.MaskVersion == o.MaskVersion && slices.Equal(c.Stops, o.Stops)
}

// Affine maps drawing coordinates to frame coordinates: an affine map,
// or, when G or H is set, a projective one, which shows a plane turned in
// 3D in perspective, x and y going to
//
//	((A*x + C*y + X) / w, (B*x + D*y + Y) / w), with w = G*x + H*y + 1.
//
// The zero value is identity; Set distinguishes a singular matrix
// (including scale zero).
type Affine struct {
	A, B, C, D, X, Y float32
	G, H             float32
	Set              bool
}

func Matrix(a, b, c, d, x, y float32) Affine { return Projective(a, b, c, d, x, y, 0, 0) }

// Projective returns the projective map of the matrix
//
//	| a c x |
//	| b d y |
//	| g h 1 |
func Projective(a, b, c, d, x, y, g, h float32) Affine {
	if a == 1 && b == 0 && c == 0 && d == 1 && x == 0 && y == 0 && g == 0 && h == 0 {
		return Affine{}
	}
	return Affine{a, b, c, d, x, y, g, h, true}
}

func Translation(x, y float32) Affine { return Matrix(1, 0, 0, 1, x, y) }
func Scaling(x, y float32) Affine     { return Matrix(x, 0, 0, y, 0, 0) }
func Rotation(degrees float32) Affine {
	angle := math.Remainder(float64(degrees), 360)
	switch angle {
	case 0:
		return Affine{}
	case 90:
		return Matrix(0, 1, -1, 0, 0, 0)
	case -90:
		return Matrix(0, -1, 1, 0, 0, 0)
	case 180, -180:
		return Matrix(-1, 0, 0, -1, 0, 0)
	}
	s, c := math.Sincos(angle * math.Pi / 180)
	return Matrix(float32(c), float32(s), -float32(s), float32(c), 0, 0)
}

// Perspective reports whether m is projective, not affine.
func (m Affine) Perspective() bool { return m.Set && (m.G != 0 || m.H != 0) }

// MovedClip returns the rectangle of a clip of r under m, and what of m is
// left to apply to it: nothing when m only moves it, as a pose sliding a
// panel in or a card lifting under the pointer does. A clip only moved is
// a rectangle like any other, which renderers cut as they cut those that
// nothing transforms, rather than with a mask of its transformed shape.
func MovedClip(r Rect, m Affine) (Rect, Affine) {
	if !m.Set || m.A != 1 || m.B != 0 || m.C != 0 || m.D != 1 || m.G != 0 || m.H != 0 {
		return r, m
	}
	r.X += m.X
	r.Y += m.Y
	return r, Affine{}
}

// MovedClipOp returns op, an OpPushClip, as MovedClip moves it: its
// rectangle moved, with the points of its gradient, when its transform
// only moves them, the transform then unset.
func MovedClipOp(op *Op) Op {
	o := *op
	r, m := MovedClip(o.Rect, o.Transform)
	if m.Set || !o.Transform.Set {
		return o
	}
	dx, dy := r.X-o.Rect.X, r.Y-o.Rect.Y
	o.Rect, o.Transform = r, Affine{}
	o.moveGradient(dx, dy)
	return o
}

// moveGradient moves the points of the op's gradient by (x, y); stripes
// start from Rect.
func (op *Op) moveGradient(x, y float32) {
	switch op.Paint {
	case PaintLinear, PaintOklab, PaintLinearStops, PaintLinearStopsOklab:
		op.Gradient = [4]float32{op.Gradient[0] + x, op.Gradient[1] + y, op.Gradient[2] + x, op.Gradient[3] + y}
	case PaintConic, PaintConicOklab, PaintRadial, PaintRadialOklab:
		op.Gradient[0], op.Gradient[1] = op.Gradient[0]+x, op.Gradient[1]+y
	}
}

// Reshaped reports whether op of s draws under a transform that does more
// than move it (scales, turns, skews it or shows it in perspective): its
// own, or, for glyphs and an effect masked by glyphs, a glyph's. Drawing
// a transform only moves lies in its clips as it would unmoved, which the
// GPU's renderers cut in the frame's coordinates whatever the transform,
// so the clips around it need no mask (gpu.Builder), and damage tracking has them change no more than
// for drawing not moved (Differ). Clips are the same (MovedClip): a clip
// pushed under a transform that only moves it reshapes none around it.
func Reshaped(s *Scene, op *Op) bool {
	if op.Transform.Set && !op.Transform.moves() {
		return true
	}
	start, end := int32(-1), int32(-1)
	switch op.Kind {
	case OpGlyphs:
		start, end = op.Start, op.End
	case OpEffect:
		// A shader's effect masked by glyphs draws them with their own
		// transforms too (EffectOp.Glyphs).
		if op.Start >= 0 && int(op.Start) < len(s.Effects) {
			start, end = s.Effects[op.Start].Glyphs[0], s.Effects[op.Start].Glyphs[1]
		}
	}
	if start >= 0 && start <= end && int(end) <= len(s.Glyphs) {
		for _, g := range s.Glyphs[start:end] {
			if g.Transform.Set && !g.Transform.moves() {
				return true
			}
		}
	}
	return false
}

// moves reports whether m only moves what it maps, without scaling,
// turning, skewing it or showing it in perspective.
func (m Affine) moves() bool {
	return m.A == 1 && m.B == 0 && m.C == 0 && m.D == 1 && m.G == 0 && m.H == 0
}

// matrix returns m's matrix, by rows.
func (m Affine) matrix() [3][3]float64 {
	if !m.Set {
		return [3][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	}
	return [3][3]float64{
		{float64(m.A), float64(m.C), float64(m.X)},
		{float64(m.B), float64(m.D), float64(m.Y)},
		{float64(m.G), float64(m.H), 1},
	}
}

// fromMatrix returns the map of matrix p, scaled so that its last entry
// is 1, and whether it has one.
func fromMatrix(p [3][3]float64) (Affine, bool) {
	k := p[2][2]
	if math.Abs(k) < 1e-12 || math.IsNaN(k) || math.IsInf(k, 0) {
		return Affine{}, false
	}
	v := [8]float64{p[0][0] / k, p[1][0] / k, p[0][1] / k, p[1][1] / k, p[0][2] / k, p[1][2] / k, p[2][0] / k, p[2][1] / k}
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > math.MaxFloat32 {
			return Affine{}, false
		}
	}
	return Projective(float32(v[0]), float32(v[1]), float32(v[2]), float32(v[3]), float32(v[4]), float32(v[5]), float32(v[6]), float32(v[7])), true
}

// singular is a map without an inverse, which shows nothing.
var singular = Affine{Set: true}

// Mul returns m*n: n acts first, then m.
func (m Affine) Mul(n Affine) Affine {
	if !m.Set {
		return n
	}
	if !n.Set {
		return m
	}
	if m.moves() && n.moves() {
		// As the general product, but for the signs of zeros.
		return Matrix(1, 0, 0, 1, n.X+m.X, n.Y+m.Y)
	}
	if !m.Perspective() && !n.Perspective() {
		return Matrix(m.A*n.A+m.C*n.B, m.B*n.A+m.D*n.B,
			m.A*n.C+m.C*n.D, m.B*n.C+m.D*n.D,
			m.A*n.X+m.C*n.Y+m.X, m.B*n.X+m.D*n.Y+m.Y)
	}
	a, b := m.matrix(), n.matrix()
	var p [3][3]float64
	for i := range 3 {
		for j := range 3 {
			p[i][j] = a[i][0]*b[0][j] + a[i][1]*b[1][j] + a[i][2]*b[2][j]
		}
	}
	r, ok := fromMatrix(p)
	if !ok {
		return singular
	}
	return r
}

// W returns the projective divisor of (x, y): positive in front of the
// viewer, as all of an affine map's plane is.
func (m Affine) W(x, y float32) float32 {
	if !m.Set {
		return 1
	}
	return m.G*x + m.H*y + 1
}

// Point returns where m maps (x, y). A point behind the viewer of a
// projective map (W not positive) goes far away.
func (m Affine) Point(x, y float32) (float32, float32) {
	if !m.Set {
		return x, y
	}
	px, py := m.A*x+m.C*y+m.X, m.B*x+m.D*y+m.Y
	if m.G == 0 && m.H == 0 {
		return px, py
	}
	w := max(m.W(x, y), 1e-6)
	return px / w, py / w
}

// VectorAt returns how far m moves the point (x, y) moved by (dx, dy):
// the same for every point of an affine map.
func (m Affine) VectorAt(x, y, dx, dy float32) (float32, float32) {
	if !m.Set {
		return dx, dy
	}
	if m.G == 0 && m.H == 0 {
		return m.A*dx + m.C*dy, m.B*dx + m.D*dy
	}
	x0, y0 := m.Point(x, y)
	x1, y1 := m.Point(x+dx, y+dy)
	return x1 - x0, y1 - y0
}

func (m Affine) Inverse() (Affine, bool) {
	if !m.Set {
		return m, true
	}
	if m.G == 0 && m.H == 0 {
		det := float64(m.A)*float64(m.D) - float64(m.B)*float64(m.C)
		if det == 0 || math.IsNaN(det) || math.IsInf(det, 0) {
			return Affine{}, false
		}
		a, b, c, d := float32(float64(m.D)/det), float32(-float64(m.B)/det), float32(-float64(m.C)/det), float32(float64(m.A)/det)
		n := Matrix(a, b, c, d, -a*m.X-c*m.Y, -b*m.X-d*m.Y)
		for _, v := range [6]float32{n.A, n.B, n.C, n.D, n.X, n.Y} {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return Affine{}, false
			}
		}
		return n, true
	}
	q := m.matrix()
	cof := func(r0, r1, c0, c1 int) float64 { return q[r0][c0]*q[r1][c1] - q[r0][c1]*q[r1][c0] }
	adj := [3][3]float64{
		{cof(1, 2, 1, 2), -cof(0, 2, 1, 2), cof(0, 1, 1, 2)},
		{-cof(1, 2, 0, 2), cof(0, 2, 0, 2), -cof(0, 1, 0, 2)},
		{cof(1, 2, 0, 1), -cof(0, 2, 0, 1), cof(0, 1, 0, 1)},
	}
	det := q[0][0]*adj[0][0] + q[0][1]*adj[1][0] + q[0][2]*adj[2][0]
	if math.Abs(det) < 1e-12 || math.IsNaN(det) || math.IsInf(det, 0) {
		return Affine{}, false
	}
	return fromMatrix(adj) // the inverse up to a factor, which fromMatrix drops
}

func (m Affine) Bounds(r Rect) Rect {
	if !m.Set {
		return r
	}
	if m.B == 0 && m.C == 0 && m.G == 0 && m.H == 0 {
		// Without rotation, skew or perspective: as from the corners, but
		// for the signs of zeros, rounding the products as Point does
		// (float32 keeps them from fusing with the sums).
		x0, x1 := float32(m.A*r.X)+m.X, float32(m.A*(r.X+r.W))+m.X
		y0, y1 := float32(m.D*r.Y)+m.Y, float32(m.D*(r.Y+r.H))+m.Y
		x0, x1 = min(x0, x1), max(x0, x1)
		y0, y1 = min(y0, y1), max(y0, y1)
		return Rect{x0, y0, x1 - x0, y1 - y0}
	}
	corners := [4][2]float32{{r.X, r.Y}, {r.X + r.W, r.Y}, {r.X, r.Y + r.H}, {r.X + r.W, r.Y + r.H}}
	if m.Perspective() {
		for _, p := range corners {
			if m.W(p[0], p[1]) <= 1e-6 {
				// Part of it is behind the viewer: it may show anywhere.
				return Rect{-1e6, -1e6, 2e6, 2e6}
			}
		}
	}
	x0, y0 := m.Point(r.X, r.Y)
	x1, y1 := x0, y0
	for _, p := range corners[1:] {
		x, y := m.Point(p[0], p[1])
		x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
	}
	return Rect{x0, y0, x1 - x0, y1 - y0}
}

// Pixels expresses a DIP transform in device pixels.
func (m Affine) Pixels(scale float32) Affine {
	if m.Set {
		m.X *= scale
		m.Y *= scale
		m.G /= scale
		m.H /= scale
	}
	return m
}
