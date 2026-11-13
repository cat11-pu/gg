package gg

import (
	"image"
	"math"
	"testing"
)

// inkBounds returns the bounding box of all dark (stroked) pixels. The
// contexts are cleared to white, so darkness rather than alpha is used to
// locate the rendered stroke.
func inkBounds(im *image.RGBA) image.Rectangle {
	b := im.Bounds()
	min := image.Pt(b.Max.X, b.Max.Y)
	max := image.Pt(b.Min.X, b.Min.Y)
	any := false
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			i := im.PixOffset(x, y)
			if im.Pix[i] < 128 && im.Pix[i+1] < 128 && im.Pix[i+2] < 128 {
				any = true
				if x < min.X {
					min.X = x
				}
				if y < min.Y {
					min.Y = y
				}
				if x > max.X {
					max.X = x
				}
				if y > max.Y {
					max.Y = y
				}
			}
		}
	}
	if !any {
		return image.Rectangle{}
	}
	return image.Rect(min.X, min.Y, max.X+1, max.Y+1)
}

// inkCountAtColumn counts dark pixels in the given column.
func inkCountAtColumn(im *image.RGBA, x int) int {
	count := 0
	for y := im.Bounds().Min.Y; y < im.Bounds().Max.Y; y++ {
		i := im.PixOffset(x, y)
		if im.Pix[i] < 128 && im.Pix[i+1] < 128 && im.Pix[i+2] < 128 {
			count++
		}
	}
	return count
}

func renderTriangle(join LineJoin) *Context {
	dc := NewContext(400, 300)
	dc.SetRGB(1, 1, 1)
	dc.Clear()
	dc.SetRGB(0, 0, 0)
	dc.SetLineWidth(8)
	dc.SetLineJoin(join)
	// A triangle whose apex spans an angle of roughly 10 degrees.
	apex := Point{X: 60, Y: 150}
	dc.MoveTo(apex.X, apex.Y)
	dc.LineTo(340, 150-50)
	dc.LineTo(340, 150+50)
	dc.ClosePath()
	dc.Stroke()
	return dc
}

// TestAcuteTriangleClosed checks that the stroke outline at a very acute
// apex is a single filled region without a notch. The buggy freetype stroker
// left a gaping hole at such vertices.
func TestAcuteTriangleClosed(t *testing.T) {
	for _, join := range []LineJoin{LineJoinMiter, LineJoinRound, LineJoinBevel} {
		dc := renderTriangle(join)
		bounds := inkBounds(dc.im)
		if bounds.Empty() {
			t.Fatalf("join %d: nothing rendered", join)
		}
		// The triangle body (well away from the apex) must be a solid, closed
		// ring: every column there carries two disjoint ink bands, i.e. at
		// least two dark pixels. The buggy stroker left the outer outline
		// unclosed, producing a single broken band.
		for x := 150; x < 320; x += 10 {
			if inkCountAtColumn(dc.im, x) < 2 {
				t.Fatalf("join %d: column %d is not a closed stroke ring", join, x)
			}
		}
	}
}

// TestMiterLimitFallsBackToBevel checks that an angle sharper than the miter
// limit does not produce a long spike: the rendered tip length stays bounded.
func TestMiterLimitFallsBackToBevel(t *testing.T) {
	render := func(deg float64) image.Rectangle {
		dc := NewContext(500, 400)
		dc.SetRGB(1, 1, 1)
		dc.Clear()
		dc.SetRGB(0, 0, 0)
		dc.SetLineWidth(20)
		dc.SetLineCapButt()
		dc.SetLineJoinMiter()
		dc.SetMiterLimit(DefaultMiterLimit)
		half := Radians(deg / 2)
		apex := Point{X: 400, Y: 200}
		arm := 180.0
		p1 := Point{X: apex.X - arm*math.Cos(half), Y: apex.Y + arm*math.Sin(half)}
		p2 := Point{X: apex.X - arm*math.Cos(half), Y: apex.Y - arm*math.Sin(half)}
		dc.MoveTo(p1.X, p1.Y)
		dc.LineTo(apex.X, apex.Y)
		dc.LineTo(p2.X, p2.Y)
		dc.Stroke()
		return inkBounds(dc.im)
	}

	// 60 degrees: miter ratio 2, well under the limit, tip reaches the apex.
	b60 := render(60)
	if b60.Max.X < 395 {
		t.Fatalf("60 degree miter should reach the apex, got bounds %v", b60)
	}
	// 8 degrees: miter ratio ~14, over the limit, so the bevel fallback must
	// clip the spike noticeably compared with the 60 degree miter.
	b8 := render(8)
	if b8.Max.X >= b60.Max.X {
		t.Fatalf("8 degree bevel tip (%d) should be clipped relative to the 60 degree miter (%d)",
			b8.Max.X, b60.Max.X)
	}
	// The clipped tip must still sit close to the apex, i.e. it is a bevel,
	// not a missing join.
	if b60.Max.X-b8.Max.X > 40 {
		t.Fatalf("bevel fallback clipped too aggressively: %v vs %v", b8, b60)
	}
}

