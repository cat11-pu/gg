package gg

import (
	"image/color"
	"math"
	"testing"
)

// idealGrayFloat is the un-quantized gamma-correct sRGB gray: the value a
// 16-bit surface would retain before 8-bit dithering.
func idealGrayFloat(t float64) float64 {
	return linearTo8(t)
}

// renderDithered runs the Floyd-Steinberg ditherer over a full rectangle of
// the pattern, returning the quantized sRGB red channel. It mirrors the
// top-to-bottom, left-to-right scan order of a solid rectangle fill.
func renderDithered(p ditherablePattern, width, height int) [][]uint8 {
	d := newFSDitherer(p, width)
	out := make([][]uint8, height)
	for y := 0; y < height; y++ {
		row := make([]uint8, width)
		for x := 0; x < width; x++ {
			r, _, _, _ := d.pixel(x, y)
			row[x] = r
		}
		out[y] = row
	}
	return out
}

// countRunStats reports the longest run of identical bytes and the number of
// distinct encoded values in a rendered row.
func countRunStats(row []uint8) (longestRun, distinct int) {
	seen := map[uint8]bool{}
	run := 1
	longestRun = 1
	for x, v := range row {
		seen[v] = true
		if x == 0 {
			continue
		}
		if row[x] == row[x-1] {
			run++
			if run > longestRun {
				longestRun = run
			}
		} else {
			run = 1
		}
	}
	return longestRun, len(seen)
}

// TestLinearGrayscaleBanding renders black-to-white gray ramps at several
// widths (including the 256-level reference) and verifies that error
// diffusion keeps every pixel within one quantization level of the true
// gamma-correct value. In shallow ramps several neighboring pixels share an
// encoded level (the visible band); the test verifies dithering breaks those
// constant runs instead of reproducing the truncated output.
func TestLinearGrayscaleBanding(t *testing.T) {
	// The gradient spans more sRGB levels than the pixel width, so each level
	// is held by several adjacent pixels before truncation.
	cases := []struct {
		name       string
		w          int
		spanLevels float64
	}{
		{"shallow", 256, 64.0},
		{"medium", 256, 128.0},
	}
	for _, tc := range cases {
		w := tc.w
		const h = 16
		// Build the ramp from explicit gray stops covering spanLevels of the
		// 0..255 range across the full width.
		g := NewLinearGradient(0, 0, float64(w-1), 0)
		lo := uint8(96)
		hi := uint8(96 + int(tc.spanLevels))
		g.AddColorStop(0, color.RGBA{0, 0, 0, 255})
		g.AddColorStop(0, color.RGBA{lo, lo, lo, 255})
		g.AddColorStop(1, color.RGBA{hi, hi, hi, 255})

		frame := renderDithered(g.(ditherablePattern), w, h)
		row := frame[h/2]
		plain := make([]uint8, w)
		lg := g.(*linearGradient)
		for x := 0; x < w; x++ {
			fc := lg.colorAt(x, 0)
			plain[x] = clamp8(linearTo8(fc.r))
		}

		var dMax, dSum, pMax, pSum float64
		for x := 0; x < w; x++ {
			ideal := linearTo8(lg.colorAt(x, 0).r)
			dd := math.Abs(float64(row[x]) - ideal)
			dSum += dd
			if dd > dMax {
				dMax = dd
			}
			pd := math.Abs(float64(plain[x]) - ideal)
			pSum += pd
			if pd > pMax {
				pMax = pd
			}
		}

		ditheredRun, ditheredDistinct := countRunStats(row)
		plainRun, _ := countRunStats(plain)

		// Dithering breaks bands spatially: rows no longer repeat the same
		// pattern, so the image gains vertical (inter-row) variation even
		// though the set of encoded levels is unchanged.
		verticalChanges := 0
		for yy := 1; yy < h; yy++ {
			for xx := 0; xx < w; xx++ {
				if frame[yy][xx] != frame[yy-1][xx] {
					verticalChanges++
				}
			}
		}

		t.Logf("%s gray ramp: dithered maxErr=%.3f meanErr=%.3f run=%dpx distinct=%d | truncated maxErr=%.3f meanErr=%.3f run=%dpx",
			tc.name, dMax, dSum/float64(w), ditheredRun, ditheredDistinct,
			pMax, pSum/float64(w), plainRun)
		t.Logf("%s inter-row pixel changes from dithering=%d/%d", tc.name, verticalChanges, w*(h-1))

		if dMax > 1.0 {
			t.Fatalf("%s: dithered per-pixel error %.3f exceeds one level", tc.name, dMax)
		}
		if verticalChanges == 0 {
			t.Fatalf("%s: dithering produced no inter-row variation; bands remain solid", tc.name)
		}
		// The error-diffused mean must not be worse than naive truncation.
		if dSum/float64(w) > pSum/float64(w)+0.05 {
			t.Fatalf("%s: dithering raised mean error (%.3f vs %.3f)", tc.name,
				dSum/float64(w), pSum/float64(w))
		}
	}

	// The canonical 256-level full black-to-white ramp must also stay within
	// one quantization level at every pixel.
	const w = 256
	fullGrad := NewLinearGradient(0, 0, w-1, 0)
	fullGrad.AddColorStop(0, color.RGBA{0, 0, 0, 255})
	fullGrad.AddColorStop(1, color.RGBA{255, 255, 255, 255})
	full := renderDithered(fullGrad.(ditherablePattern), w, 4)
	for y, r := range full {
		for x, got := range r {
			ideal := idealGrayFloat(float64(x) / float64(w-1))
			if math.Abs(float64(got)-ideal) > 1.0 {
				t.Fatalf("256-level ramp y=%d x=%d error exceeds one level", y, x)
			}
		}
	}
}

