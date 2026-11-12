// Text rendering demo: glyph raster cache with LRU bounds, sub-pixel
// positioning, kern table kerning and small-size supersampled anti-aliasing.
// It renders the same mixed Chinese/English paragraph at 12, 16 and 24 px and
// reports cache hit rate plus a memory footprint curve.
package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/fogleman/gg"
)

const (
	latinFont = "fonts/DejaVuSans.ttf"
	cjkFont   = "fonts/SimHei.ttf"

	paragraph = "The quick brown fox jumps over the lazy dog. " +
		"敏捷的棕色狐狸跳过了懒狗。Type AVATAR Teapot World, " +
		"中英混排 Mixed Text 0123456789."
)

type sample struct {
	size       float64
	hits       int64
	misses     int64
	hitRate    float64
	evictions  int64
	entries    int
	cacheBytes int
	heapBytes  uint64
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "demo failed:", err)
		os.Exit(1)
	}
}

func run() error {
	sizes := []float64{12, 16, 24}

	// Main comparison board.
	const pad, lineH = 30.0, 1.4
	W := 1280
	rowH := 170.0
	featureH := 200.0
	H := int(pad*2 + rowH*float64(len(sizes)) + featureH + 250)
	dc := gg.NewContext(W, H)
	dc.SetRGB(1, 1, 1)
	dc.Clear()

	dc.SetRGB(0.08, 0.10, 0.16)
	if err := dc.LoadFontFace(latinFont, 22); err != nil {
		return err
	}
	dc.DrawString("Text rendering pipeline demo", pad, pad+8)

	samples := make([]sample, 0, len(sizes))
	csv := "size_px,hits,misses,hit_rate,evictions,cache_entries,cache_bytes,heap_bytes\n"

	for i, size := range sizes {
		top := pad + 50 + float64(i)*rowH
		renderer, err := gg.LoadTextFontSet(size, 512, 2*1024*1024, latinFont, cjkFont)
		if err != nil {
			return err
		}
		row := gg.NewContext(int(W-2*pad), int(rowH-20))
		row.SetRGB(1, 1, 1)
		row.Clear()
		row.SetTextRenderer(renderer)
		row.SetRGB(0.1, 0.1, 0.1)

		// Same paragraph drawn three times: first pass fills the cache,
		// following passes are served from it.
		for line := 0; line < 3; line++ {
			y := size*lineH + float64(line)*size*lineH
			row.DrawString(paragraph, 4, y)
		}

		st := renderer.AggregateStats()
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		s := sample{
			size:       size,
			hits:       st.Hits,
			misses:     st.Misses,
			hitRate:    st.HitRate(),
			evictions:  st.Evictions,
			entries:    st.Entries,
			cacheBytes: st.Bytes,
			heapBytes:  ms.HeapAlloc,
		}
		samples = append(samples, s)
		csv += fmt.Sprintf("%g,%d,%d,%.4f,%d,%d,%d,%d\n",
			size, s.hits, s.misses, s.hitRate, s.evictions, s.entries, s.cacheBytes, s.heapBytes)

		dc.DrawImage(row.Image(), int(pad), int(top))
		dc.SetRGB(0.35, 0.35, 0.4)
		_ = dc.LoadFontFace(latinFont, 13)
		dc.DrawString(fmt.Sprintf("%gpx  hit %.1f%%  (%d hits / %d misses)  glyphs %d  cache %d B  evictions %d",
			size, 100*s.hitRate, s.hits, s.misses, s.entries, s.cacheBytes, s.evictions),
			pad+4, top+rowH-26)
	}

	// Long-running stress: feed a bounded cache a stream longer than its
	// capacity and record the memory curve to prove it plateaus.
	curve := stressMemoryCurve()

	featureTop := pad + 50 + float64(len(sizes))*rowH + 10
	drawFeaturePanel(dc, pad, featureTop, float64(W)-2*pad, featureH)

	chartTop := featureTop + featureH + 10
	drawMemoryChart(dc, pad, chartTop, float64(W)-2*pad, 190, curve)

	if err := dc.SavePNG("text_demo.png"); err != nil {
		return err
	}
	if err := os.WriteFile("text_demo_stats.csv", []byte(csv), 0644); err != nil {
		return err
	}
	if err := os.WriteFile("text_demo_memory.csv", curve.csv(), 0644); err != nil {
		return err
	}

	printReport(samples, curve)
	return nil
}

type curvePoint struct {
	iteration int
	cacheB    int
	heapKB    uint64
	entries   int
	evictions int64
}

type memoryCurve []curvePoint

func (m memoryCurve) csv() []byte {
	out := "iteration,cache_bytes,cache_entries,evictions,heap_kb\n"
	for _, p := range m {
		out += fmt.Sprintf("%d,%d,%d,%d,%d\n", p.iteration, p.cacheB, p.entries, p.evictions, p.heapKB)
	}
	return []byte(out)
}

