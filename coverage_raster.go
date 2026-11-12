package gg

import (
	"math"

	"github.com/golang/freetype/raster"
)

type coverageCell struct {
	x     int
	area  float64
	cover float64
}

// coverageRasterizer accumulates the exact area covered by each pixel. It is
// a scanline rasterizer: no off-screen image is allocated or downsampled.
type coverageRasterizer struct {
	rows    [][]coverageCell
	spanBuf []raster.Span
}

func (r *coverageRasterizer) reset(height int) {
	if cap(r.rows) < height {
		r.rows = make([][]coverageCell, height)
	} else {
		r.rows = r.rows[:height]
		for i := range r.rows {
			r.rows[i] = r.rows[i][:0]
		}
	}
}

func (r *coverageRasterizer) addCell(y, x int, area, cover float64) {
	if y < 0 || y >= len(r.rows) || area == 0 && cover == 0 {
		return
	}
	cells := r.rows[y]
	lo, hi := 0, len(cells)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if cells[mid].x < x {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(cells) && cells[lo].x == x {
		cells[lo].area += area
		cells[lo].cover += cover
		return
	}
	cells = append(cells, coverageCell{})
	copy(cells[lo+1:], cells[lo:])
	cells[lo] = coverageCell{x: x, area: area, cover: cover}
	r.rows[y] = cells
}

func (r *coverageRasterizer) addSegment(x0, y0, x1, y1 float64, direction float64) {
	if y0 == y1 {
		return
	}

	firstRow := int(math.Floor(math.Min(y0, y1)))
	lastRow := int(math.Ceil(math.Max(y0, y1))) - 1
	if firstRow < 0 {
		firstRow = 0
	}
	if lastRow >= len(r.rows) {
		lastRow = len(r.rows) - 1
	}

	for y := firstRow; y <= lastRow; y++ {
		row := float64(y)
		ya := math.Max(y0, row)
		yb := math.Min(y1, row+1)
		if ya >= yb {
			continue
		}

		xa := x0 + (x1-x0)*(ya-y0)/(y1-y0)
		xb := x0 + (x1-x0)*(yb-y0)/(y1-y0)
		dy := yb - ya
		r.addScan(y, xa, xb, dy, direction)
	}
}

func (r *coverageRasterizer) addScan(y int, x0, x1, dy, direction float64) {
	if dy == 0 {
		return
	}
	x0i := int(math.Floor(x0))
	x1i := int(math.Floor(x1))
	x0f := x0 - float64(x0i)
	x1f := x1 - float64(x1i)

	if x0i == x1i {
		r.addCell(y, x0i, direction*(x0f+x1f)*dy, direction*dy)
		return
	}

	if x1 > x0 {
		dx := x1 - x0
		firstDY := dy * (1 - x0f) / dx
		r.addCell(y, x0i, direction*(x0f+1)*firstDY, direction*firstDY)

		middleDY := dy / dx
		for x := x0i + 1; x < x1i; x++ {
			r.addCell(y, x, direction*middleDY, direction*middleDY)
		}

		lastDY := dy * x1f / dx
		r.addCell(y, x1i, direction*x1f*lastDY, direction*lastDY)
		return
	}

	dx := x0 - x1
	firstDY := dy * x0f / dx
	r.addCell(y, x0i, direction*x0f*firstDY, direction*firstDY)

	middleDY := dy / dx
	for x := x0i - 1; x > x1i; x-- {
		r.addCell(y, x, direction*middleDY, direction*middleDY)
	}

	lastDY := dy * (1 - x1f) / dx
	r.addCell(y, x1i, direction*(1+x1f)*lastDY, direction*lastDY)
}

func (r *coverageRasterizer) addPath(path raster.Path) {
	var cx, cy float64
	started := false
	addLine := func(x1, y1 float64) {
		if !started {
			cx, cy = x1, y1
			started = true
			return
		}
		endX, endY := x1, y1
		x0, y0 := cx, cy
		direction := 1.0
		if y1 < y0 {
			x0, x1 = x1, x0
			y0, y1 = y1, y0
			direction = -1
		}
		r.addSegment(x0, y0, x1, y1, direction)
		cx, cy = endX, endY
	}
	for i := 0; i < len(path); {
		switch path[i] {
		case 0:
			cx = unfix(path[i+1])
			cy = unfix(path[i+2])
			started = true
			i += 4
		case 1:
			x, y := unfix(path[i+1]), unfix(path[i+2])
			addLine(x, y)
			i += 4
		case 2:
			x1, y1 := unfix(path[i+1]), unfix(path[i+2])
			x2, y2 := unfix(path[i+3]), unfix(path[i+4])
			points := QuadraticBezier(cx, cy, x1, y1, x2, y2)
			for _, p := range points[1:] {
				addLine(p.X, p.Y)
			}
			i += 6
		case 3:
			x1, y1 := unfix(path[i+1]), unfix(path[i+2])
			x2, y2 := unfix(path[i+3]), unfix(path[i+4])
			x3, y3 := unfix(path[i+5]), unfix(path[i+6])
			points := CubicBezier(cx, cy, x1, y1, x2, y2, x3, y3)
			for _, p := range points[1:] {
				addLine(p.X, p.Y)
			}
			i += 8
		default:
			panic("bad path")
		}
	}
}

func coverageAlpha(area float64, evenOdd bool) uint32 {
	alpha := math.Abs(area)
	if evenOdd {
		alpha = math.Mod(alpha, 2)
		if alpha > 1 {
			alpha = 2 - alpha
		}
	} else if alpha > 1 {
		alpha = 1
	}
	if alpha <= 0 {
		return 0
	}
	return uint32(math.Round(alpha * 65535))
}

func (r *coverageRasterizer) spans(width int, evenOdd bool) []raster.Span {
	spans := r.spanBuf[:0]
	for y, cells := range r.rows {
		if len(cells) == 0 {
			continue
		}

		cover := 0.0
		xi := 0
		for _, cell := range cells {
			x := cell.x
			if x > xi && cover != 0 {
				alpha := coverageAlpha(cover, evenOdd)
				if alpha != 0 {
					spans = append(spans, raster.Span{Y: y, X0: xi, X1: x, Alpha: alpha})
				}
			}

			cover += cell.cover
			alpha := coverageAlpha(cover-cell.area/2, evenOdd)
			if alpha != 0 {
				x0, x1 := x, x+1
				if x0 < 0 {
					x0 = 0
				}
				if x1 > width {
					x1 = width
				}
				if x0 < x1 {
					spans = append(spans, raster.Span{Y: y, X0: x0, X1: x1, Alpha: alpha})
				}
			}
			xi = x + 1
		}
	}
	r.spanBuf = spans
	return spans
}
