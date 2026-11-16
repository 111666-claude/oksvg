// Copyright 2026 The oksvg Authors. All rights reserved.
//
// Whitebox tests for mask rendering internals.
package oksvg

import (
	"image"
	"strings"
	"testing"

	"github.com/srwiley/rasterx"
)

// An oversized mask region falls back to unmasked drawing instead of
// allocating a huge offscreen buffer.
func TestOversizedMaskBufferGuard(t *testing.T) {
	old := maxMaskBufferPixels
	defer func() { maxMaskBufferPixels = old }()
	maxMaskBufferPixels = 50 // tiny, so the 100x100 mask region trips the guard

	const svg = `<svg viewBox="0 0 100 100" width="100" height="100">
<defs>
<mask id="big" maskUnits="userSpaceOnUse" x="0" y="0" width="100" height="100">
	<rect x="0" y="0" width="100" height="100" fill="white"/>
</mask>
</defs>
<rect x="0" y="0" width="100" height="100" fill="red" mask="url(#big)"/>
</svg>`
	icon, err := ReadIconStream(strings.NewReader(svg), StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	scannerGV := rasterx.NewScannerGV(100, 100, img, img.Bounds())
	raster := rasterx.NewDasher(100, 100, scannerGV)
	icon.Draw(raster, 1.0)
	if a := img.RGBAAt(50, 50).A; a != 255 {
		t.Errorf("expected unmasked fallback with alpha 255, got %d", a)
	}
}

// Mask length parsing supports plain numbers, units and percentages.
func TestParseMaskLength(t *testing.T) {
	if v, err := parseMaskLength("50", 200); err != nil || v != 50 {
		t.Errorf("parseMaskLength(50) = %v, %v", v, err)
	}
	if v, err := parseMaskLength("25%", 200); err != nil || v != 50 {
		t.Errorf("parseMaskLength(25%%) = %v, %v", v, err)
	}
	if v, err := parseMaskLength("10px", 200); err != nil || v != 10 {
		t.Errorf("parseMaskLength(10px) = %v, %v", v, err)
	}
	if _, err := parseMaskLength("abc", 200); err == nil {
		t.Error("expected error for invalid mask length")
	}
}
