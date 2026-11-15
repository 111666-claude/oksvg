// Copyright 2026 The oksvg Authors. All rights reserved.
//
// clip_test.go tests basic SVG clipPath rendering: rect, circle and path
// clip shapes defined in defs, used by shapes and groups, with transforms,
// union and intersection semantics, and explicit error results.
package oksvg_test

import (
	"image"
	"image/color"
	"strings"
	"testing"

	. "github.com/srwiley/oksvg"
	. "github.com/srwiley/rasterx"
)

func DrawIconStrict(t *testing.T, iconPath string) *image.RGBA {
	t.Helper()
	icon, err := ReadIcon(iconPath, StrictErrorMode)
	if err != nil {
		t.Fatalf("failed to read %s: %v", iconPath, err)
	}
	w, h := int(icon.ViewBox.W), int(icon.ViewBox.H)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	scannerGV := NewScannerGV(w, h, img, img.Bounds())
	raster := NewDasher(w, h, scannerGV)
	icon.Draw(raster, 1.0)
	return img
}

func DrawStringStrict(t *testing.T, svg string) *image.RGBA {
	t.Helper()
	icon, err := ReadIconStream(strings.NewReader(svg), StrictErrorMode)
	if err != nil {
		t.Fatalf("failed to read svg: %v", err)
	}
	w, h := int(icon.ViewBox.W), int(icon.ViewBox.H)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	scannerGV := NewScannerGV(w, h, img, img.Bounds())
	raster := NewDasher(w, h, scannerGV)
	icon.Draw(raster, 1.0)
	return img
}

var transparent = color.RGBA{0, 0, 0, 0}

func expectPixel(t *testing.T, img *image.RGBA, x, y int, want color.RGBA) {
	t.Helper()
	got := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
	if got != want {
		t.Errorf("pixel (%d,%d) = %v, want %v", x, y, got, want)
	}
}

func hasPartialAlpha(img *image.RGBA) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a > 0 && a < 0xffff {
				return true
			}
		}
	}
	return false
}

func TestClipRectIcon(t *testing.T) {
	img := DrawIconStrict(t, "testdata/TestClipRect.svg")
	red := color.RGBA{0xff, 0, 0, 0xff}
	expectPixel(t, img, 50, 50, red)         // inside clip rect
	expectPixel(t, img, 21, 21, red)         // just inside corner
	expectPixel(t, img, 79, 79, red)         // just inside far edge
	expectPixel(t, img, 10, 10, transparent) // outside clip keeps background
	expectPixel(t, img, 85, 50, transparent)
	expectPixel(t, img, 50, 85, transparent)
}

func TestClipCircleIcon(t *testing.T) {
	img := DrawIconStrict(t, "testdata/TestClipCircle.svg")
	blue := color.RGBA{0, 0, 0xff, 0xff}
	expectPixel(t, img, 50, 50, blue)        // center
	expectPixel(t, img, 50, 22, blue)        // inside radius
	expectPixel(t, img, 50, 18, transparent) // outside radius
	expectPixel(t, img, 10, 10, transparent) // corner far outside
	if !hasPartialAlpha(img) {
		t.Error("expected anti-aliased edge pixels on circle clip boundary")
	}
}

func TestClipPathIcon(t *testing.T) {
	img := DrawIconStrict(t, "testdata/TestClipPath.svg")
	green := color.RGBA{0, 0xff, 0, 0xff}
	expectPixel(t, img, 20, 20, green) // inside triangle
	expectPixel(t, img, 80, 80, transparent)
	expectPixel(t, img, 90, 5, transparent)
}

func TestClipGroupIcon(t *testing.T) {
	img := DrawIconStrict(t, "testdata/TestClipGroup.svg")
	red := color.RGBA{0xff, 0, 0, 0xff}
	blue := color.RGBA{0, 0, 0xff, 0xff}
	expectPixel(t, img, 25, 25, red)  // group clip applies to first child
	expectPixel(t, img, 25, 75, blue) // and to second child
	expectPixel(t, img, 75, 25, transparent)
	expectPixel(t, img, 75, 75, transparent)
}

func TestClipTransformIcon(t *testing.T) {
	img := DrawIconStrict(t, "testdata/TestClipTransform.svg")
	red := color.RGBA{0xff, 0, 0, 0xff}
	// group translate(50,10) moves both the rect and its user-space clip
	expectPixel(t, img, 60, 20, red)
	expectPixel(t, img, 10, 10, transparent)
	expectPixel(t, img, 75, 35, transparent)
}

