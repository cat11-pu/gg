package gg

import (
	"math"

	"github.com/golang/freetype/raster"
)

// DefaultMiterLimit is the default ratio of miter length to stroke width at
// which a miter join is converted to a bevel join. It matches the value used
// by the SVG and Canvas 2D specifications.
const DefaultMiterLimit = 10.0

// collinearTol is the sine tolerance below which two consecutive segments
// are treated as collinear.
const collinearTol = 1e-7

// ringEpsilon is the distance below which two outline points are treated as
// coincident when assembling the filled stroke polygon.
const ringEpsilon = 1e-6

// WidthFunc returns the stroke width at parametric position t in [0, 1]
// along a path. A nil WidthFunc means a constant width is used.
type WidthFunc func(t float64) float64

// strokeOptions controls how a path is stroked.
type strokeOptions struct {
	width      float64
	widthFunc  WidthFunc
	cap        LineCap
	join       LineJoin
	miterLimit float64
}

// strokeOutline builds the filled outline of a stroked set of sub-paths in
// float64 space and appends it to the supplied rasterizer.
func strokeOutline(r *raster.Rasterizer, paths [][]Point, opt strokeOptions) {
	for _, points := range paths {
		strokeSubpath(r, points, opt)
	}
}

func strokeSubpath(r *raster.Rasterizer, raw []Point, opt strokeOptions) {
	pts := simplifyPoints(raw)
	if len(pts) < 2 {
		return
	}

	n := len(pts)
	closed := pts[0] == pts[n-1]
	if closed {
		pts = pts[:n-1]
		n = len(pts)
		if n < 2 {
			return
		}
	}

	half := make([]float64, n)
	for i := 0; i < n; i++ {
		half[i] = 0.5 * widthAt(opt, i, n)
	}

	// Segment unit directions and left/right normal unit vectors.
	segCount := n - 1
	if closed {
		segCount = n
	}
	dir := make([]Point, segCount)
	ln := make([]Point, segCount)
	for s := 0; s < segCount; s++ {
		a := pts[s]
		b := pts[(s+1)%n]
		dir[s] = normalize(b.Sub(a))
		ln[s] = pRot90CCW(dir[s])
	}

	// Boundary points on both sides for every segment.
	lStart := make([]Point, segCount)
	lEnd := make([]Point, segCount)
	rStart := make([]Point, segCount)
	rEnd := make([]Point, segCount)
	for s := 0; s < segCount; s++ {
		i, j := s, (s+1)%n
		lStart[s] = pts[i].Add(ln[s].Scale(half[i]))
		lEnd[s] = pts[j].Add(ln[s].Scale(half[j]))
		rStart[s] = pts[i].Sub(ln[s].Scale(half[i]))
		rEnd[s] = pts[j].Sub(ln[s].Scale(half[j]))
	}

	var outline []Point

	if closed {
		// Left side, walking forward.
		for s := 0; s < segCount; s++ {
			outline = append(outline, lStart[s])
			next := (s + 1) % segCount
			outline = appendJoin(outline, pts[(s+1)%n], lEnd[s], lStart[next],
				half[(s+1)%n], opt, true)
		}
		// Right side, walking backward.
		for s := segCount - 1; s >= 0; s-- {
			prev := (s - 1 + segCount) % segCount
			outline = append(outline, rEnd[s])
			outline = appendJoin(outline, pts[s], rStart[s], rEnd[prev],
				half[s], opt, false)
		}
	} else {
		// Left side, walking forward.
		for s := 0; s < segCount; s++ {
			outline = append(outline, lStart[s])
			if s < segCount-1 {
				outline = appendJoin(outline, pts[s+1], lEnd[s], lStart[s+1],
					half[s+1], opt, true)
			} else {
				outline = append(outline, lEnd[s])
			}
		}
		// End cap.
		outline = appendCap(outline, pts[n-1], dir[segCount-1], half[n-1], opt.cap, true)
		// Right side, walking backward.
		for s := segCount - 1; s >= 0; s-- {
			outline = append(outline, rEnd[s])
			if s > 0 {
				outline = appendJoin(outline, pts[s], rStart[s], rEnd[s-1],
					half[s], opt, false)
			} else {
				outline = append(outline, rStart[0])
			}
		}
		// Start cap.
		outline = appendCap(outline, pts[0], dir[0], half[0], opt.cap, false)
	}

	appendPolygon(r, outline)
}