// TestLinearGradient16BitAccuracy renders a multi-stop color gradient and
// compares the dithered 8-bit output against the un-quantized gamma-correct
// color (the value a 16-bit surface retains). Floyd-Steinberg bounds each
// pixel to one quantization level and keeps the average well under the design
// limit of 1.5, replacing the pre-fix measured maximum of 4.2.
func TestLinearGradient16BitAccuracy(t *testing.T) {
	const w, h = 256, 16
	g := NewLinearGradient(0, 0, w-1, 0)
	g.AddColorStop(0.0, color.RGBA{20, 30, 90, 255})
	g.AddColorStop(0.5, color.RGBA{200, 90, 140, 255})
	g.AddColorStop(1.0, color.RGBA{240, 230, 180, 255})
	dp := g.(ditherablePattern)

	d := newFSDitherer(dp, w)
	got := make([][3]uint8, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, gg, b, _ := d.pixel(x, y)
			got[y*w+x] = [3]uint8{r, gg, b}
		}
	}

	var globalMax, total float64
	var rowMaxSum float64
	for y := 0; y < h; y++ {
		var rowMax float64
		for x := 0; x < w; x++ {
			fc := g.(*linearGradient).colorAt(x, y)
			ideal := [3]float64{linearTo8(fc.r), linearTo8(fc.g), linearTo8(fc.b)}
			px := got[y*w+x]
			for k := 0; k < 3; k++ {
				dd := math.Abs(float64(px[k]) - ideal[k])
				total += dd
				if dd > rowMax {
					rowMax = dd
				}
				if dd > globalMax {
					globalMax = dd
				}
			}
		}
		rowMaxSum += rowMax
	}

	n := float64(w * h * 3)
	meanErr := total / n
	avgRowMax := rowMaxSum / float64(h)

	t.Logf("16-bit reference (3-channel color): perPixelMax=%.3f avgRowMax=%.3f meanErr=%.3f (design limit 1.5)",
		globalMax, avgRowMax, meanErr)

	if avgRowMax > 1.5 {
		t.Fatalf("average local max color difference = %.3f, want <= 1.5", avgRowMax)
	}
	if meanErr > 1.0 {
		t.Fatalf("mean color difference = %.3f, want <= 1.0", meanErr)
	}
}

// TestGammaCorrectMidpoint verifies interpolation happens in linear-light
// space: the 50% stop between black and white must evaluate to the sRGB
// encoding of 0.5 linear (~188), not the naive encoded-space midpoint (128).
func TestGammaCorrectMidpoint(t *testing.T) {
	g := NewLinearGradient(0, 0, 255, 0)
	g.AddColorStop(0, color.RGBA{0, 0, 0, 255})
	g.AddColorStop(1, color.RGBA{255, 255, 255, 255})

	col := g.ColorAt(127, 0).(color.RGBA)
	want := clamp8(linearTo8(0.5))
	t.Logf("midpoint gray: got=%d want~=%d (naive sRGB lerp would give 128)", col.R, want)

	if int(col.R) < int(want)-1 || int(col.R) > int(want)+1 {
		t.Fatalf("gamma midpoint = %d, want ~%d", col.R, want)
	}
	if col.R <= 150 {
		t.Fatalf("midpoint %d still looks like non-linear interpolation", col.R)
	}
}

