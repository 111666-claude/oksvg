// Copyright 2026 The oksvg Authors. All rights reserved.
//
// Pixel sampling tests for SVG alpha mask rendering.
package oksvg_test

import (
	"image"
	"strings"
	"testing"

	. "github.com/srwiley/oksvg"
	. "github.com/srwiley/rasterx"
)

func drawMaskIcon(t *testing.T, svg string, w, h int) *image.RGBA {
	t.Helper()
	icon, err := ReadIconStream(strings.NewReader(svg), StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	scannerGV := NewScannerGV(w, h, img, img.Bounds())
	raster := NewDasher(w, h, scannerGV)
	icon.Draw(raster, 1.0)
	return img
}

func alphaAt(img *image.RGBA, x, y int) int {
	return int(img.RGBAAt(x, y).A)
}

func checkAlpha(t *testing.T, img *image.RGBA, x, y, min, max int) {
	t.Helper()
	a := alphaAt(img, x, y)
	if a < min || a > max {
		t.Errorf("alpha at (%d,%d) = %d, want in [%d,%d]", x, y, a, min, max)
	}
}

// A translucent gradient mask fades a red rectangle out from left to right.
func TestGradientMaskOnRect(t *testing.T) {
	const svg = `<svg viewBox="0 0 100 100" width="100" height="100">
<defs>
<linearGradient id="fade" x1="0" y1="0" x2="1" y2="0">
	<stop offset="0" stop-color="white" stop-opacity="1"/>
	<stop offset="1" stop-color="white" stop-opacity="0"/>
</linearGradient>
<mask id="fadeMask" maskUnits="userSpaceOnUse" x="0" y="0" width="100" height="100">
	<rect x="0" y="0" width="100" height="100" fill="url(#fade)"/>
</mask>
</defs>
<rect x="0" y="0" width="100" height="100" fill="red" mask="url(#fadeMask)"/>
</svg>`
	img := drawMaskIcon(t, svg, 100, 100)

	a10, a50, a90 := alphaAt(img, 10, 50), alphaAt(img, 50, 50), alphaAt(img, 90, 50)
	// gradient alpha at x=10,50,90 is roughly 0.9, 0.5, 0.1 of 255
	checkAlpha(t, img, 10, 50, 210, 240)
	checkAlpha(t, img, 50, 50, 110, 145)
	checkAlpha(t, img, 90, 50, 5, 45)
	if !(a10 > a50 && a50 > a90) {
		t.Errorf("alpha should fade left to right, got %d, %d, %d", a10, a50, a90)
	}
	// the fill is red, so the premultiplied red channel tracks the alpha
	c := img.RGBAAt(50, 50)
	if r, a := int(c.R), int(c.A); r < a-10 || r > a+10 {
		t.Errorf("red channel %d does not track alpha %d", r, a)
	}
	if int(c.B) > 10 {
		t.Errorf("unexpected blue channel %d", c.B)
	}
}

// A geometric (circle) mask applied to a transformed, semi-transparent
// group of two overlapping rectangles.
func TestGeometricMaskOnGroup(t *testing.T) {
	const svg = `<svg viewBox="0 0 100 100" width="100" height="100">
<defs>
<mask id="circleMask" maskUnits="userSpaceOnUse" x="0" y="0" width="100" height="100">
	<circle cx="50" cy="50" r="30" fill="white"/>
</mask>
</defs>
<g transform="translate(10, 0)" opacity="0.5" mask="url(#circleMask)">
	<rect x="0" y="0" width="60" height="100" fill="red"/>
	<rect x="30" y="0" width="60" height="100" fill="blue"/>
</g>
</svg>`
	img := drawMaskIcon(t, svg, 100, 100)

	// The mask region follows the group transform: the circle is centered
	// at device (60,50) with radius 30, spanning x in [30,90].
	checkAlpha(t, img, 25, 50, 0, 0)     // left of the shifted circle
	checkAlpha(t, img, 85, 50, 120, 136) // inside shifted circle, blue rect
	checkAlpha(t, img, 95, 50, 0, 0)     // right of the shifted circle
	checkAlpha(t, img, 60, 15, 0, 0)     // above the circle

	// Inside the circle the group opacity of 0.5 applies.
	checkAlpha(t, img, 35, 50, 120, 136) // red rect only (user x=25 < 30)
	c := img.RGBAAt(35, 50)
	if int(c.R) < 110 || int(c.B) > 10 {
		t.Errorf("expected masked red at (35,50), got %+v", c)
	}
	// The blue rect is drawn after the red one and dominates where they
	// overlap; each child keeps the group opacity of 0.5, so the overlap
	// composites to alpha 0.75 (consistent with unmasked groups in oksvg).
	c = img.RGBAAt(60, 50)
	if int(c.B) <= int(c.R) {
		t.Errorf("expected blue over red at (60,50), got %+v", c)
	}
	checkAlpha(t, img, 60, 50, 180, 200)
}

// A mask that is never referenced must not be drawn.
func TestUnreferencedMaskNotDrawn(t *testing.T) {
	const svg = `<svg viewBox="0 0 100 100" width="100" height="100">
<defs>
<mask id="unused" maskUnits="userSpaceOnUse" x="0" y="0" width="100" height="100">
	<rect x="0" y="0" width="100" height="100" fill="white"/>
</mask>
</defs>
</svg>`
	img := drawMaskIcon(t, svg, 100, 100)
	checkAlpha(t, img, 50, 50, 0, 0)
}

// An unknown mask reference leaves the content unmasked.
func TestUnknownMaskReference(t *testing.T) {
	const svg = `<svg viewBox="0 0 100 100" width="100" height="100">
<rect x="0" y="0" width="100" height="100" fill="red" mask="url(#missing)"/>
</svg>`
	img := drawMaskIcon(t, svg, 100, 100)
	checkAlpha(t, img, 50, 50, 255, 255)
}

// A mask with a non-positive region hides the masked content.
func TestNonPositiveMaskRegion(t *testing.T) {
	const svg = `<svg viewBox="0 0 100 100" width="100" height="100">
<defs>
<mask id="empty" maskUnits="userSpaceOnUse" x="0" y="0" width="0" height="100">
	<rect x="0" y="0" width="100" height="100" fill="white"/>
</mask>
</defs>
<rect x="0" y="0" width="100" height="100" fill="red" mask="url(#empty)"/>
</svg>`
	img := drawMaskIcon(t, svg, 100, 100)
	checkAlpha(t, img, 50, 50, 0, 0)
}

// A cyclic mask reference is reported and the content is drawn unmasked.
func TestCyclicMaskReference(t *testing.T) {
	const svg = `<svg viewBox="0 0 100 100" width="100" height="100">
<defs>
<mask id="cyclic" maskUnits="userSpaceOnUse" x="0" y="0" width="100" height="100">
	<rect x="0" y="0" width="100" height="100" fill="white" mask="url(#cyclic)"/>
</mask>
</defs>
<rect x="0" y="0" width="100" height="100" fill="red" mask="url(#cyclic)"/>
</svg>`
	img := drawMaskIcon(t, svg, 100, 100)
	checkAlpha(t, img, 50, 50, 255, 255)
}

// Unsupported mask units leave the content unmasked.
func TestUnsupportedMaskUnits(t *testing.T) {
	const svg = `<svg viewBox="0 0 100 100" width="100" height="100">
<defs>
<mask id="obb" maskUnits="objectBoundingBox">
	<rect x="0" y="0" width="100" height="100" fill="white"/>
</mask>
</defs>
<rect x="0" y="0" width="100" height="100" fill="red" mask="url(#obb)"/>
</svg>`
	img := drawMaskIcon(t, svg, 100, 100)
	checkAlpha(t, img, 50, 50, 255, 255)
}

// Two sibling elements referencing the same mask are masked independently,
// and an unmasked element between them is unaffected.
func TestSiblingsSharingMask(t *testing.T) {
	const svg = `<svg viewBox="0 0 100 100" width="100" height="100">
<defs>
<mask id="half" maskUnits="userSpaceOnUse" x="0" y="0" width="100" height="100">
	<rect x="0" y="0" width="50" height="100" fill="white"/>
</mask>
</defs>
<rect x="0" y="0" width="100" height="40" fill="red" mask="url(#half)"/>
<rect x="0" y="40" width="100" height="20" fill="green"/>
<rect x="0" y="60" width="100" height="40" fill="blue" mask="url(#half)"/>
</svg>`
	img := drawMaskIcon(t, svg, 100, 100)

	checkAlpha(t, img, 25, 20, 255, 255) // masked red, inside mask half
	checkAlpha(t, img, 75, 20, 0, 0)     // masked red, outside mask half
	checkAlpha(t, img, 75, 50, 255, 255) // unmasked green everywhere
	checkAlpha(t, img, 25, 80, 255, 255) // masked blue, inside mask half
	checkAlpha(t, img, 75, 80, 0, 0)     // masked blue, outside mask half
}
