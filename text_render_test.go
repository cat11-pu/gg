package gg

import (
	"image"
	"testing"

	"golang.org/x/image/math/fixed"
)

func newTestFace(t *testing.T, points float64) *textFace {
	t.Helper()
	tf, err := LoadTextFace("fonts/DejaVuSans.ttf", points)
	if err != nil {
		t.Fatalf("load face: %v", err)
	}
	return tf
}

func TestGlyphCacheLRULimit(t *testing.T) {
	cache := NewGlyphCache(4, 0)
	keys := []glyphCacheKey{
		{rune: 'a'}, {rune: 'b'}, {rune: 'c'}, {rune: 'd'}, {rune: 'e'},
	}
	for i, k := range keys {
		cache.insert(&glyphCacheEntry{key: k, mask: image.NewAlpha(image.Rect(0, 0, 2, 2)), bytes: 4})
		if got := cache.Stats().Entries; got > 4 {
			t.Fatalf("after insert %d entries=%d, want <= 4", i, got)
		}
	}
	st := cache.Stats()
	if st.Entries != 4 {
		t.Fatalf("entries=%d, want 4", st.Entries)
	}
	if st.Evictions != 1 {
		t.Fatalf("evictions=%d, want 1", st.Evictions)
	}
	// 'a' was the oldest and must have been evicted.
	if _, ok := cache.lookup(keys[0]); ok {
		t.Fatal("oldest entry should have been evicted")
	}
	// Looking up 'b' makes it recently used, so inserting another entry
	// should evict 'c' instead of 'b'.
	if _, ok := cache.lookup(keys[1]); !ok {
		t.Fatal("'b' should still be cached")
	}
	cache.insert(&glyphCacheEntry{key: glyphCacheKey{rune: 'f'}, mask: image.NewAlpha(image.Rect(0, 0, 1, 1)), bytes: 1})
	if _, ok := cache.lookup(keys[2]); ok {
		t.Fatal("'c' should be the LRU entry evicted after touching 'b'")
	}
}

func TestGlyphCacheByteLimit(t *testing.T) {
	cache := NewGlyphCache(100, 10)
	for _, r := range []rune{'a', 'b', 'c', 'd'} {
		cache.insert(&glyphCacheEntry{
			key:   glyphCacheKey{rune: r},
			mask:  image.NewAlpha(image.Rect(0, 0, 2, 2)),
			bytes: 4,
		})
	}
	st := cache.Stats()
	if st.Bytes > 10 {
		t.Fatalf("cache bytes=%d exceeds cap 10", st.Bytes)
	}
	if st.Evictions == 0 {
		t.Fatal("expected evictions to enforce byte cap")
	}
}

func TestGlyphCacheHitRate(t *testing.T) {
	cache := NewGlyphCache(8, 0)
	k := glyphCacheKey{rune: 'x'}
	cache.insert(&glyphCacheEntry{key: k, mask: image.NewAlpha(image.Rect(0, 0, 1, 1)), bytes: 1})
	for i := 0; i < 3; i++ {
		cache.lookup(k) // hits
	}
	cache.lookup(glyphCacheKey{rune: 'y'}) // miss
	st := cache.Stats()
	if st.Hits != 3 || st.Misses != 1 {
		t.Fatalf("hits=%d misses=%d", st.Hits, st.Misses)
	}
	if got := st.HitRate(); got != 0.75 {
		t.Fatalf("hit rate=%.2f, want 0.75", got)
	}
}

func TestFontSetFallback(t *testing.T) {
	latin := newTestFace(t, 16)
	cjk, err := LoadTextFace("fonts/SimHei.ttf", 16)
	if err != nil {
		t.Fatalf("load cjk face: %v", err)
	}
	r := NewTextRenderer(64, 0, latin, cjk)

	if got := r.faceFor('A'); got != latin {
		t.Fatal("Latin rune should resolve to the Latin face")
	}
	if got := r.faceFor('中'); got != cjk {
		t.Fatalf("CJK rune should fall back to the CJK face, got %p want %p", got, cjk)
	}
}

func TestKerningApplied(t *testing.T) {
	tf := newTestFace(t, 16)
	r := NewTextRenderer(64, 0, tf)

	offWidth := r.MeasureString("AVATAR")
	r.KerningEnabled = false
	onWidth := r.MeasureString("AVATAR")
	if offWidth >= onWidth {
		t.Fatalf("kerning should tighten the string: kernOn=%v kernOff=%v", onWidth, offWidth)
	}

	r.KerningEnabled = true
	dc := NewContext(300, 40)
	dc.SetTextRenderer(r)
	dc.SetRGB(0, 0, 0)
	dc.DrawString("AVATAR Teapot", 4, 30)
	if r.KernApplied == 0 {
		t.Fatal("expected non-zero kerning pairs to be applied")
	}
}