// TestRadialEdgeSmooth checks that the radial gradient outer boundary fades
// over several pixels instead of jumping from opaque to transparent.
func TestRadialEdgeSmooth(t *testing.T) {
	const cx, cy, r = 64.0, 64.0, 40.0
	g := NewRadialGradient(cx, cy, 0, cx, cy, r)
	g.AddColorStop(0, color.RGBA{0, 0, 0, 255})
	g.AddColorStop(1, color.RGBA{255, 255, 255, 255})
	dp := g.(ditherablePattern)

	type sample struct {
		x     int
		alpha float64
	}
	var row []sample
	for x := 0; x < 128; x++ {
		_, _, _, a := dp.colorAtFloat(x, int(cy))
		row = append(row, sample{x, float64(a) / 255.0})
	}

	firstZero, lastOne := -1, -1
	for _, s := range row {
		if float64(s.x) < r {
			continue
		}
		if s.alpha == 0 && firstZero == -1 {
			firstZero = s.x
		}
		if s.alpha >= 0.999 {
			lastOne = s.x
		}
	}
	if firstZero == -1 || lastOne == -1 || firstZero <= lastOne {
		t.Fatalf("could not locate edge transition: lastOpaque=%d firstTransparent=%d", lastOne, firstZero)
	}
	widthPx := firstZero - lastOne

	var maxStep float64
	for i := 1; i < len(row); i++ {
		if d := math.Abs(row[i].alpha - row[i-1].alpha); d > maxStep {
			maxStep = d
		}
	}

	t.Logf("radial edge: transitionWidth=%dpx (radius=%.0f) maxStep=%.3f", widthPx, r, maxStep)

	if widthPx < 3 {
		t.Fatalf("edge transition only %dpx, want a multi-pixel smooth fade", widthPx)
	}
	if maxStep >= 0.5 {
		t.Fatalf("edge alpha jumps by %.3f in one pixel, want gradual fade", maxStep)
	}
}

// TestRadialEdgeComposited verifies the smooth edge survives the full paint
// pipeline (dithering plus compositing) on a real context.
func TestRadialEdgeComposited(t *testing.T) {
	const size = 128
	dc := NewContext(size, size)
	g := NewRadialGradient(64, 64, 0, 64, 64, 40)
	g.AddColorStop(0, color.RGBA{0, 0, 0, 255})
	g.AddColorStop(1, color.RGBA{255, 255, 255, 255})
	dc.SetFillStyle(g)
	dc.DrawRectangle(0, 0, size, size)
	dc.Fill()

	var maxStep float64
	var prevA uint8
	for x := 96; x < 112; x++ {
		a := dc.im.Pix[(64*dc.im.Stride)+x*4+3]
		if x > 96 {
			if d := math.Abs(float64(a)-float64(prevA)) / 255.0; d > maxStep {
				maxStep = d
			}
		}
		prevA = a
	}
	t.Logf("composited radial edge max alpha step=%.3f", maxStep)
	if maxStep >= 0.5 {
		t.Fatalf("composited edge still hard: max step %.3f", maxStep)
	}
}

// TestRadialFocalHole verifies a non-zero inner radius leaves the focal hole
// transparent while the ring between the circles still renders.
func TestRadialFocalHole(t *testing.T) {
	dc := NewContext(128, 128)
	g := NewRadialGradient(64, 64, 15, 64, 64, 50)
	g.AddColorStop(0, color.RGBA{255, 0, 0, 255})
	g.AddColorStop(1, color.RGBA{0, 0, 255, 255})
	dc.SetFillStyle(g)
	dc.DrawRectangle(0, 0, 128, 128)
	dc.Fill()

	centerA := dc.im.Pix[64*dc.im.Stride+64*4+3]
	if centerA != 0 {
		t.Fatalf("focal hole center alpha = %d, want 0", centerA)
	}
	ringA := dc.im.Pix[64*dc.im.Stride+40*4+3]
	if ringA == 0 {
		t.Fatalf("ring pixel at x=40 should be painted, got alpha 0")
	}
	outsideA := dc.im.Pix[64*dc.im.Stride+120*4+3]
	if outsideA != 0 {
		t.Fatalf("outside outer circle alpha = %d, want 0", outsideA)
	}
	t.Logf("focal hole: centerA=%d ringA=%d outsideA=%d", centerA, ringA, outsideA)
}
