package gg

import (
	"image/color"
	"math"
	"sort"
)

type stop struct {
	pos   float64
	color color.Color
}

type stops []stop

// Len satisfies the Sort interface.
func (s stops) Len() int {
	return len(s)
}

// Less satisfies the Sort interface.
func (s stops) Less(i, j int) bool {
	return s[i].pos < s[j].pos
}

// Swap satisfies the Sort interface.
func (s stops) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

type Gradient interface {
	Pattern
	AddColorStop(offset float64, color color.Color)
}

// srgbToLinear converts an sRGB-encoded channel (normalized to [0,1]) into
// linear-light space. Interpolating in this space is gamma correct, so
// midpoints land on the perceptually right value instead of the darker value
// produced by blending encoded bytes directly.
func srgbToLinear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// linearToSRGB is the inverse of srgbToLinear.
func linearToSRGB(c float64) float64 {
	if c <= 0.0031308 {
		return c * 12.92
	}
	return 1.055*math.Pow(c, 1.0/2.4) - 0.055
}

// rgba16ToLinear expands a 16-bit color.Color channel to a normalized linear
// light value.
func rgba16ToLinear(v uint32) float64 {
	return srgbToLinear(float64(v) / 65535.0)
}

// linearTo8 quantizes a linear-light channel back to an 8-bit sRGB value.
// The returned float keeps fractional precision so the ditherer can diffuse
// the quantization remainder instead of throwing it away.
func linearTo8(v float64) float64 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return linearToSRGB(v) * 255.0
}

// rgbaFloat is the gamma-correct gradient color in linear-light space. RGB is
// straight (non-premultiplied); alpha stays in its encoded [0,1] range, since
// coverage is interpolated linearly regardless of gamma.
type rgbaFloat struct {
	r, g, b, a float64
}

func colorToFloat(c color.Color) rgbaFloat {
	r, g, b, a := c.RGBA()
	return rgbaFloat{
		r: rgba16ToLinear(r),
		g: rgba16ToLinear(g),
		b: rgba16ToLinear(b),
		a: float64(a) / 65535.0,
	}
}

func colorLerpFloat(c0, c1 color.Color, t float64) rgbaFloat {
	f0 := colorToFloat(c0)
	f1 := colorToFloat(c1)
	return rgbaFloat{
		r: f0.r + (f1.r-f0.r)*t,
		g: f0.g + (f1.g-f0.g)*t,
		b: f0.b + (f1.b-f0.b)*t,
		a: f0.a + (f1.a-f0.a)*t,
	}
}

// to8 returns the color as straight-alpha 8-bit sRGB channels, with the RGB
// channels left as floats so fractional quantization error can be dithered.
func (c rgbaFloat) to8() (r, g, b, a uint8) {
	return uint8(clamp8(linearTo8(c.r))),
		uint8(clamp8(linearTo8(c.g))),
		uint8(clamp8(linearTo8(c.b))),
		uint8(clamp8(c.a * 255.0))
}

// color returns the gamma-correct color as a standard color.Color.
func (c rgbaFloat) color() color.Color {
	r, g, b, a := c.to8()
	return color.RGBA{r, g, b, a}
}

// toFloat returns the un-quantized sRGB color channels for the ditherer.
func (c rgbaFloat) toFloat() (r, g, b float64, a uint8) {
	return linearTo8(c.r), linearTo8(c.g), linearTo8(c.b), clamp8(c.a * 255.0)
}

// withCoverage applies the radial-edge feather to alpha while leaving the
// straight (non-premultiplied) RGB untouched. The painter performs
// source-over compositing, which expects straight color plus an independent
// coverage; scaling the color here would darken the feather into a visible
// ring when painted over an opaque background.
func (c rgbaFloat) withCoverage(coverage float64) rgbaFloat {
	return rgbaFloat{r: c.r, g: c.g, b: c.b, a: c.a * coverage}
}

// gradientColorAt evaluates the stop ramp at pos and returns the
// gamma-correct color. Positions outside [0,1] clamp to the endpoint stops,
// matching the previous behavior.
func gradientColorAt(pos float64, s stops) rgbaFloat {
	if pos <= 0.0 || len(s) == 1 {
		return colorToFloat(s[0].color)
	}

	last := s[len(s)-1]
	if pos >= last.pos {
		return colorToFloat(last.color)
	}

	for i, stop := range s[1:] {
		if pos < stop.pos {
			t := (pos - s[i].pos) / (stop.pos - s[i].pos)
			return colorLerpFloat(s[i].color, stop.color, t)
		}
	}

	return colorToFloat(last.color)
}

