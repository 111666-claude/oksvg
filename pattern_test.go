// Copyright 2021 The oksvg Authors. All rights reserved.
//
// pattern_test.go exercises pattern fill parsing and rendering, checking
// tile placement, transforms, clipping, opacity and the handling of invalid,
// cyclic or unsupported pattern definitions.

package oksvg

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/srwiley/rasterx"
)

// renderPattern parses and draws an SVG document into a fresh RGBA image of
// the requested size and returns that image.
func renderPattern(t *testing.T, svg string, w, h int) *image.RGBA {
	t.Helper()
	icon, err := ReadIconStream(strings.NewReader(svg), WarnErrorMode)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	scanner := rasterx.NewScannerGV(w, h, img, img.Bounds())
	raster := rasterx.NewDasher(w, h, scanner)
	icon.Draw(raster, 1.0)
	return img
}

// opaque reports whether the pixel is fully opaque and matches the rgb triple
// within a tolerance that allows rasterizer anti-aliasing.
func pixelIs(img *image.RGBA, x, y int, want color.NRGBA) bool {
	got := img.RGBAAt(x, y)
	const tol = 12
	d := func(a, b uint8) int {
		v := int(a) - int(b)
		if v < 0 {
			return -v
		}
		return v
	}
	return d(got.R, want.R) <= tol && d(got.G, want.G) <= tol &&
		d(got.B, want.B) <= tol && got.A > 240
}

func transparentAt(img *image.RGBA, x, y int) bool {
	return img.RGBAAt(x, y).A == 0
}

var (
	red  = color.NRGBA{R: 255, A: 255}
	blue = color.NRGBA{B: 255, A: 255}
)

// TestPatternStripes verifies userSpaceOnUse x/y/width/height tiling and the
// transparent gaps of a partially covered tile.
func TestPatternStripes(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20" viewBox="0 0 40 20">
<defs><pattern id="p" patternUnits="userSpaceOnUse" x="0" y="0" width="10" height="10">
<rect x="0" y="0" width="5" height="10" fill="red"/></pattern></defs>
<rect x="0" y="0" width="40" height="20" fill="url(#p)"/></svg>`
	img := renderPattern(t, svg, 40, 20)
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			switch {
			case x%10 < 5:
				if !pixelIs(img, x, y, red) {
					t.Errorf("(%d,%d): want red stripe", x, y)
				}
			default:
				if !transparentAt(img, x, y) {
					t.Errorf("(%d,%d): want transparent tile gap", x, y)
				}
			}
		}
	}
}

// TestPatternOffset checks that the pattern x/y origin shifts every tile and
// is honored even when content coordinates use the same origin.
func TestPatternOffset(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="30" height="10" viewBox="0 0 30 10">
<defs><pattern id="p" patternUnits="userSpaceOnUse" x="5" y="0" width="10" height="10">
<rect x="5" y="0" width="5" height="10" fill="red"/></pattern></defs>
<rect x="0" y="0" width="30" height="10" fill="url(#p)"/></svg>`
	img := renderPattern(t, svg, 30, 10)
	for y := 0; y < 10; y++ {
		for x := 0; x < 30; x++ {
			// Tiles start at x=5, period 10; red is the second half of each.
			redBand := (x >= 5 && x < 10) || (x >= 15 && x < 20) || (x >= 25 && x < 30)
			if redBand && !pixelIs(img, x, y, red) {
				t.Errorf("(%d,%d): want offset red band", x, y)
			}
			if !redBand && !transparentAt(img, x, y) {
				t.Errorf("(%d,%d): want transparency outside band", x, y)
			}
		}
	}
}