// stressMemoryCurve runs 40 iterations of rotating text against a cache
// capped at 32 glyphs / 64 KB, sampling the footprint after every pass. The
// working set is larger than the cap, so LRU evictions happen continuously
// and the footprint must level off instead of growing without bound.
func stressMemoryCurve() memoryCurve {
	renderer, err := gg.LoadTextFontSet(16, 32, 64*1024, latinFont, cjkFont)
	if err != nil {
		panic(err)
	}
	curve := memoryCurve{}
	runes := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789敏捷棕色狐狸跳过懒狗中英混排文字渲染缓存测试字形光栅化亚像素定位")
	for iter := 1; iter <= 40; iter++ {
		dc := gg.NewContext(1400, 60)
		dc.SetTextRenderer(renderer)
		dc.SetRGB(0, 0, 0)
		// Rotate the rune window so each pass introduces some new glyphs,
		// forcing LRU evictions rather than pure hits.
		start := (iter * 7) % len(runes)
		line := make([]rune, 60)
		for i := range line {
			line[i] = runes[(start+i)%len(runes)]
		}
		dc.DrawString(string(line), 4, 40)

		st := renderer.AggregateStats()
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		curve = append(curve, curvePoint{
			iteration: iter,
			cacheB:    st.Bytes,
			entries:   st.Entries,
			evictions: st.Evictions,
			heapKB:    ms.HeapAlloc / 1024,
		})
	}
	return curve
}

// drawFeaturePanel renders side-by-side toggles proving each pipeline stage.
func drawFeaturePanel(dc *gg.Context, x, y, w, h float64) {
	dc.SetRGB(0.97, 0.97, 0.98)
	dc.DrawRectangle(x, y, w, h)
	dc.Fill()

	dc.SetRGB(0.08, 0.1, 0.16)
	_ = dc.LoadFontFace(latinFont, 15)
	dc.DrawString("Pipeline features (left: enabled, right: disabled)", x+10, y+20)

	colW := w / 3
	panelW := int(colW - 20)
	panelH := 64
	labelY1 := y + 36
	panelY1 := y + 42
	labelY2 := y + 118
	panelY2 := y + 124

	// Each column gets a fresh 11px renderer so toggles cannot leak.
	build := func() *gg.TextRenderer {
		r, err := gg.LoadTextFontSet(11, 256, 1<<20, latinFont, cjkFont)
		if err != nil {
			panic(err)
		}
		return r
	}

	// Column 1: sub-pixel pen vs integer pen.
	col1x := x + 10
	dc.SetRGB(0.25, 0.25, 0.3)
	_ = dc.LoadFontFace(latinFont, 11)
	dc.DrawString("sub-pixel pen", col1x, labelY1)
	blit(dc, build(), col1x, panelY1, panelW, panelH, func(c *gg.Context) {
		drawFractional(c, false)
	})
	dc.DrawString("integer pen (jitter)", col1x, labelY2)
	blit(dc, build(), col1x, panelY2, panelW, panelH, func(c *gg.Context) {
		drawFractional(c, true)
	})

	// Column 2: kerning on/off from the kern table.
	col2x := x + colW + 10
	dc.DrawString("kern on", col2x, labelY1)
	blit(dc, build(), col2x, panelY1, panelW, panelH, func(c *gg.Context) {
		c.TextRenderer().KerningEnabled = true
		c.DrawString("AVATAR Teapot World", 4, 24)
		c.DrawString("AVATAR Teapot World", 4, 50)
	})
	dc.DrawString("kern off", col2x, labelY2)
	blit(dc, build(), col2x, panelY2, panelW, panelH, func(c *gg.Context) {
		c.TextRenderer().KerningEnabled = false
		c.DrawString("AVATAR Teapot World", 4, 24)
		c.DrawString("AVATAR Teapot World", 4, 50)
	})

	// Column 3: supersampled AA on/off at 11px.
	col3x := x + colW*2 + 10
	dc.DrawString("small-size AA on", col3x, labelY1)
	blit(dc, build(), col3x, panelY1, panelW, panelH, func(c *gg.Context) {
		c.TextRenderer().SmallSizeAA = true
		c.DrawString("Small 字形 text 0123", 4, 24)
		c.DrawString("Small 字形 text 0123", 4, 50)
	})
	dc.DrawString("small-size AA off", col3x, labelY2)
	blit(dc, build(), col3x, panelY2, panelW, panelH, func(c *gg.Context) {
		c.TextRenderer().SmallSizeAA = false
		c.DrawString("Small 字形 text 0123", 4, 24)
		c.DrawString("Small 字形 text 0123", 4, 50)
	})
}

