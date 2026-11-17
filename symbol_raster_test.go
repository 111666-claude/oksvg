// Copyright 2022 The oksvg Authors. All rights reserved.
//
// Rasterization tests for symbol/use rendering: the same symbol drawn at two
// different sizes, the meet/slice/none viewport modes and a rotated instance
// are verified through the alpha bounding box of the rendered pixels.
package oksvg_test

import (
	"image"
	"math"
	"strings"
	"testing"

	. "github.com/srwiley/oksvg"
	. "github.com/srwiley/rasterx"
)

// strReader wraps strings.NewReader for the inline SVG fixtures.
func strReader(s string) *strings.Reader { return strings.NewReader(s) }

// sqrt2 is the square root of two, used for rotated bounding box math.
const sqrt2 = math.Sqrt2

// renderIcon rasterizes an icon into a fresh w x h RGBA image.
func renderIcon(t *testing.T, icon *SvgIcon, w, h int) *image.RGBA {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	scanner := NewScannerGV(w, h, img, img.Bounds())
	raster := NewDasher(w, h, scanner)
	icon.Draw(raster, 1.0)
	return img
}

// alphaBounds returns the bounding box of all pixels with a non-zero alpha.
// Empty input yields a zero rectangle with its flag set to false.
func alphaBounds(img *image.RGBA) (image.Rectangle, bool) {
	b := img.Bounds()
	minX, minY := b.Max.X, b.Max.Y
	maxX, maxY := b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.RGBAAt(x, y).A != 0 {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < b.Min.X {
		return image.Rectangle{}, false
	}
	return image.Rect(minX, minY, maxX+1, maxY+1), true
}

// assertBoundsNear checks that every edge of the alpha bounding box is within
// tol pixels of the expected inclusive pixel rectangle.
func assertBoundsNear(t *testing.T, got image.Rectangle, ok bool, want image.Rectangle, tol int, name string) {
	t.Helper()
	if !ok {
		t.Fatalf("%s: expected rendered pixels, got an empty image", name)
	}
	check := func(label string, g, w int) {
		d := g - w
		if d < -tol || d > tol {
			t.Errorf("%s: %s edge got %d, want %d (±%d)", name, label, g, w, tol)
		}
	}
	check("left", got.Min.X, want.Min.X)
	check("top", got.Min.Y, want.Min.Y)
	check("right", got.Max.X, want.Max.X)
	check("bottom", got.Max.Y, want.Max.Y)
}

const symbolRasterSVG = `
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 400">
  <defs>
    <symbol id="meetSym" viewBox="0 0 100 50" preserveAspectRatio="xMidYMid meet">
      <rect x="0" y="0" width="100" height="50" fill="red"/>
    </symbol>
    <symbol id="sliceSym" viewBox="0 0 100 50" preserveAspectRatio="xMidYMid slice">
      <rect x="0" y="0" width="100" height="50" fill="green"/>
    </symbol>
    <symbol id="noneSym" viewBox="0 0 100 50" preserveAspectRatio="none">
      <rect x="0" y="0" width="100" height="50" fill="blue"/>
    </symbol>
  </defs>
  <use href="#meetSym"  x="10"  y="10"  width="80" height="80"/>
  <use href="#sliceSym" x="120" y="10"  width="80" height="80"/>
  <use href="#noneSym"  x="230" y="10"  width="80" height="80"/>
</svg>`

func TestSymbolViewportRasterBounds(t *testing.T) {
	icon, err := ReadIconStream(strReader(symbolRasterSVG), StrictErrorMode)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	img := renderIcon(t, icon, 400, 400)

	// The bounds use the image.Rectangle convention where Max is one past the
	// last filled pixel; edge anti-aliasing can shift an edge by a pixel or two.
	meet, ok := colorBounds(img, 0xff0000)
	assertBoundsNear(t, meet, ok, image.Rect(10, 30, 80, 70), 2, "meet")

	// slice: scale 1.6 makes the rect 160 wide, centered on the 80-wide
	// viewport at x=120 so the rect spans screen x=80..240.
	slice, ok := colorBounds(img, 0x008000)
	assertBoundsNear(t, slice, ok, image.Rect(80, 10, 230, 90), 2, "slice")

	// none: stretched non-uniformly to fill the whole 80x80 viewport.
	none, ok := colorBounds(img, 0x0000ff)
	assertBoundsNear(t, none, ok, image.Rect(230, 10, 310, 90), 2, "none")
}

const symbolTwoSizesSVG = `
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 300 300">
  <defs>
    <symbol id="sq" viewBox="0 0 100 100">
      <rect x="0" y="0" width="100" height="100" fill="purple"/>
    </symbol>
  </defs>
  <use href="#sq" x="10" y="10"  width="100" height="100"/>
  <use href="#sq" x="150" y="150" width="40"  height="40"/>
</svg>`

func TestSymbolTwoSizesRasterBounds(t *testing.T) {
	icon, err := ReadIconStream(strReader(symbolTwoSizesSVG), StrictErrorMode)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	img := renderIcon(t, icon, 300, 300)
	b, ok := colorBounds(img, 0x800080)
	if !ok {
		t.Fatal("expected purple pixels from both instances")
	}
	// Bounding box spans the outer corners of the two instances.
	assertBoundsNear(t, b, ok, image.Rect(10, 10, 190, 190), 1, "two sizes")
}

const symbolRotateSVG = `
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 200">
  <defs>
    <symbol id="sq" viewBox="0 0 100 100">
      <rect x="0" y="0" width="100" height="100" fill="teal"/>
    </symbol>
  </defs>
  <!-- Rotate the 80x80 instance 45 degrees about the viewport center (100,90). -->
  <use href="#sq" x="60" y="50" width="80" height="80" transform="rotate(45 100 90)"/>
</svg>`

func TestSymbolRotatedRasterBounds(t *testing.T) {
	icon, err := ReadIconStream(strReader(symbolRotateSVG), StrictErrorMode)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	img := renderIcon(t, icon, 200, 200)
	b, ok := colorBounds(img, 0x008080)
	if !ok {
		t.Fatal("expected teal pixels from the rotated instance")
	}
	// A rotated square of side 80 has an ideal axis-aligned bounding box of
	// 80*sqrt(2) ~= 113.1 centered on the rotation center (100,90); edge
	// anti-aliasing trims a few boundary pixels, so the rasterized box is
	// slightly smaller but stays centered on the same point.
	half := 40 * sqrt2
	want := image.Rect(
		int(100-half), int(90-half),
		int(100+half), int(90+half))
	assertBoundsNear(t, b, ok, want, 4, "rotated")
	// The center of the rendered box must match the rotation center exactly.
	cx := (b.Min.X + b.Max.X) / 2
	cy := (b.Min.Y + b.Max.Y) / 2
	if cx != 100 || cy != 90 {
		t.Errorf("rotated: center got (%d,%d), want (100,90)", cx, cy)
	}
}

// colorBounds returns the alpha bounding box of pixels matching the given
// 0xRRGGBB color (any non-zero alpha).
func colorBounds(img *image.RGBA, rgb uint32) (image.Rectangle, bool) {
	wantR := uint8(rgb >> 16)
	wantG := uint8(rgb >> 8)
	wantB := uint8(rgb)
	b := img.Bounds()
	minX, minY := b.Max.X, b.Max.Y
	maxX, maxY := b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.RGBAAt(x, y)
			if c.A != 0 && c.R == wantR && c.G == wantG && c.B == wantB {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < b.Min.X {
		return image.Rectangle{}, false
	}
	return image.Rect(minX, minY, maxX+1, maxY+1), true
}