// Linear Gradient
type linearGradient struct {
	x0, y0, x1, y1 float64
	stops          stops
}

func (g *linearGradient) ColorAt(x, y int) color.Color {
	if len(g.stops) == 0 {
		return color.Transparent
	}
	return g.colorAt(x, y).color()
}

// colorAtFloat satisfies ditherablePattern.
func (g *linearGradient) colorAtFloat(x, y int) (r, gg, b float64, a uint8) {
	if len(g.stops) == 0 {
		return 0, 0, 0, 0
	}
	return g.colorAt(x, y).toFloat()
}

func (g *linearGradient) colorAt(x, y int) rgbaFloat {
	fx, fy := float64(x), float64(y)
	x0, y0, x1, y1 := g.x0, g.y0, g.x1, g.y1
	dx, dy := x1-x0, y1-y0

	// Horizontal
	if dy == 0 && dx != 0 {
		return gradientColorAt((fx-x0)/dx, g.stops)
	}

	// Vertical
	if dx == 0 && dy != 0 {
		return gradientColorAt((fy-y0)/dy, g.stops)
	}

	// Dot product
	s0 := dx*(fx-x0) + dy*(fy-y0)
	if s0 < 0 {
		return colorToFloat(g.stops[0].color)
	}
	// Calculate distance to (x0,y0) alone (x0,y0)->(x1,y1)
	mag := math.Hypot(dx, dy)
	u := ((fx-x0)*-dy + (fy-y0)*dx) / (mag * mag)
	x2, y2 := x0+u*-dy, y0+u*dx
	d := math.Hypot(fx-x2, fy-y2) / mag
	return gradientColorAt(d, g.stops)
}

func (g *linearGradient) AddColorStop(offset float64, color color.Color) {
	g.stops = append(g.stops, stop{pos: offset, color: color})
	sort.Sort(g.stops)
}

func NewLinearGradient(x0, y0, x1, y1 float64) Gradient {
	g := &linearGradient{
		x0: x0, y0: y0,
		x1: x1, y1: y1,
	}
	return g
}

// Radial Gradient
type circle struct {
	x, y, r float64
}

type radialGradient struct {
	c0, c1, cd circle
	a, inva    float64
	mindr      float64
	stops      stops
}

func dot3(x0, y0, z0, x1, y1, z1 float64) float64 {
	return x0*x1 + y0*y1 + z0*z1
}

// radialEdgeSoftness is the half-width, in pixels, of the radial gradient
// outer-edge transition. The old implementation cut from the last stop color
// to fully transparent within a pixel or two; spreading the fade over a few
// pixels removes that visible hard edge.
const radialEdgeSoftness = 2.0

// radialEdgeFactor fades coverage to zero across the outer circle based on the
// pixel's signed distance from the outer circumference, so the transition
// keeps a constant on-screen width for both concentric and eccentric
// gradients. Positive distance is outside the circle.
func radialEdgeFactor(distToOuter float64) float64 {
	if distToOuter <= -radialEdgeSoftness {
		return 1
	}
	if distToOuter >= radialEdgeSoftness {
		return 0
	}
	u := (distToOuter + radialEdgeSoftness) / (2 * radialEdgeSoftness)
	return 1 - u*u*(3-2*u)
}

func (g *radialGradient) ColorAt(x, y int) color.Color {
	if len(g.stops) == 0 {
		return color.Transparent
	}
	col, coverage := g.eval(x, y)
	if coverage <= 0 {
		return color.Transparent
	}
	return col.withCoverage(coverage).color()
}

// colorAtFloat satisfies ditherablePattern.
func (g *radialGradient) colorAtFloat(x, y int) (r, gg, b float64, a uint8) {
	if len(g.stops) == 0 {
		return 0, 0, 0, 0
	}
	col, coverage := g.eval(x, y)
	if coverage <= 0 {
		return 0, 0, 0, 0
	}
	return col.withCoverage(coverage).toFloat()
}