// blit creates a white sub-canvas, lets paint draw onto it with the given
// renderer and composites the result onto dc at (x, y).
func blit(dc *gg.Context, r *gg.TextRenderer, x, y float64, w, h int, paint func(c *gg.Context)) {
	sub := gg.NewContext(w, h)
	sub.SetRGB(1, 1, 1)
	sub.Clear()
	sub.SetRGB(0.1, 0.1, 0.1)
	sub.SetTextRenderer(r)
	paint(sub)
	dc.DrawImage(sub.Image(), int(x), int(y))
}

// drawFractional draws the same word four times with a 0.25px increasing x
// offset. Fractional pens move smoothly; integer snapping jumps whole pixels.
func drawFractional(c *gg.Context, integer bool) {
	const s = "Subpixel 字"
	for i := 0; i < 4; i++ {
		off := float64(i) * 0.4
		if integer {
			off = float64(int(off + 0.5))
		}
		c.DrawString(s, 4+off, 13+float64(i)*15)
	}
}

func drawMemoryChart(dc *gg.Context, x, y, w, h float64, curve memoryCurve) {
	dc.SetRGB(0.97, 0.97, 0.98)
	dc.DrawRectangle(x, y, w, h)
	dc.Fill()

	dc.SetRGB(0.08, 0.1, 0.16)
	_ = dc.LoadFontFace(latinFont, 15)
	dc.DrawString("Memory curve: bounded LRU cache (cap 32 glyphs / 64 KB) over 40 iterations", x+10, y+20)

	maxB := 1
	for _, p := range curve {
		if p.cacheB > maxB {
			maxB = p.cacheB
		}
	}
	plotX, plotY, plotW, plotH := x+50, y+40, w-70, h-70

	// Axes and gridlines.
	dc.SetRGB(0.75, 0.75, 0.78)
	for g := 0; g <= 4; g++ {
		gy := plotY + plotH*float64(g)/4
		dc.DrawLine(plotX, gy, plotX+plotW, gy)
		dc.Stroke()
		val := maxB * (4 - g) / 4
		dc.SetRGB(0.4, 0.4, 0.45)
		_ = dc.LoadFontFace(latinFont, 11)
		dc.DrawString(fmt.Sprintf("%d KB", val/1024), plotX-44, gy+4)
		dc.SetRGB(0.75, 0.75, 0.78)
	}
	dc.SetRGB(0.3, 0.3, 0.35)
	dc.DrawLine(plotX, plotY, plotX, plotY+plotH)
	dc.Stroke()
	dc.DrawLine(plotX, plotY+plotH, plotX+plotW, plotY+plotH)
	dc.Stroke()

	// Cache footprint polyline.
	dc.SetRGB(0.15, 0.45, 0.85)
	dc.SetLineWidth(2)
	for i, p := range curve {
		px := plotX + plotW*float64(i)/float64(len(curve)-1)
		py := plotY + plotH*(1-float64(p.cacheB)/float64(maxB))
		if i == 0 {
			dc.MoveTo(px, py)
		} else {
			dc.LineTo(px, py)
		}
	}
	dc.Stroke()
	dc.SetLineWidth(1)

	last := curve[len(curve)-1]
	first := curve[0]
	dc.SetRGB(0.15, 0.15, 0.2)
	_ = dc.LoadFontFace(latinFont, 12)
	dc.DrawString(fmt.Sprintf("cache bytes: %d -> %d (plateau), entries capped at %d, cumulative evictions: %d",
		first.cacheB, last.cacheB, last.entries, last.evictions), plotX, plotY+plotH+22)
}

func printReport(samples []sample, curve memoryCurve) {
	fmt.Println("== Cache hit rate per font size ==")
	for _, s := range samples {
		fmt.Printf("%2gpx: hit rate %5.1f%%  (%d hits / %d misses, %d glyphs, %d B cache, %d evictions)\n",
			s.size, 100*s.hitRate, s.hits, s.misses, s.entries, s.cacheBytes, s.evictions)
	}
	fmt.Println()
	fmt.Println("== Memory curve (bounded cache, 40 iterations) ==")
	for i := 0; i < len(curve); i += 5 {
		p := curve[i]
		fmt.Printf("iter %2d: cache %6d B (%3d glyphs), heap %6d KB\n",
			p.iteration, p.cacheB, p.entries, p.heapKB)
	}
	last := curve[len(curve)-1]
	fmt.Printf("iter %2d: cache %6d B (%3d glyphs), heap %6d KB\n",
		last.iteration, last.cacheB, last.entries, last.heapKB)
	fmt.Println()
	fmt.Println("outputs: text_demo.png, text_demo_stats.csv, text_demo_memory.csv")
}