// appendJoin appends the outer boundary connecting the incoming offset point
// a to the outgoing offset point b around the vertex pivot. The radius is the
// half width at the vertex. leftSide selects which boundary is being built.
func appendJoin(outline []Point, pivot, a, b Point, radius float64,
	opt strokeOptions, leftSide bool) []Point {

	va := normalize(a.Sub(pivot))
	vb := normalize(b.Sub(pivot))
	cross := va.X*vb.Y - va.Y*vb.X
	dot := clamp(va.Dot(vb), -1, 1)

	// Collinear segments: the boundary continues straight through.
	if math.Abs(cross) < collinearTol {
		return append(outline, b)
	}

	turnLeft := cross > 0
	outer := leftSide == turnLeft

	if !outer {
		// Inner corner: the boundary passes through the single vertex point.
		return append(outline, b)
	}

	switch opt.join {
	case LineJoinRound:
		startAngle := math.Atan2(a.Y-pivot.Y, a.X-pivot.X)
		endAngle := math.Atan2(b.Y-pivot.Y, b.X-pivot.X)
		return appendArcPoints(outline, pivot, radius, startAngle, endAngle, turnLeft)

	case LineJoinMiter:
		angle := math.Acos(dot)
		if angle < collinearTol {
			return append(outline, b)
		}
		miterRatio := 1 / math.Sin(angle/2)
		if miterRatio <= opt.miterLimit {
			// Intersect the two offset edges to form the miter tip.
			tip, ok := intersectRay(a, normalize(pivot.Sub(a)), b, normalize(pivot.Sub(b)))
			if ok {
				return append(outline, tip)
			}
		}
		// Miter limit exceeded: fall back to a bevel.
		return append(outline, b)

	default: // LineJoinBevel
		return append(outline, b)
	}
}

// appendCap appends the boundary of an end cap. At the end of the path the
// outline arrives at the left-hand offset point; at the start it arrives at
// the right-hand offset point.
func appendCap(outline []Point, pivot, tangent Point, half float64,
	cap LineCap, atEnd bool) []Point {

	// A zero-width cap collapses onto the pivot; the boundary edges already
	// meet there, so adding cap geometry would create a degenerate spike.
	if half <= 0 {
		return append(outline, pivot)
	}

	normal := pRot90CCW(tangent)
	left := pivot.Add(normal.Scale(half))
	right := pivot.Sub(normal.Scale(half))

	switch cap {
	case LineCapButt:
		if atEnd {
			return append(outline, right)
		}
		return append(outline, left)

	case LineCapSquare:
		ext := tangent.Scale(half)
		if atEnd {
			return append(outline, left.Add(ext), right.Add(ext), right)
		}
		return append(outline, right.Sub(ext), left.Sub(ext), left)

	default: // LineCapRound
		aLeft := math.Atan2(left.Y-pivot.Y, left.X-pivot.X)
		aRight := math.Atan2(right.Y-pivot.Y, right.X-pivot.X)
		if atEnd {
			// Sweep from the left point around the end to the right point.
			return appendArcPoints(outline, pivot, half, aLeft, aRight, false)
		}
		// Sweep from the right point, around the outside of the start, to the
		// left point. Both end caps bulge outward and use a clockwise sweep.
		return appendArcPoints(outline, pivot, half, aRight, aLeft, false)
	}
}