// TestVariableWidthStroke checks that a width function produces a stroke
// whose rendered thickness follows the requested width.
func TestVariableWidthStroke(t *testing.T) {
	dc := NewContext(600, 400)
	dc.SetRGB(1, 1, 1)
	dc.Clear()
	dc.SetRGB(0, 0, 0)
	dc.SetLineCapButt()
	dc.SetLineWidthFunc(func(pos float64) float64 {
		return 10 + 40*pos
	})
	dc.MoveTo(50, 200)
	dc.LineTo(550, 200)
	dc.Stroke()

	thicknessAt := func(x int) int { return inkCountAtColumn(dc.im, x) }
	thin := thicknessAt(80)
	thick := thicknessAt(520)
	if !(thick > 2*thin) {
		t.Fatalf("expected thick end to be more than twice the thin end, got thin=%d thick=%d", thin, thick)
	}
}

// TestSelfIntersectingNoSpike renders a four-petal rose and ensures the
// output is a compact, connected region without stray spike pixels far from
// the curve.
func TestSelfIntersectingNoSpike(t *testing.T) {
	dc := NewContext(400, 400)
	dc.SetRGB(1, 1, 1)
	dc.Clear()
	dc.SetRGB(0, 0, 0)
	dc.SetLineJoinRound()
	dc.SetLineCapRound()
	dc.SetLineWidthFunc(func(pos float64) float64 {
		return 6 + 14*math.Sin(math.Pi*pos)
	})
	const cx, cy, R = 200.0, 200.0, 160.0
	var first Point
	for i := 0; i <= 360; i++ {
		theta := 2 * math.Pi * float64(i) / 360
		r := R * math.Cos(2*theta)
		p := Point{X: cx + r*math.Cos(theta), Y: cy + r*math.Sin(theta)}
		if i == 0 {
			dc.MoveTo(p.X, p.Y)
			first = p
		} else {
			dc.LineTo(p.X, p.Y)
		}
	}
	dc.LineTo(first.X, first.Y)
	dc.Stroke()

	bounds := inkBounds(dc.im)
	// The rose fits inside a circle of radius R centered in the image; allow a
	// small rasterization margin. A spike would blow past these bounds.
	margin := 25
	want := image.Rect(cx-R-margin, cy-R-margin, cx+R+margin, cy+R+margin)
	if !bounds.In(want) {
		t.Fatalf("rendered bounds %v exceed expected region %v", bounds, want)
	}
}

// TestCaps verifies the geometric extent of each end cap on a horizontal
// stroke.
func TestCaps(t *testing.T) {
	render := func(cap LineCap) *Context {
		dc := NewContext(400, 200)
		dc.SetRGB(1, 1, 1)
		dc.Clear()
		dc.SetRGB(0, 0, 0)
		dc.SetLineWidth(40)
		dc.SetLineCap(cap)
		dc.DrawLine(100, 100, 300, 100)
		dc.Stroke()
		return dc
	}

	butt := inkBounds(render(LineCapButt).im)
	if butt.Min.X < 99 || butt.Max.X > 301 {
		t.Fatalf("butt cap should not extend past endpoints, got %v", butt)
	}
	square := inkBounds(render(LineCapSquare).im)
	if square.Min.X > 82 || square.Max.X < 318 {
		t.Fatalf("square cap should extend by half width, got %v", square)
	}
	round := inkBounds(render(LineCapRound).im)
	if round.Min.X > 82 || round.Max.X < 318 {
		t.Fatalf("round cap should extend by half width, got %v", round)
	}
}
