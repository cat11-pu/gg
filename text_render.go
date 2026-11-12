package gg

import (
	"image"
	"image/color"
	stdraw "image/draw"

	"github.com/golang/freetype/truetype"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/math/f64"
	"golang.org/x/image/math/fixed"
)

// SubPixelsX is the number of sub-pixel positions a glyph is quantized to
// horizontally when cached. 4 means 1/4-pixel precision, which removes the
// jitter of small-size layouts while keeping the cache effective.
const SubPixelsX = 4

// SubPixelsY is the vertical sub-pixel quantization. Baselines usually snap to
// the pixel grid, so a single vertical position is enough.
const SubPixelsY = 1

// SmallSizeThreshold enables supersampled anti-aliasing for every font size
// strictly below this value (in points), so 12px-and-below text stays legible.
const SmallSizeThreshold = 12.0

// supersample is the rasterization multiplier used for small sizes.
const supersample = 2

// textFace bundles a font face at the target point size with a 2x variant
// used for small-size anti-aliasing, and a dedicated LRU glyph cache. Keeping
// one cache per face means cache keys never have to identify the font.
type textFace struct {
	// face is the font at the requested point size.
	face font.Face
	// face2x is the same font rasterized at twice the point size. It is nil
	// when small-size supersampling is unavailable.
	face2x font.Face
	// font is the parsed TrueType font, used for reliable coverage checks.
	font  *truetype.Font
	cache *GlyphCache
	// size is the requested point size.
	size float64
}

// faceAt returns the rasterization face for the given supersampling factor.
func (tf *textFace) faceAt(factor int) font.Face {
	if factor == supersample && tf.face2x != nil {
		return tf.face2x
	}
	return tf.face
}

// covers reports whether the face contains a real glyph for r. A TrueType
// glyph index of 0 is the .notdef glyph, i.e. the rune is unsupported; this
// avoids relying on font.Face.GlyphAdvance, which returns ok=true even for
// unsupported runes on common faces.
func (tf *textFace) covers(r rune) bool {
	if tf.font != nil {
		return tf.font.Index(r) != 0
	}
	_, ok := tf.face.GlyphAdvance(r)
	return ok
}

// TextRenderer draws text with a cached, sub-pixel positioned glyph pipeline.
// It wraps one or more textFace objects: the first face that contains a rune
// is used, which makes mixed-script (e.g. Chinese + English) text work with a
// Latin face and a CJK fallback face.
//
// As with font.Face, a TextRenderer is not safe for concurrent use across
// goroutines; its per-face GlyphCache objects themselves are locked.
type TextRenderer struct {
	faces []*textFace

	// KerningEnabled turns on the kerning data shipped in the font's kern
	// table (read through font.Face.Kern).
	KerningEnabled bool
	// SubPixelEnabled keeps fractional pen positions instead of rounding each
	// glyph to an integer pixel position.
	SubPixelEnabled bool
	// SmallSizeAA enables 2x supersampled anti-aliasing below
	// SmallSizeThreshold points.
	SmallSizeAA bool

	// KernApplied counts the non-zero kerning adjustments applied so far.
	KernApplied int64
}

// NewTextRenderer builds a TextRenderer. Each face gets its own glyph cache
// capped at maxEntries glyphs and maxBytes of mask memory.
func NewTextRenderer(maxEntries, maxBytes int, faces ...*textFace) *TextRenderer {
	for _, tf := range faces {
		if tf.cache == nil {
			tf.cache = NewGlyphCache(maxEntries, maxBytes)
		}
	}
	return &TextRenderer{
		faces:           faces,
		KerningEnabled:  true,
		SubPixelEnabled: true,
		SmallSizeAA:     true,
	}
}

// Caches returns every per-face glyph cache for stats and reconfiguration.
func (r *TextRenderer) Caches() []*GlyphCache {
	out := make([]*GlyphCache, 0, len(r.faces))
	for _, tf := range r.faces {
		out = append(out, tf.cache)
	}
	return out
}

// AggregateStats sums the stats of all per-face caches.
func (r *TextRenderer) AggregateStats() GlyphCacheStats {
	var s GlyphCacheStats
	for _, c := range r.Caches() {
		st := c.Stats()
		s.Hits += st.Hits
		s.Misses += st.Misses
		s.Evictions += st.Evictions
		s.Entries += st.Entries
		s.Bytes += st.Bytes
		s.MaxBytes += st.MaxBytes
	}
	return s
}

// faceFor returns the first face that can render r.
func (r *TextRenderer) faceFor(ch rune) *textFace {
	for _, tf := range r.faces {
		if tf.covers(ch) {
			return tf
		}
	}
	if len(r.faces) > 0 {
		return r.faces[0]
	}
	return nil
}

type plannedGlyph struct {
	face *textFace
	r    rune
	pen  fixed.Point26_6
}

