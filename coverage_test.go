package gg

import (
	"image"
	"image/color"
	"math"
	"math/rand"
	"testing"
)

const coverageSupersample = 16

type coverageScene struct {
	name   string
	width  int
	height int
	draw   func(*Context)
}

func coverageScenes() []coverageScene {
	return []coverageScene{
		{
			name:   "existing-fill-test",
			width:  100,
			height: 100,
			draw: func(dc *Context) {
				rnd := rand.New(rand.NewSource(99))
				for i := 0; i < 10; i++ {
					dc.NewSubPath()
					for j := 0; j < 10; j++ {
						x := rnd.Float64() * 100
						y := rnd.Float64() * 100
						dc.LineTo(x, y)
					}
					dc.ClosePath()
					dc.SetRGBA(rnd.Float64(), rnd.Float64(), rnd.Float64(), rnd.Float64())
					dc.Fill()
				}
			},
		},
		{
			name:   "slender-triangle",
			width:  320,
			height: 240,
			draw: func(dc *Context) {
				dc.MoveTo(20, 120)
				dc.LineTo(300, 108)
				dc.LineTo(300, 122)
				dc.ClosePath()
				dc.Fill()
			},
		},
		{
			name:   "acute-triangle",
			width:  320,
			height: 240,
			draw: func(dc *Context) {
				dc.MoveTo(20, 120)
				dc.LineTo(300, 119.25)
				dc.LineTo(20, 120.75)
				dc.ClosePath()
				dc.Fill()
			},
		},
		{
			name:   "self-intersecting-polygon",
			width:  320,
			height: 240,
			draw: func(dc *Context) {
				dc.MoveTo(40, 40)
				dc.LineTo(280, 200)
				dc.LineTo(40, 200)
				dc.LineTo(280, 40)
				dc.ClosePath()
				dc.Fill()
			},
		},
	}
}

func drawCoverageScene(scene coverageScene) *image.RGBA {
	dc := NewContext(scene.width, scene.height)
	dc.SetRGB(1, 1, 1)
	dc.Clear()
	dc.SetRGB(0, 0, 0)
	scene.draw(dc)
	return dc.im
}

func drawCoverageReference(scene coverageScene) *image.RGBA {
	const scale = coverageSupersample
	dc := NewContext(scene.width*scale, scene.height*scale)
	dc.SetRGB(1, 1, 1)
	dc.Clear()
	dc.SetRGB(0, 0, 0)
	dc.Scale(scale, scale)
	scene.draw(dc)

	dst := image.NewRGBA(image.Rect(0, 0, scene.width, scene.height))
	src := dc.im
	for y := 0; y < scene.height; y++ {
		for x := 0; x < scene.width; x++ {
			var r, g, b, a uint32
			for sy := 0; sy < scale; sy++ {
				for sx := 0; sx < scale; sx++ {
					c := src.RGBAAt(x*scale+sx, y*scale+sy)
					r += uint32(c.R)
					g += uint32(c.G)
					b += uint32(c.B)
					a += uint32(c.A)
				}
			}
			const samples = scale * scale
			dst.Set(x, y, color.RGBA{
				uint8((r + samples/2) / samples),
				uint8((g + samples/2) / samples),
				uint8((b + samples/2) / samples),
				uint8((a + samples/2) / samples),
			})
		}
	}
	return dst
}

func coveragePSNR(a, b *image.RGBA) float64 {
	var sum float64
	var count int
	for y := a.Rect.Min.Y; y < a.Rect.Max.Y; y++ {
		for x := a.Rect.Min.X; x < a.Rect.Max.X; x++ {
			ca := a.RGBAAt(x, y)
			cb := b.RGBAAt(x, y)
			dr := float64(int(ca.R) - int(cb.R))
			dg := float64(int(ca.G) - int(cb.G))
			db := float64(int(ca.B) - int(cb.B))
			sum += dr*dr + dg*dg + db*db
			count += 3
		}
	}
	if sum == 0 {
		return math.Inf(1)
	}
	mse := sum / float64(count)
	return 10 * math.Log10(255*255/mse)
}

func TestCoverageQuality(t *testing.T) {
	for _, scene := range coverageScenes() {
		t.Run(scene.name, func(t *testing.T) {
			got := drawCoverageScene(scene)
			reference := drawCoverageReference(scene)
			psnr := coveragePSNR(got, reference)
			t.Logf("PSNR: %.2f dB", psnr)
			if psnr < 40 {
				t.Errorf("PSNR %.2f dB is below 40 dB", psnr)
			}
		})
	}
}

func BenchmarkCoverageScene(b *testing.B) {
	for _, scene := range coverageScenes() {
		b.Run(scene.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				drawCoverageScene(scene)
			}
		})
	}
}

func BenchmarkCoverageReference16x(b *testing.B) {
	for _, scene := range coverageScenes() {
		b.Run(scene.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				drawCoverageReference(scene)
			}
		})
	}
}