// TestPatternCheckerboard verifies a two-color opaque tile made of several
// basic shapes tiles in both axes with the right cell colors.
func TestPatternCheckerboard(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 16 16">
<defs><pattern id="p" patternUnits="userSpaceOnUse" width="8" height="8">
<rect width="8" height="8" fill="blue"/>
<rect width="4" height="4" fill="red"/>
<rect x="4" y="4" width="4" height="4" fill="red"/></pattern></defs>
<rect width="16" height="16" fill="url(#p)"/></svg>`
	img := renderPattern(t, svg, 16, 16)
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			redCell := (x%8 < 4) == (y%8 < 4)
			want := blue
			if redCell {
				want = red
			}
			if !pixelIs(img, x, y, want) {
				t.Errorf("(%d,%d): want %v", x, y, want)
			}
		}
	}
}

// TestPatternTransparentTile confirms a tile whose shapes cover only part of
// its area leaves the rest transparent (rather than painting a default fill).
func TestPatternTransparentTile(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 12 12">
<defs><pattern id="p" patternUnits="userSpaceOnUse" width="6" height="6">
<rect x="0" y="0" width="6" height="3" fill="red"/></pattern></defs>
<rect width="12" height="12" fill="url(#p)"/></svg>`
	img := renderPattern(t, svg, 12, 12)
	for y := 0; y < 12; y++ {
		for x := 0; x < 12; x++ {
			if y%6 < 3 {
				if !pixelIs(img, x, y, red) {
					t.Errorf("(%d,%d): want red half-tile", x, y)
				}
			} else if !transparentAt(img, x, y) {
				t.Errorf("(%d,%d): want transparent half-tile", x, y)
			}
		}
	}
}

// TestPatternRotatedTransform checks patternTransform rotation: diagonal
// stripes must be present and the paint must remain clipped to the shape.
func TestPatternRotatedTransform(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 20 20">
<defs><pattern id="p" patternUnits="userSpaceOnUse" width="8" height="8" patternTransform="rotate(45)">
<rect x="0" y="0" width="4" height="8" fill="red"/></pattern></defs>
<rect x="0" y="0" width="20" height="20" fill="url(#p)"/></svg>`
	img := renderPattern(t, svg, 20, 20)
	redCount, clearCount := 0, 0
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			switch {
			case pixelIs(img, x, y, red):
				redCount++
			case transparentAt(img, x, y):
				clearCount++
			}
		}
	}
	// Roughly half the area is covered by a half-width rotated stripe.
	if redCount < 60 || clearCount < 60 {
		t.Errorf("rotated pattern coverage wrong: red=%d clear=%d", redCount, clearCount)
	}
}

// TestPatternClipToShape ensures the pattern is visible only inside the
// filled shape (a circle), with transparency outside it.
func TestPatternClipToShape(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 20 20">
<defs><pattern id="p" patternUnits="userSpaceOnUse" width="6" height="6">
<rect width="3" height="6" fill="red"/></pattern></defs>
<circle cx="10" cy="10" r="8" fill="url(#p)"/></svg>`
	img := renderPattern(t, svg, 20, 20)
	if !transparentAt(img, 0, 0) || !transparentAt(img, 19, 19) {
		t.Error("pattern painted outside the circle corners")
	}
	// A point well inside the circle must be either a red stripe or a
	// transparent tile gap, but never a color bleeding outside the circle.
	inside := img.RGBAAt(10, 8)
	if inside.A != 0 && !pixelIs(img, 10, 8, red) {
		t.Errorf("inside circle should be red stripe or gap, got %v", inside)
	}
	// Counting painted pixels gives a finite subset of the circle area,
	// proving the fill is clipped rather than filling the whole image.
	if n := countOpaque(img); n >= 20*20 || n == 0 {
		t.Errorf("clipped pattern coverage unexpected: %d", n)
	}
}

// TestPatternParentTransformAndOpacity verifies that a parent g transform
// moves the tiled fill and that group opacity is applied to the pattern.
func TestPatternParentTransformAndOpacity(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20" viewBox="0 0 40 20">
<defs><pattern id="p" patternUnits="userSpaceOnUse" width="10" height="10">
<rect width="5" height="10" fill="red"/></pattern></defs>
<g transform="translate(10,0)" opacity="0.5">
<rect width="20" height="10" fill="url(#p)"/></g></svg>`
	img := renderPattern(t, svg, 40, 20)
	if !transparentAt(img, 2, 5) {
		t.Error("translated group should leave the left area empty")
	}
	got := img.RGBAAt(12, 5)
	if got.A < 110 || got.A > 146 {
		t.Errorf("opacity 0.5 not applied to pattern fill: alpha=%d", got.A)
	}
}

// TestPatternObjectBoundingBox covers the default patternUnits: tile width
// 0.25 of the bbox yields four horizontal tiles across the shape.
func TestPatternObjectBoundingBox(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 20 20">
<defs><pattern id="p" width="0.25" height="0.5">
<rect width="0.25" height="0.5" fill="blue"/>
<rect x="0" y="0" width="0.125" height="0.5" fill="red"/></pattern></defs>
<rect width="20" height="20" fill="url(#p)"/></svg>`
	img := renderPattern(t, svg, 20, 20)
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			// Each tile is 5 device px wide; first half red, second blue.
			// Column 2 is the anti-aliased seam and is allowed either color.
			if x%5 == 2 {
				continue
			}
			want := blue
			if x%5 < 2 {
				want = red
			}
			if !pixelIs(img, x, y, want) {
				t.Errorf("(%d,%d): want %v", x, y, want)
			}
		}
	}
}