// plan runs the text layout pass: per-rune face selection, kerning from the
// font tables and a fractional (sub-pixel) pen. No rasterization happens
// here, so layout and rendering stay independent.
func (r *TextRenderer) plan(s string, x, y float64) []plannedGlyph {
	pen := fixed.Point26_6{X: fix(x), Y: fix(y)}
	out := make([]plannedGlyph, 0, len(s))
	var prevFace *textFace
	var prev rune = -1
	for _, c := range s {
		tf := r.faceFor(c)
		if tf == nil {
			continue
		}
		if r.KerningEnabled && prev >= 0 && tf == prevFace {
			if k := tf.face.Kern(prev, c); k != 0 {
				pen.X += k
				r.KernApplied++
			}
		}
		adv, ok := tf.face.GlyphAdvance(c)
		if !ok {
			prev = c
			prevFace = tf
			continue
		}
		out = append(out, plannedGlyph{face: tf, r: c, pen: pen})
		pen.X += adv
		prev = c
		prevFace = tf
	}
	return out
}

// measure returns the advance width of s in 26.6 fixed point units.
func (r *TextRenderer) measure(s string) fixed.Int26_6 {
	var width fixed.Int26_6
	var prevFace *textFace
	var prev rune = -1
	for _, c := range s {
		tf := r.faceFor(c)
		if tf == nil {
			continue
		}
		if r.KerningEnabled && prev >= 0 && tf == prevFace {
			width += tf.face.Kern(prev, c)
		}
		if adv, ok := tf.face.GlyphAdvance(c); ok {
			width += adv
		}
		prev = c
		prevFace = tf
	}
	return width
}

// rasterizeGlyph returns a cached alpha mask and placement data for the
// glyph, rasterizing it through the underlying font.Face only on cache miss.
func (r *TextRenderer) rasterizeGlyph(g plannedGlyph) (*glyphCacheEntry, int) {
	factor := 1
	if r.SmallSizeAA && g.face.size < SmallSizeThreshold {
		factor = supersample
	}
	quantX, quantY := 0, 0
	if r.SubPixelEnabled {
		quantX = int((g.pen.X & 63) * SubPixelsX / 64)
		quantY = int((g.pen.Y & 63) * SubPixelsY / 64)
	}
	key := glyphCacheKey{rune: g.r, quantX: quantX, quantY: quantY, scale: factor}
	if e, ok := g.face.cache.lookup(key); ok {
		return e, factor
	}

	face := g.face.faceAt(factor)
	dot := fixed.Point26_6{}
	if r.SubPixelEnabled {
		dot.X = fixed.Int26_6(quantX * 64 / SubPixelsX * factor)
		dot.Y = fixed.Int26_6(quantY * 64 / SubPixelsY * factor)
	}
	dr, mask, maskp, _, ok := face.Glyph(dot, g.r)
	if !ok {
		return nil, factor
	}
	alpha := copyAlpha(mask, dr, maskp)
	e := &glyphCacheEntry{
		key:     key,
		mask:    alpha,
		bearing: dr.Min,
		bytes:   alphaSize(alpha.Rect.Dx(), alpha.Rect.Dy()),
	}
	g.face.cache.insert(e)
	return e, factor
}

// copyAlpha extracts the glyph's coverage mask into an independent image,
// because a font.Face may reuse its mask buffer on the next Glyph call.
func copyAlpha(mask image.Image, dr image.Rectangle, maskp image.Point) *image.Alpha {
	w, h := dr.Dx(), dr.Dy()
	if w <= 0 || h <= 0 {
		return image.NewAlpha(image.Rectangle{})
	}
	out := image.NewAlpha(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			_, _, _, a := mask.At(maskp.X+x, maskp.Y+y).RGBA()
			out.SetAlpha(x, y, color.Alpha{A: uint8((a + 128) / 257)})
		}
	}
	return out
}

// drawGlyph composites one planned glyph onto dst through the context matrix.
func (r *TextRenderer) drawGlyph(dst *image.RGBA, src image.Image, g plannedGlyph, matrix Matrix) {
	e, factor := r.rasterizeGlyph(g)
	if e == nil || e.mask.Rect.Empty() {
		return
	}

	mask := e.mask
	bearingX, bearingY := e.bearing.X, e.bearing.Y
	if factor == supersample {
		// Downscale the 2x mask back to target resolution with bilinear
		// interpolation, giving the small glyph extra edge coverage.
		w, h := mask.Rect.Dx(), mask.Rect.Dy()
		small := image.NewAlpha(image.Rect(0, 0, (w+1)/2, (h+1)/2))
		draw.ApproxBiLinear.Scale(small, small.Rect, mask, mask.Rect, stdraw.Src, nil)
		mask = small
		bearingX /= 2
		bearingY /= 2
	}

	// Glyph origin (pen) plus integer bearing. The fractional pen remainder
	// is carried by the affine transform, which yields sub-pixel positioning.
	fx := unfix(g.pen.X) + float64(bearingX)
	fy := unfix(g.pen.Y) + float64(bearingY)
	m := matrix.Translate(fx, fy)
	s2d := f64.Aff3{m.XX, m.XY, m.X0, m.YX, m.YY, m.Y0}
	draw.BiLinear.Transform(dst, s2d, src, mask.Rect, stdraw.Over, &draw.Options{
		SrcMask:  mask,
		SrcMaskP: image.ZP,
	})
}

// drawString renders s onto im at (x, y), using the given transform matrix.
func (r *TextRenderer) drawString(im *image.RGBA, src image.Image, s string, x, y float64, matrix Matrix) {
	for _, g := range r.plan(s, x, y) {
		r.drawGlyph(im, src, g, matrix)
	}
}

// MeasureString returns the rendered width of s in pixels.
func (r *TextRenderer) MeasureString(s string) float64 {
	return unfix(r.measure(s))
}