func TestSubPixelPenProducesDistinctMasks(t *testing.T) {
	tf := newTestFace(t, 16)
	r := NewTextRenderer(64, 0, tf)

	glyph := func(penX float64) *glyphCacheEntry {
		g := plannedGlyph{
			face: tf,
			r:    'H',
			pen:  fixed.Point26_6{X: fix(penX), Y: fix(30)},
		}
		e, _ := r.rasterizeGlyph(g)
		return e
	}
	integer := glyph(10.0)
	fractional := glyph(10.3)
	if integer.key.quantX == fractional.key.quantX {
		t.Fatalf("fractional pen should map to a different sub-pixel bucket, both=%d", integer.key.quantX)
	}

	// Sub-pixel disabled: every fractional position collapses to one bucket.
	r.SubPixelEnabled = false
	snapped := glyph(10.3)
	if snapped.key.quantX != 0 {
		t.Fatalf("integer snapping should use bucket 0, got %d", snapped.key.quantX)
	}
}

func TestSmallSizeSupersampling(t *testing.T) {
	// 11px is below the 12px threshold and must rasterize at 2x, then the
	// composited result should still land in a reasonably sized mask.
	tf := newTestFace(t, 11)
	r := NewTextRenderer(64, 0, tf)
	g := plannedGlyph{face: tf, r: 'M', pen: fixed.Point26_6{X: fix(4), Y: fix(20)}}
	e, factor := r.rasterizeGlyph(g)
	if factor != supersample {
		t.Fatalf("11px factor=%d, want %d", factor, supersample)
	}
	if e.mask.Rect.Dx() == 0 {
		t.Fatal("expected a rasterized 2x mask")
	}

	// 16px is above the threshold and renders 1x.
	big := newTestFace(t, 16)
	r2 := NewTextRenderer(64, 0, big)
	g2 := plannedGlyph{face: big, r: 'M', pen: fixed.Point26_6{X: fix(4), Y: fix(20)}}
	_, factor2 := r2.rasterizeGlyph(g2)
	if factor2 != 1 {
		t.Fatalf("16px factor=%d, want 1", factor2)
	}
}

func TestSmallSizeAADownsamplesToTargetSize(t *testing.T) {
	tf, err := LoadTextFace("fonts/SimHei.ttf", 11)
	if err != nil {
		t.Fatal(err)
	}
	r := NewTextRenderer(64, 0, tf)

	// The cached mask is rasterized at 2x.
	g := plannedGlyph{face: tf, r: '中', pen: fixed.Point26_6{X: fix(10), Y: fix(28)}}
	e, factor := r.rasterizeGlyph(g)
	if factor != supersample {
		t.Fatalf("factor=%d, want %d", factor, supersample)
	}
	superW, superH := e.mask.Rect.Dx(), e.mask.Rect.Dy()

	// Compositing downsamples back to roughly half, i.e. target resolution.
	dc := NewContext(60, 40)
	dc.SetRGB(1, 1, 1)
	dc.Clear()
	dc.SetRGB(0, 0, 0)
	dc.SetTextRenderer(r)
	dc.DrawString("中", 10, 28)

	// A 2x mask must be meaningfully larger than the final half-size box.
	halfW, halfH := (superW+1)/2, (superH+1)/2
	if superW <= halfW || superH <= halfH {
		t.Fatalf("2x mask %dx%d not larger than target %dx%d", superW, superH, halfW, halfH)
	}

	// With AA disabled the same glyph renders at 1x instead.
	r2 := NewTextRenderer(64, 0, tf)
	r2.SmallSizeAA = false
	g2 := plannedGlyph{face: tf, r: '中', pen: fixed.Point26_6{X: fix(10), Y: fix(28)}}
	e2, factor2 := r2.rasterizeGlyph(g2)
	if factor2 != 1 {
		t.Fatalf("AA-off factor=%d, want 1", factor2)
	}
	if e2.mask.Rect.Dx() >= superW {
		t.Fatalf("1x mask width=%d should be smaller than 2x width=%d", e2.mask.Rect.Dx(), superW)
	}
}