// eval returns the raw gradient color plus the outer-edge coverage factor.
func (g *radialGradient) eval(x, y int) (rgbaFloat, float64) {
	// copy from pixman's pixman-radial-gradient.c

	dx, dy := float64(x)+0.5-g.c0.x, float64(y)+0.5-g.c0.y
	b := dot3(dx, dy, g.c0.r, g.cd.x, g.cd.y, g.cd.r)
	c := dot3(dx, dy, -g.c0.r, dx, dy, g.c0.r)
	// Signed distance from the pixel center to the outer circumference, in
	// pixels. Used to feather the edge at a constant on-screen width.
	odx, ody := float64(x)+0.5-g.c1.x, float64(y)+0.5-g.c1.y
	distToOuter := math.Hypot(odx, ody) - g.c1.r
	coverage := radialEdgeFactor(distToOuter)
	// Explicit focal-hole test: points inside the inner circle have no
	// gradient value. Checking the geometry directly is more robust than the
	// quadratic roots' sign convention, especially for eccentric gradients.
	if g.c0.r > 0 {
		idx, idy := float64(x)+0.5-g.c0.x, float64(y)+0.5-g.c0.y
		if math.Hypot(idx, idy) < g.c0.r {
			return rgbaFloat{}, 0
		}
	}

	if g.a == 0 {
		if b == 0 {
			return rgbaFloat{}, 0
		}
		t := 0.5 * c / b
		if t*g.cd.r >= g.mindr {
			if coverage <= 0 {
				return rgbaFloat{}, 0
			}
			return gradientColorAt(t, g.stops), coverage
		}
		return rgbaFloat{}, 0
	}

	var t float64
	found := false
	discr := dot3(b, g.a, 0, b, -c, 0)
	if discr >= 0 {
		sqrtdiscr := math.Sqrt(discr)
		t0 := (b + sqrtdiscr) * g.inva
		t1 := (b - sqrtdiscr) * g.inva

		switch {
		case t0*g.cd.r >= g.mindr:
			t, found = t0, true
		case t1*g.cd.r >= g.mindr:
			t, found = t1, true
		}
	}

	if !found {
		// No real root: outside the outer circle (the feather handles the
		// fade) or inside the focal hole. Outside the edge the color is the
		// last stop; inside the hole there is no gradient value at all.
		if coverage <= 0 || distToOuter < 0 {
			return rgbaFloat{}, 0
		}
		return colorToFloat(g.stops[len(g.stops)-1].color), coverage
	}

	if coverage <= 0 {
		return rgbaFloat{}, 0
	}
	return gradientColorAt(t, g.stops), coverage
}

func (g *radialGradient) AddColorStop(offset float64, color color.Color) {
	g.stops = append(g.stops, stop{pos: offset, color: color})
	sort.Sort(g.stops)
}

func NewRadialGradient(x0, y0, r0, x1, y1, r1 float64) Gradient {
	c0 := circle{x0, y0, r0}
	c1 := circle{x1, y1, r1}
	cd := circle{x1 - x0, y1 - y0, r1 - r0}
	a := dot3(cd.x, cd.y, -cd.r, cd.x, cd.y, cd.r)
	var inva float64
	if a != 0 {
		inva = 1.0 / a
	}
	mindr := -c0.r
	g := &radialGradient{
		c0:    c0,
		c1:    c1,
		cd:    cd,
		a:     a,
		inva:  inva,
		mindr: mindr,
	}
	return g
}

// Conic Gradient
type conicGradient struct {
	cx, cy   float64
	rotation float64
	stops    stops
}

func (g *conicGradient) ColorAt(x, y int) color.Color {
	if len(g.stops) == 0 {
		return color.Transparent
	}
	return g.colorAt(x, y).color()
}

// colorAtFloat satisfies ditherablePattern.
func (g *conicGradient) colorAtFloat(x, y int) (r, gg, b float64, a uint8) {
	if len(g.stops) == 0 {
		return 0, 0, 0, 0
	}
	return g.colorAt(x, y).toFloat()
}

func (g *conicGradient) colorAt(x, y int) rgbaFloat {
	a := math.Atan2(float64(y)-g.cy, float64(x)-g.cx)
	t := norm(a, -math.Pi, math.Pi) - g.rotation
	if t < 0 {
		t += 1
	}
	return gradientColorAt(t, g.stops)
}

func (g *conicGradient) AddColorStop(offset float64, color color.Color) {
	g.stops = append(g.stops, stop{pos: offset, color: color})
	sort.Sort(g.stops)
}

func NewConicGradient(cx, cy, deg float64) Gradient {
	g := &conicGradient{
		cx:       cx,
		cy:       cy,
		rotation: normalizeAngle(deg) / 360,
	}
	return g
}

func normalizeAngle(t float64) float64 {
	t = math.Mod(t, 360)
	if t < 0 {
		t += 360
	}
	return t
}

// Map value which is in range [a..b] to range [0..1]
func norm(value, a, b float64) float64 {
	return (value - a) * (1.0 / (b - a))
}