// appendArcPoints appends a polyline approximating a circular arc centered at
// pivot. The arc sweeps from angle a0 to a1 counter-clockwise when ccw is
// true, clockwise otherwise, always taking the shorter turn.
func appendArcPoints(outline []Point, pivot Point, radius, a0, a1 float64, ccw bool) []Point {
	delta := a1 - a0
	if ccw {
		for delta < 0 {
			delta += 2 * math.Pi
		}
		if delta > math.Pi {
			delta -= 2 * math.Pi
		}
	} else {
		for delta > 0 {
			delta -= 2 * math.Pi
		}
		if delta < -math.Pi {
			delta += 2 * math.Pi
		}
	}

	steps := int(math.Ceil(math.Abs(delta) / (math.Pi / 8)))
	if steps < 1 {
		steps = 1
	}
	for i := 1; i <= steps; i++ {
		a := a0 + delta*float64(i)/float64(steps)
		outline = append(outline, Point{
			pivot.X + radius*math.Cos(a),
			pivot.Y + radius*math.Sin(a),
		})
	}
	return outline
}

// intersectRay returns the intersection of the ray starting at p1 in
// direction d1 and the ray starting at p2 in direction d2.
func intersectRay(p1, d1, p2, d2 Point) (Point, bool) {
	den := d1.X*d2.Y - d1.Y*d2.X
	if math.Abs(den) < 1e-12 {
		return Point{}, false
	}
	dp := p2.Sub(p1)
	t := (dp.X*d2.Y - dp.Y*d2.X) / den
	return Point{p1.X + t*d1.X, p1.Y + t*d1.Y}, true
}

// widthAt evaluates the width at vertex i, falling back to the constant
// width when no width function is supplied.
func widthAt(opt strokeOptions, i, n int) float64 {
	if opt.widthFunc == nil {
		return opt.width
	}
	t := 0.0
	if n > 1 {
		t = float64(i) / float64(n-1)
	}
	w := opt.widthFunc(t)
	if w < 0 || math.IsNaN(w) || math.IsInf(w, 0) {
		w = 0
	}
	return w
}

// simplifyPoints drops consecutive duplicate vertices.
func simplifyPoints(points []Point) []Point {
	if len(points) == 0 {
		return nil
	}
	out := make([]Point, 0, len(points))
	for _, p := range points {
		if len(out) > 0 && p == out[len(out)-1] {
			continue
		}
		out = append(out, p)
	}
	return out
}

// appendPolygon feeds a closed polygon into the rasterizer.
func appendPolygon(r *raster.Rasterizer, polygon []Point) {
	polygon = dedupeRing(polygon)
	if len(polygon) < 3 {
		return
	}
	r.Start(polygon[0].Fixed())
	prev := polygon[0]
	for _, p := range polygon[1:] {
		if p == prev {
			continue
		}
		r.Add1(p.Fixed())
		prev = p
	}
	r.Add1(polygon[0].Fixed())
}

// dedupeRing removes consecutive duplicate points, treating the first and
// last point as neighbors so a degenerate cap cannot pull a stray edge across
// the polygon.
func dedupeRing(points []Point) []Point {
	if len(points) == 0 {
		return nil
	}
	out := make([]Point, 0, len(points))
	for i, p := range points {
		if i > 0 && p.Distance(out[len(out)-1]) < ringEpsilon {
			continue
		}
		out = append(out, p)
	}
	// Drop a duplicated closing point.
	if len(out) > 1 && out[0].Distance(out[len(out)-1]) < ringEpsilon {
		out = out[:len(out)-1]
	}
	return out
}

func normalize(p Point) Point {
	l := math.Hypot(p.X, p.Y)
	if l < 1e-12 {
		return Point{}
	}
	return Point{p.X / l, p.Y / l}
}

func pRot90CCW(p Point) Point {
	return Point{-p.Y, p.X}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (a Point) Sub(b Point) Point     { return Point{a.X - b.X, a.Y - b.Y} }
func (a Point) Add(b Point) Point     { return Point{a.X + b.X, a.Y + b.Y} }
func (a Point) Scale(s float64) Point { return Point{a.X * s, a.Y * s} }
