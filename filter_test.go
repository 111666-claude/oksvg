// Copyright 2024 The oksvg Authors. All rights reserved.
package oksvg_test

import (
	"image"
	"strings"
	"testing"

	. "github.com/srwiley/oksvg"
	. "github.com/srwiley/rasterx"
)

const dropShadowFilterDef = `<defs>
	<filter id="ds" x="-50%" y="-50%" width="200%" height="200%">
		<feDropShadow dx="5" dy="3" stdDeviation="2" flood-color="#ff0000" flood-opacity="1"/>
	</filter>
</defs>`

func drawSVGString(t *testing.T, svg string, w, h int) *image.RGBA {
	t.Helper()
	icon, err := ReadIconStream(strings.NewReader(svg), WarnErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	scanner := NewScannerGV(w, h, img, img.Bounds())
	raster := NewDasher(w, h, scanner)
	icon.Draw(raster, 1.0)
	return img
}

// A semi-transparent rectangle with a red drop shadow offset by (5,3).
func TestFeDropShadowRect(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100">` + dropShadowFilterDef +
		`<rect x="20" y="20" width="40" height="40" fill="#0000ff" fill-opacity="0.5" filter="url(#ds)"/></svg>`
	img := drawSVGString(t, svg, 100, 100)

	// Center of the rect: half transparent blue over the red shadow.
	if c := img.RGBAAt(40, 40); c.R < 60 || c.R > 200 || c.B < 60 || c.B > 200 {
		t.Errorf("rect center = %v, want a blue/red blend", c)
	}
	// Below-right of the rect, inside the offset shadow: red, halved by
	// the rect's 0.5 opacity (alpha ~0.5 * blur coverage).
	if s := img.RGBAAt(61, 55); s.R < 60 || s.R > 200 || s.B > 60 {
		t.Errorf("shadow pixel (61,55) = %v, want half-strength red", s)
	}
	// The mirror point on the opposite side must have no shadow:
	// the offset moves the shadow away from it.
	if n := img.RGBAAt(17, 27); n.R > 40 || n.A > 40 {
		t.Errorf("mirror pixel (17,27) = %v, want no shadow", n)
	}
	// Far outside everything: fully transparent.
	if f := img.RGBAAt(90, 90); f.A != 0 {
		t.Errorf("far pixel = %v, want transparent", f)
	}
}

// A group with child elements gets one shadow from the composite alpha.
func TestFeDropShadowGroup(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100">` + dropShadowFilterDef + `
	<g filter="url(#ds)">
		<rect x="20" y="20" width="30" height="30" fill="#00ff00"/>
		<rect x="35" y="35" width="30" height="30" fill="#0000ff"/>
	</g></svg>`
	img := drawSVGString(t, svg, 100, 100)

	// Both children are drawn.
	if c := img.RGBAAt(30, 30); c.G < 200 {
		t.Errorf("first child = %v, want green", c)
	}
	if c := img.RGBAAt(55, 55); c.B < 200 {
		t.Errorf("second child = %v, want blue", c)
	}
	// The union of the children spans (20..65, 20..65); shifted by (5,3)
	// the shadow reaches (25..70, 23..68). Check a point only the shadow
	// of the second child covers.
	if s := img.RGBAAt(68, 60); s.R < 150 || s.G > 60 || s.B > 60 {
		t.Errorf("group shadow pixel (68,60) = %v, want mostly red", s)
	}
	// No shadow on the opposite side.
	if n := img.RGBAAt(15, 15); n.A > 40 {
		t.Errorf("pixel (15,15) = %v, want no shadow", n)
	}
}

// Parent transforms and opacity apply to the shadow as well.
func TestFeDropShadowTransformOpacity(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100">` + dropShadowFilterDef + `
	<g transform="translate(10,5)" opacity="0.5">
		<rect x="20" y="20" width="40" height="40" fill="#0000ff" filter="url(#ds)"/>
	</g></svg>`
	img := drawSVGString(t, svg, 100, 100)

	// The rect moved to (30..70, 25..65) and the shadow to (35..75, 28..68).
	// Shadow alpha is halved by the group opacity: red is about 128.
	if s := img.RGBAAt(73, 60); s.R < 80 || s.R > 180 {
		t.Errorf("shifted, halved shadow pixel (73,60) = %v, want R near 128", s)
	}
	// The untransformed rect and shadow positions stay empty.
	if n := img.RGBAAt(25, 25); n.A > 40 {
		t.Errorf("untransformed position (25,25) = %v, want empty", n)
	}
}

// An explicit filter region clips the shadow and the source consistently.
func TestFeDropShadowRegionClip(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100"><defs>
	<filter id="dsr" x="0%" y="0%" width="100%" height="100%">
		<feDropShadow dx="5" dy="3" stdDeviation="2" flood-color="#ff0000"/>
	</filter></defs>
	<rect x="20" y="20" width="40" height="40" fill="#0000ff" fill-opacity="0.5" filter="url(#dsr)"/></svg>`
	img := drawSVGString(t, svg, 100, 100)

	// The region is exactly the rect bounds, so the offset shadow is
	// clipped away outside them.
	if s := img.RGBAAt(63, 53); s.A != 0 {
		t.Errorf("pixel (63,53) outside filter region = %v, want transparent", s)
	}
	// Inside the region the shadow shows through the half transparent rect.
	if s := img.RGBAAt(58, 58); s.R < 30 || s.B < 60 {
		t.Errorf("pixel (58,58) inside filter region = %v, want a blue/red blend", s)
	}
}

// An unknown filter reference is ignored: the element draws without a shadow.
func TestFilterUnknownReference(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100">
	<rect x="20" y="20" width="40" height="40" fill="#0000ff" filter="url(#missing)"/></svg>`
	img := drawSVGString(t, svg, 100, 100)
	if c := img.RGBAAt(40, 40); c.B < 200 {
		t.Errorf("rect = %v, want blue", c)
	}
	if s := img.RGBAAt(63, 53); s.A != 0 {
		t.Errorf("unknown filter produced a shadow: %v", s)
	}
}

// A negative stdDeviation is rejected: no shadow, source still drawn.
func TestFilterNegativeBlur(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100"><defs>
	<filter id="neg"><feDropShadow dx="5" dy="3" stdDeviation="-2"/></filter></defs>
	<rect x="20" y="20" width="40" height="40" fill="#0000ff" filter="url(#neg)"/></svg>`
	img := drawSVGString(t, svg, 100, 100)
	if c := img.RGBAAt(40, 40); c.B < 200 {
		t.Errorf("rect = %v, want blue", c)
	}
	if s := img.RGBAAt(63, 53); s.A != 0 {
		t.Errorf("negative blur produced a shadow: %v", s)
	}
}

// Complex filter chains are not supported: no shadow, source still drawn.
func TestFilterUnsupportedChain(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100"><defs>
	<filter id="cx"><feGaussianBlur stdDeviation="2"/><feDropShadow dx="5" dy="3"/></filter></defs>
	<rect x="20" y="20" width="40" height="40" fill="#0000ff" filter="url(#cx)"/></svg>`
	img := drawSVGString(t, svg, 100, 100)
	if c := img.RGBAAt(40, 40); c.B < 200 {
		t.Errorf("rect = %v, want blue", c)
	}
	if s := img.RGBAAt(63, 53); s.A != 0 {
		t.Errorf("unsupported chain produced a shadow: %v", s)
	}
}

// When the required offscreen image exceeds MaxFilterOffscreenPixels the
// filter is skipped and the source is drawn without a shadow.
func TestFilterOversizedOffscreen(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100">` + dropShadowFilterDef +
		`<rect x="20" y="20" width="40" height="40" fill="#0000ff" filter="url(#ds)"/></svg>`
	old := MaxFilterOffscreenPixels
	MaxFilterOffscreenPixels = 10
	defer func() { MaxFilterOffscreenPixels = old }()
	img := drawSVGString(t, svg, 100, 100)
	if c := img.RGBAAt(40, 40); c.B < 200 {
		t.Errorf("rect = %v, want blue", c)
	}
	if s := img.RGBAAt(63, 53); s.A != 0 {
		t.Errorf("oversized offscreen still produced a shadow: %v", s)
	}
}

// Icons without filters are unaffected by the filter support.
func TestNoFilterUnchanged(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100">
	<rect x="20" y="20" width="40" height="40" fill="#0000ff" fill-opacity="0.5"/></svg>`
	img := drawSVGString(t, svg, 100, 100)
	if c := img.RGBAAt(40, 40); c.B < 100 || c.R != 0 {
		t.Errorf("rect = %v, want half transparent blue", c)
	}
	if s := img.RGBAAt(63, 53); s.A != 0 {
		t.Errorf("unfiltered rect produced output at (63,53): %v", s)
	}
}