// TestPatternContentUnitsObjectBoundingBox checks patternContentUnits with a
// shape translated away from the origin: tiles must align to the shape's bbox
// and not leak into the surrounding area.
func TestPatternContentUnitsObjectBoundingBox(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="30" height="30" viewBox="0 0 30 30">
<defs><pattern id="p" width="0.5" height="0.5" patternContentUnits="objectBoundingBox">
<rect width="0.5" height="0.5" fill="blue"/>
<rect width="0.25" height="0.5" fill="red"/></pattern></defs>
<rect x="10" y="10" width="20" height="20" fill="url(#p)"/></svg>`
	img := renderPattern(t, svg, 30, 30)
	if !transparentAt(img, 2, 2) {
		t.Error("pattern must stay within the translated shape")
	}
	if !pixelIs(img, 12, 15, red) || !pixelIs(img, 18, 15, blue) {
		t.Error("OBB content units produced wrong cell colors")
	}
}

// TestPatternGradientContent verifies an objectBoundingBox gradient used
// inside a pattern tile renders across the full tile.
func TestPatternGradientContent(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 20 20">
<defs>
<linearGradient id="g" x1="0" y1="0" x2="1" y2="0" gradientUnits="objectBoundingBox">
<stop offset="0" stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient>
<pattern id="p" patternUnits="userSpaceOnUse" width="10" height="10">
<rect width="10" height="10" fill="url(#g)"/></pattern>
</defs><rect width="20" height="20" fill="url(#p)"/></svg>`
	img := renderPattern(t, svg, 20, 20)
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			if img.RGBAAt(x, y).A == 0 {
				t.Errorf("(%d,%d): gradient tile should be opaque", x, y)
			}
		}
	}
	// Left edge of each tile trends red, right edge blue.
	if img.RGBAAt(0, 5).R <= img.RGBAAt(0, 5).B {
		t.Error("gradient tile left edge should trend red")
	}
	if img.RGBAAt(9, 5).B <= img.RGBAAt(9, 5).R {
		t.Error("gradient tile right edge should trend blue")
	}
}

