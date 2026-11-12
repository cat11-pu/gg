package gg

import (
	"github.com/golang/freetype/raster"
)

// ditherablePattern is implemented by patterns that can provide their
// un-quantized, gamma-correct color as 8-bit sRGB channels. The pattern
// painter uses it to apply error-diffusion dithering while compositing,
// which keeps the gradients smooth on 8-bit (and banding-prone) surfaces.
type ditherablePattern interface {
	Pattern
	// colorAtFloat returns the ideal gradient color for the given pixel as
	// straight (non-premultiplied) sRGB channels. RGB is un-quantized (the
	// value a 16-bit surface would hold); alpha is an 8-bit coverage level.
	colorAtFloat(x, y int) (r, g, b float64, a uint8)
}

// fsDitherer holds the Floyd-Steinberg error-diffusion state while a single
// paint operation rasterizes a gradient. The freetype rasterizer emits spans
// top to bottom and left to right, which is the scan order the algorithm
// requires. One ditherer is created per Fill/Stroke, so the state never
// leaks between independent draws.
type fsDitherer struct {
	p ditherablePattern
	w int
	// cur holds error diffused into the scanline being processed; next
	// accumulates the downward weights and becomes cur after each row.
	cur, next [][3]float64
	lastY     int
}

func newFSDitherer(p ditherablePattern, width int) *fsDitherer {
	return &fsDitherer{
		p:     p,
		w:     width,
		cur:   make([][3]float64, width+2),
		next:  make([][3]float64, width+2),
		lastY: -1,
	}
}

func clamp8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}

// pixel applies one Floyd-Steinberg step. It returns the quantized,
// straight-alpha sRGB color to composite for pixel (x, y).
func (d *fsDitherer) pixel(x, y int) (r, g, b, a uint8) {
	if y != d.lastY {
		// Advancing to a new scanline: the downward-error buffer becomes the
		// current row, and a fresh buffer starts collecting the next row.
		if d.lastY >= 0 {
			d.cur, d.next = d.next, d.cur
			for i := range d.next {
				d.next[i] = [3]float64{}
			}
		}
		d.lastY = y
	}

	sr, sg, sb, sa := d.p.colorAtFloat(x, y)

	// Fully transparent pixels contribute no color; skip error diffusion so
	// the feathered radial edge does not bleed noise into empty regions. The
	// rightward carry still propagates so the next pixel in the span is not
	// fed stale error.
	if sa == 0 {
		d.cur[x+1][0] += d.cur[x][0] * 7.0 / 16.0
		d.cur[x+1][1] += d.cur[x][1] * 7.0 / 16.0
		d.cur[x+1][2] += d.cur[x][2] * 7.0 / 16.0
		d.cur[x] = [3]float64{}
		return 0, 0, 0, 0
	}

	// Error is carried only for the color channels; alpha is composited
	// exactly so coverage stays untouched.
	fr := float64(sr) + d.cur[x][0]
	fg := float64(sg) + d.cur[x][1]
	fb := float64(sb) + d.cur[x][2]

	qr := clamp8(fr)
	qg := clamp8(fg)
	qb := clamp8(fb)

	er := fr - float64(qr)
	eg := fg - float64(qg)
	eb := fb - float64(qb)

	// Floyd-Steinberg weights: right 7/16, below-left 3/16, below 5/16,
	// below-right 1/16.
	if x+1 < d.w {
		d.cur[x+1][0] += er * 7.0 / 16.0
		d.cur[x+1][1] += eg * 7.0 / 16.0
		d.cur[x+1][2] += eb * 7.0 / 16.0
	}
	if x > 0 {
		d.next[x-1][0] += er * 3.0 / 16.0
		d.next[x-1][1] += eg * 3.0 / 16.0
		d.next[x-1][2] += eb * 3.0 / 16.0
	}
	d.next[x][0] += er * 5.0 / 16.0
	d.next[x][1] += eg * 5.0 / 16.0
	d.next[x][2] += eb * 5.0 / 16.0
	if x+1 < d.w {
		d.next[x+1][0] += er / 16.0
		d.next[x+1][1] += eg / 16.0
		d.next[x+1][2] += eb / 16.0
	}

	return qr, qg, qb, sa
}

// paintDithered runs a full pattern-painter pass with error diffusion. It
// mirrors patternPainter.Paint but quantizes the gradient through the
// ditherer instead of truncating each channel independently. The ditherer is
// shared across spans because the rasterizer can split one scanline into
// several Paint calls; recreating the error state per span would corrupt the
// diffusion and darken partial-coverage edges.
func (r *patternPainter) paintDithered(ss []raster.Span) {
	im, mask, pattern := r.im, r.mask, r.p.(ditherablePattern)
	b := im.Bounds()
	if r.ditherer == nil {
		r.ditherer = newFSDitherer(pattern, im.Rect.Dx())
	}
	d := r.ditherer
	for _, s := range ss {
		if s.Y < b.Min.Y {
			continue
		}
		if s.Y >= b.Max.Y {
			return
		}
		if s.X0 < b.Min.X {
			s.X0 = b.Min.X
		}
		if s.X1 > b.Max.X {
			s.X1 = b.Max.X
		}
		if s.X0 >= s.X1 {
			continue
		}
		y := s.Y - im.Rect.Min.Y
		i := y*im.Stride + (s.X0-im.Rect.Min.X)*4
		for x := s.X0 - im.Rect.Min.X; x < s.X1-im.Rect.Min.X; x++ {
			ma := s.Alpha
			if mask != nil {
				ma = ma * uint32(mask.AlphaAt(x, y).A) / 255
				if ma == 0 {
					i += 4
					continue
				}
			}
			cr, cg, cb, ca := d.pixel(x, y)
			// Straight-alpha source-over in a uniform 8-bit domain. The
			// ditherer's color and the span coverage are both 8-bit, so this
			// keeps the units consistent; uint64 prevents product wrap near
			// bright, low-coverage edges (a uint32/uint8 formula wraps there).
			cover := uint64(ma) >> 8
			srcA := uint64(ca) * cover / 255
			inv := 255 - srcA
			dr, dg, db, da := uint64(im.Pix[i+0]), uint64(im.Pix[i+1]), uint64(im.Pix[i+2]), uint64(im.Pix[i+3])
			im.Pix[i+0] = uint8((uint64(cr)*srcA + dr*inv) / 255)
			im.Pix[i+1] = uint8((uint64(cg)*srcA + dg*inv) / 255)
			im.Pix[i+2] = uint8((uint64(cb)*srcA + db*inv) / 255)
			im.Pix[i+3] = uint8((uint64(ca)*cover + da*inv) / 255)
			i += 4
		}
	}
}