func TestClipInnerTransformIcon(t *testing.T) {
	img := DrawIconStrict(t, "testdata/TestClipInnerTransform.svg")
	red := color.RGBA{0xff, 0, 0, 0xff}
	// transform on a group inside the clipPath shifts the clip shape
	expectPixel(t, img, 50, 50, red)
	expectPixel(t, img, 10, 50, transparent)
	expectPixel(t, img, 65, 50, transparent)
}

func TestClipNestedIcon(t *testing.T) {
	img := DrawIconStrict(t, "testdata/TestClipNested.svg")
	red := color.RGBA{0xff, 0, 0, 0xff}
	// parent and child clips intersect: only [30,60)x[30,60) is visible
	expectPixel(t, img, 45, 45, red)
	expectPixel(t, img, 15, 15, transparent)
	expectPixel(t, img, 75, 75, transparent)
	expectPixel(t, img, 15, 45, transparent)
	expectPixel(t, img, 45, 15, transparent)
}

func TestClipUnionIcon(t *testing.T) {
	img := DrawIconStrict(t, "testdata/TestClipUnion.svg")
	red := color.RGBA{0xff, 0, 0, 0xff}
	// two rects in one clipPath union; note the clipPath sits outside
	// defs, so its shapes must not be drawn directly (would be black)
	expectPixel(t, img, 20, 20, red)
	expectPixel(t, img, 80, 80, red)
	expectPixel(t, img, 50, 50, transparent)
	expectPixel(t, img, 20, 80, transparent)
}

func TestClipBadgeIcon(t *testing.T) {
	img := DrawIconStrict(t, "testdata/TestClipBadge.svg")
	red := color.RGBA{0xff, 0, 0, 0xff}
	badge := color.RGBA{0xee, 0xee, 0xee, 0xff}
	// stripe 1 runs through (50,55): visible inside the badge outline
	expectPixel(t, img, 50, 55, red)
	// stripe 1 also passes near (2,31), but that is outside the badge
	// circle, so the background must be preserved there
	expectPixel(t, img, 2, 31, transparent)
	// badge fill shows between the stripes
	expectPixel(t, img, 50, 50, badge)
	// outside the badge nothing is drawn
	expectPixel(t, img, 95, 50, transparent)
}

func TestClipErrors(t *testing.T) {
	cases := map[string]string{
		"circular reference": `<svg width="10" height="10" viewBox="0 0 10 10">
			<defs>
				<clipPath id="ca"><rect width="5" height="5" clip-path="url(#cb)"/></clipPath>
				<clipPath id="cb"><rect width="5" height="5" clip-path="url(#ca)"/></clipPath>
			</defs>
			<rect width="10" height="10" clip-path="url(#ca)"/>
		</svg>`,
		"objectBoundingBox units": `<svg width="10" height="10" viewBox="0 0 10 10">
			<defs><clipPath id="obb" clipPathUnits="objectBoundingBox"><rect width="1" height="1"/></clipPath></defs>
			<rect width="10" height="10" clip-path="url(#obb)"/>
		</svg>`,
		"missing reference": `<svg width="10" height="10" viewBox="0 0 10 10">
			<rect width="10" height="10" clip-path="url(#nope)"/>
		</svg>`,
		"invalid path in clipPath": `<svg width="10" height="10" viewBox="0 0 10 10">
			<defs><clipPath id="bp"><path d="M10 10 Q"/></clipPath></defs>
			<rect width="10" height="10" clip-path="url(#bp)"/>
		</svg>`,
	}
	for name, svg := range cases {
		_, err := ReadIconStream(strings.NewReader(svg), StrictErrorMode)
		if err == nil {
			t.Errorf("%s: expected an error in StrictErrorMode, got nil", name)
		}
	}
}

func TestUnusedClipDoesNotChangeOutput(t *testing.T) {
	plain := `<svg width="20" height="20" viewBox="0 0 20 20">
		<rect width="20" height="20" fill="#123456"/>
	</svg>`
	withDefs := `<svg width="20" height="20" viewBox="0 0 20 20">
		<defs><clipPath id="u"><rect width="5" height="5"/></clipPath></defs>
		<rect width="20" height="20" fill="#123456"/>
	</svg>`
	a := DrawStringStrict(t, plain)
	b := DrawStringStrict(t, withDefs)
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			ca := color.RGBAModel.Convert(a.At(x, y)).(color.RGBA)
			cb := color.RGBAModel.Convert(b.At(x, y)).(color.RGBA)
			if ca != cb {
				t.Fatalf("pixel (%d,%d) differs: %v vs %v", x, y, ca, cb)
			}
		}
	}
}