// TestPatternNested confirms a pattern can fill its tile with another
// pattern and still produce a finite, painted result.
func TestPatternNested(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 20 20">
<defs>
<pattern id="inner" patternUnits="userSpaceOnUse" width="5" height="5">
<rect width="5" height="2" fill="blue"/></pattern>
<pattern id="outer" patternUnits="userSpaceOnUse" width="10" height="10">
<rect width="10" height="10" fill="url(#inner)"/></pattern>
</defs><rect width="20" height="20" fill="url(#outer)"/></svg>`
	img := renderPattern(t, svg, 20, 20)
	painted := countOpaque(img)
	// The inner tile only fills its top two of five rows, so the outer tile
	// (and thus the shape) is covered on 2/5 of the pixels in a regular
	// grid. The key checks are finite coverage and the blue inner color.
	if painted == 0 || painted >= 20*20 {
		t.Errorf("nested pattern coverage unexpected: %d", painted)
	}
	if !pixelIs(img, 0, 0, blue) {
		t.Error("nested inner pattern color missing at top-left")
	}
	if !transparentAt(img, 0, 3) {
		t.Error("inner tile gap should remain transparent through nesting")
	}
}

// countOpaque returns how many pixels carry any alpha.
func countOpaque(img *image.RGBA) int {
	n := 0
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i+3] != 0 {
			n++
		}
	}
	return n
}

// TestPatternInvalidAndCyclic makes sure malformed, unknown, unsupported and
// cyclic references disable the paint deterministically without hanging or
// recursing.
func TestPatternInvalidAndCyclic(t *testing.T) {
	cases := map[string]string{
		"zeroSize":     `<pattern id="p" patternUnits="userSpaceOnUse" width="0" height="10"><rect width="5" height="10" fill="red"/></pattern>`,
		"negativeSize": `<pattern id="p" patternUnits="userSpaceOnUse" width="-5" height="10"><rect width="5" height="10" fill="red"/></pattern>`,
		"unknownRef":   ``,
		"emptyPattern": `<pattern id="p" patternUnits="userSpaceOnUse" width="10" height="10"/>`,
		"viewBox":      `<pattern id="p" patternUnits="userSpaceOnUse" width="10" height="10" viewBox="0 0 10 10"><rect width="5" height="10" fill="red"/></pattern>`,
		"selfCycle":    `<pattern id="p" patternUnits="userSpaceOnUse" width="10" height="10"><rect width="10" height="10" fill="url(#p)"/></pattern>`,
		"mutualA":      `<pattern id="a" patternUnits="userSpaceOnUse" width="10" height="10"><rect width="10" height="10" fill="url(#b)"/></pattern><pattern id="b" patternUnits="userSpaceOnUse" width="10" height="10"><rect width="10" height="10" fill="url(#a)"/></pattern>`,
	}
	body := func(defs, fill string) string {
		return `<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 20 20"><defs>` +
			defs + `</defs><rect width="20" height="20" fill="` + fill + `"/></svg>`
	}
	documents := map[string]string{
		"zeroSize":     body(cases["zeroSize"], "url(#p)"),
		"negativeSize": body(cases["negativeSize"], "url(#p)"),
		"unknownRef":   body(``, "url(#missing)"),
		"emptyPattern": body(cases["emptyPattern"], "url(#p)"),
		"viewBox":      body(cases["viewBox"], "url(#p)"),
		"selfCycle":    body(cases["selfCycle"], "url(#p)"),
		"mutualA":      body(cases["mutualA"], "url(#a)"),
	}
	for name, doc := range documents {
		t.Run(name, func(t *testing.T) {
			// renderPattern must return promptly; a cyclic reference that
			// recursed would hang the test rather than reach this assertion.
			img := renderPattern(t, doc, 20, 20)
			if n := countOpaque(img); n != 0 {
				t.Errorf("pattern %q should paint nothing, painted %d pixels", name, n)
			}
		})
	}
}

// TestPatternOversizedTileBoundsMemory ensures an enormous tile is downscaled
// rather than allocated at full size.
func TestPatternOversizedTileBoundsMemory(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40" viewBox="0 0 40 40">
<defs><pattern id="p" patternUnits="userSpaceOnUse" width="1000000" height="1000000">
<rect width="1000000" height="1000000" fill="red"/></pattern></defs>
<rect width="40" height="40" fill="url(#p)"/></svg>`
	img := renderPattern(t, svg, 40, 40)
	if n := countOpaque(img); n != 40*40 {
		t.Errorf("oversized tile should still cover shape, painted=%d", n)
	}
}

// TestPatternCurvedShapeBounds exercises the Bezier-flattening bounds
// collector: a circle (a path made of curves) filled with an
// objectBoundingBox pattern must tile against the curve's true extents.
func TestPatternCurvedShapeBounds(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="30" height="30" viewBox="0 0 30 30">
<defs><pattern id="p" width="0.25" height="0.25">
<rect width="0.25" height="0.25" fill="red"/></pattern></defs>
<circle cx="15" cy="15" r="12" fill="url(#p)"/></svg>`
	img := renderPattern(t, svg, 30, 30)
	if !transparentAt(img, 1, 1) {
		t.Error("pattern must stay inside the circle")
	}
	if !pixelIs(img, 15, 15, red) {
		t.Error("circle center should be painted")
	}
	n := countOpaque(img)
	if n < 400 || n > 30*30 {
		t.Errorf("circle coverage out of range: %d", n)
	}
}

// TestPatternUnreferencedNotDrawn verifies a pattern that is never referenced
// does not paint anything on its own.
func TestPatternUnreferencedNotDrawn(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 20 20">
<defs><pattern id="unused" patternUnits="userSpaceOnUse" width="5" height="5">
<rect width="5" height="5" fill="red"/></pattern></defs></svg>`
	img := renderPattern(t, svg, 20, 20)
	if n := countOpaque(img); n != 0 {
		t.Errorf("unreferenced pattern painted %d pixels", n)
	}
}
