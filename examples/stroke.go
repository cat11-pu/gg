// This program renders reference images for the stroke engine: joins, caps,
// miter-limit fallback, the acute-triangle regression and variable-width
// strokes (including a self-intersecting path).
package main

import (
	"fmt"
	"math"

	"github.com/fogleman/gg"
)

const white = 1.0

func newImage(w, h int) *gg.Context {
	dc := gg.NewContext(w, h)
	dc.SetRGB(white, white, white)
	dc.Clear()
	dc.SetRGB(0, 0, 0)
	return dc
}

// joins renders miter, round and bevel joins side by side.
func joins() {
	const W, H = 1800, 700
	dc := newImage(W, H)
	dc.SetLineWidth(34)
	dc.SetLineCapButt()
	joins := []struct {
		name string
		join gg.LineJoin
	}{
		{"miter", gg.LineJoinMiter},
		{"round", gg.LineJoinRound},
		{"bevel", gg.LineJoinBevel},
	}
	for i, j := range joins {
		cx := 300 + float64(i)*600
		dc.SetLineJoin(j.join)
		dc.NewSubPath()
		dc.MoveTo(cx-200, 420)
		dc.LineTo(cx, 180)
		dc.LineTo(cx+200, 420)
		dc.Stroke()
		dc.DrawString(j.name, cx-40, 600)
	}
	dc.SavePNG("stroke_joins.png")
}

// caps renders butt, round and square caps side by side.
func caps() {
	const W, H = 1800, 400
	dc := newImage(W, H)
	dc.SetLineWidth(60)
	caps := []struct {
		name string
		cap  gg.LineCap
	}{
		{"butt", gg.LineCapButt},
		{"round", gg.LineCapRound},
		{"square", gg.LineCapSquare},
	}
	for i, c := range caps {
		x0 := 150 + float64(i)*600
		dc.SetLineCap(c.cap)
		dc.DrawLine(x0, 200, x0+350, 200)
		dc.Stroke()
		dc.DrawString(c.name, x0+120, 340)
	}
	dc.SavePNG("stroke_caps.png")
}

// miterLimit shows the automatic miter-to-bevel fallback as the corner angle
// gets sharper than the default miter limit.
func miterLimit() {
	const W, H = 2400, 700
	dc := newImage(W, H)
	dc.SetLineWidth(26)
	dc.SetLineCapButt()
	dc.SetLineJoinMiter()
	for i, deg := range []float64{60, 20, 11, 8} {
		cx := 280 + float64(i)*580
		cy := 320.0
		arm := 220.0
		half := gg.Radians(deg / 2)
		apex := gg.Point{X: cx, Y: cy}
		p1 := gg.Point{X: cx - arm*math.Cos(half), Y: cy + arm*math.Sin(half)}
		p2 := gg.Point{X: cx - arm*math.Cos(half), Y: cy - arm*math.Sin(half)}
		dc.NewSubPath()
		dc.MoveTo(p1.X, p1.Y)
		dc.LineTo(apex.X, apex.Y)
		dc.LineTo(p2.X, p2.Y)
		dc.Stroke()
		dc.DrawString(fmt.Sprintf("%g deg", deg), cx-45, 580)
	}
	dc.SavePNG("stroke_miter_limit.png")
}

// acuteTriangle reproduces the original bug report: a ~10 degree triangle
// stroked with width 8, rendered once per join style.
func acuteTriangle() {
	const W, H = 1500, 1000
	for _, j := range []struct {
		name string
		join gg.LineJoin
	}{
		{"miter", gg.LineJoinMiter},
		{"round", gg.LineJoinRound},
		{"bevel", gg.LineJoinBevel},
	} {
		dc := newImage(W, H)
		dc.SetLineWidth(8)
		dc.SetLineJoin(j.join)
		dc.MoveTo(200, 500)
		dc.LineTo(1100, 240)
		dc.LineTo(1100, 760)
		dc.ClosePath()
		dc.Stroke()
		dc.SavePNG("stroke_triangle_" + j.name + ".png")
	}
}

// variableWidth strokes an S-curve whose width varies smoothly along the
// path and tapers at both ends.
func variableWidth() {
	const W, H = 1600, 900
	dc := newImage(W, H)
	dc.SetLineCapRound()
	dc.SetLineJoinRound()
	dc.SetLineWidthFunc(func(t float64) float64 {
		return 1 + 46*math.Pow(math.Sin(math.Pi*t), 1.6)
	})
	// A horizontal S-curve built from two cubic Beziers. The width function
	// is evaluated along the smoothly flattened path.
	dc.MoveTo(120, 650)
	dc.CubicTo(500, 650, 620, 250, 1000, 250)
	dc.CubicTo(1380, 250, 1100, 650, 1480, 650)
	dc.Stroke()
	dc.SavePNG("stroke_variable_width.png")
}

// variableWidthSelf strokes a four-petal rose (r = cos(2theta)) whose lobes
// cross at the center. The outline stays compact with no stray spikes.
func variableWidthSelf() {
	const S = 1200
	dc := newImage(S, S)
	dc.SetLineJoinRound()
	dc.SetLineCapRound()
	dc.SetLineWidthFunc(func(t float64) float64 {
		return 8 + 26*math.Pow(math.Sin(math.Pi*t), 1.2)
	})
	const cx, cy, R = 600.0, 600.0, 460.0
	m := 720
	var first gg.Point
	for i := 0; i <= m; i++ {
		t := float64(i) / float64(m)
		theta := 2 * math.Pi * t
		r := R * math.Cos(2*theta)
		p := gg.Point{X: cx + r*math.Cos(theta), Y: cy + r*math.Sin(theta)}
		if i == 0 {
			dc.MoveTo(p.X, p.Y)
			first = p
		} else {
			dc.LineTo(p.X, p.Y)
		}
	}
	dc.LineTo(first.X, first.Y)
	dc.Stroke()
	dc.SavePNG("stroke_variable_self.png")
}

func main() {
	joins()
	caps()
	miterLimit()
	acuteTriangle()
	variableWidth()
	variableWidthSelf()
}
