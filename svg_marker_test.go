// Copyright 2026 The oksvg Authors. All rights reserved.
//
// svg_marker_test.go verifies SVG marker rendering: positions and directions
// are checked per pixel for lines, polylines, curves and varying stroke
// widths, and error handling for unknown, cyclic and invalid markers.

package oksvg

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
	"testing"

	"github.com/srwiley/rasterx"
)

var (
	pixelWhite = color.RGBA{0xff, 0xff, 0xff, 0xff}
	pixelRed   = color.RGBA{0xff, 0x00, 0x00, 0xff}
	pixelBlue  = color.RGBA{0x00, 0x00, 0xff, 0xff}
	pixelGreen = color.RGBA{0x00, 0x80, 0x00, 0xff}
)

// renderSVG parses the SVG string and draws it on a white background.
func renderSVG(t *testing.T, svg string, w, h int, errMode ...ErrorMode) *image.RGBA {
	t.Helper()
	icon, err := ReadIconStream(strings.NewReader(svg), errMode...)
	if err != nil {
		t.Fatalf("ReadIconStream: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{pixelWhite}, image.Point{}, draw.Src)
	scanner := rasterx.NewScannerGV(w, h, img, img.Bounds())
	raster := rasterx.NewDasher(w, h, scanner)
	icon.Draw(raster, 1.0)
	return img
}

func expectPixel(t *testing.T, img *image.RGBA, x, y int, want color.RGBA) {
	t.Helper()
	if got := img.RGBAAt(x, y); got != want {
		t.Errorf("pixel (%d,%d): got %v, want %v", x, y, got, want)
	}
}

// expectPixelNear allows a small tolerance for opacity blending results.
func expectPixelNear(t *testing.T, img *image.RGBA, x, y int, want color.RGBA, tol uint8) {
	t.Helper()
	got := img.RGBAAt(x, y)
	d := func(a, b uint8) int {
		d := int(a) - int(b)
		if d < 0 {
			return -d
		}
		return d
	}
	if d(got.R, want.R) > int(tol) || d(got.G, want.G) > int(tol) ||
		d(got.B, want.B) > int(tol) || d(got.A, want.A) > int(tol) {
		t.Errorf("pixel (%d,%d): got %v, want %v +/- %d", x, y, got, want, tol)
	}
}

// arrowMarkerSVG wraps body SVG with a defs section containing a red arrow
// head marker: tip at (10,5) of a 10x10 viewBox, pointing along +x.
func arrowMarkerSVG(body, markerAttrs string) string {
	return `<svg viewBox="0 0 100 100" width="100" height="100">` +
		`<defs><marker id="arrow" viewBox="0 0 10 10" refX="10" refY="5" ` +
		`markerWidth="10" markerHeight="10" orient="auto" ` + markerAttrs + `>` +
		`<path d="M0,0 L10,5 L0,10 z" fill="red"/>` +
		`</marker></defs>` + body + `</svg>`
}

func TestMarkerLineEndRight(t *testing.T) {
	// Horizontal line, arrow head at the end vertex (90,50) pointing right.
	// stroke-width 1 => strokeWidth units scale is 1, viewBox scale is 1.
	img := renderSVG(t, arrowMarkerSVG(
		`<line x1="10" y1="50" x2="90" y2="50" stroke="black" stroke-width="1" marker-end="url(#arrow)"/>`, ""),
		100, 100)
	// Arrow body spans x in [80,90]; at x=85 the triangle covers y in [47.5,52.5].
	expectPixel(t, img, 85, 50, pixelRed)
	expectPixel(t, img, 82, 50, pixelRed)
	expectPixel(t, img, 86, 50, pixelRed)
	expectPixel(t, img, 85, 45, pixelWhite) // above the triangle
	expectPixel(t, img, 85, 55, pixelWhite) // below the triangle
	expectPixel(t, img, 75, 48, pixelWhite) // behind the marker, off the line
	expectPixel(t, img, 95, 50, pixelWhite) // beyond the tip
}

func TestMarkerLineEndLeft(t *testing.T) {
	// Same line reversed: the arrow must point left at vertex (10,50).
	img := renderSVG(t, arrowMarkerSVG(
		`<line x1="90" y1="50" x2="10" y2="50" stroke="black" stroke-width="1" marker-end="url(#arrow)"/>`, ""),
		100, 100)
	// Arrow body spans x in [10,20]; at x=15 the triangle covers y in [47.5,52.5].
	expectPixel(t, img, 15, 50, pixelRed)
	expectPixel(t, img, 12, 50, pixelRed)
	expectPixel(t, img, 15, 45, pixelWhite)
	expectPixel(t, img, 15, 55, pixelWhite)
	expectPixel(t, img, 5, 50, pixelWhite)  // beyond the tip
	expectPixel(t, img, 25, 48, pixelWhite) // behind the marker
}

func TestMarkerStartAndDiagonal(t *testing.T) {
	// Diagonal line from top-left to bottom-right, arrow at the start vertex
	// pointing along the line direction (down-right).
	img := renderSVG(t, arrowMarkerSVG(
		`<line x1="20" y1="20" x2="80" y2="80" stroke="none" marker-start="url(#arrow)"/>`, ""),
		100, 100)
	// The arrow tip is at (20,20); the body extends up-left along the diagonal.
	// A point 5 units behind the tip along the line is inside the triangle.
	expectPixel(t, img, 17, 17, pixelRed)
	// The same distance perpendicular off the line is outside.
	expectPixel(t, img, 13, 21, pixelWhite)
	expectPixel(t, img, 21, 13, pixelWhite)
	// Ahead of the tip there is nothing.
	expectPixel(t, img, 24, 24, pixelWhite)
}

func TestMarkerMidPolyline(t *testing.T) {
	// Blue circle mid marker centered on each interior vertex.
	svg := `<svg viewBox="0 0 100 100" width="100" height="100">` +
		`<defs><marker id="dot" markerWidth="8" markerHeight="8" refX="4" refY="4" ` +
		`markerUnits="userSpaceOnUse"><circle cx="4" cy="4" r="3" fill="blue"/></marker></defs>` +
		`<polyline points="10,50 50,50 50,90" fill="none" stroke="none" marker-mid="url(#dot)"/>` +
		`</svg>`
	img := renderSVG(t, svg, 100, 100)
	// Mid vertex at (50,50): circle of radius 3 centered there.
	expectPixel(t, img, 50, 50, pixelBlue)
	expectPixel(t, img, 49, 49, pixelBlue)
	expectPixel(t, img, 56, 50, pixelWhite) // outside radius 3
	expectPixel(t, img, 50, 44, pixelWhite)
	// Start and end vertices carry no marker.
	expectPixel(t, img, 10, 50, pixelWhite)
	expectPixel(t, img, 50, 90, pixelWhite)
}

func TestMarkerCurveTangents(t *testing.T) {
	// Symmetric cubic: both end tangents are vertical.
	img := renderSVG(t, arrowMarkerSVG(
		`<path d="M10,50 C10,10 90,10 90,50" fill="none" stroke="none" `+
			`marker-start="url(#arrow)" marker-end="url(#arrow)"/>`, ""),
		100, 100)
	// End tangent points down (+y): arrow at (90,50) with body above it.
	expectPixel(t, img, 90, 45, pixelRed)
	expectPixel(t, img, 90, 42, pixelRed)
	expectPixel(t, img, 85, 45, pixelWhite) // left of the vertical arrow
	expectPixel(t, img, 95, 45, pixelWhite) // right of the vertical arrow
	expectPixel(t, img, 90, 55, pixelWhite) // beyond the tip
	// Start tangent points up (-y): arrow at (10,50) with body below it.
	expectPixel(t, img, 10, 55, pixelRed)
	expectPixel(t, img, 10, 58, pixelRed)
	expectPixel(t, img, 5, 55, pixelWhite)
	expectPixel(t, img, 15, 55, pixelWhite)
	expectPixel(t, img, 10, 45, pixelWhite)
}

func TestMarkerStrokeWidthUnits(t *testing.T) {
	// Green square marker: viewBox 10x10 mapped to a 5x5 viewport, refX=0
	// refY=5 so it extends forward from the vertex, vertically centered.
	mk := func(body string) string {
		return `<svg viewBox="0 0 120 100" width="120" height="100">` +
			`<defs><marker id="sq" viewBox="0 0 10 10" refX="0" refY="5" ` +
			`markerWidth="5" markerHeight="5" orient="auto">` +
			`<rect x="0" y="0" width="10" height="10" fill="green"/></marker></defs>` +
			body + `</svg>`
	}
	// stroke-width 4 => total scale 0.5*4 = 2: square covers x in [90,110],
	// y in [40,60].
	img := renderSVG(t, mk(
		`<line x1="50" y1="50" x2="90" y2="50" stroke="black" stroke-width="4" marker-end="url(#sq)"/>`),
		120, 100)
	expectPixel(t, img, 95, 50, pixelGreen)
	expectPixel(t, img, 105, 55, pixelGreen)
	expectPixel(t, img, 105, 45, pixelGreen)
	expectPixel(t, img, 100, 65, pixelWhite) // outside the 20x20 square
	expectPixel(t, img, 112, 50, pixelWhite)
	// stroke-width 1 => total scale 0.5: square covers x in [90,95],
	// y in [47.5,52.5].
	img = renderSVG(t, mk(
		`<line x1="50" y1="50" x2="90" y2="50" stroke="black" stroke-width="1" marker-end="url(#sq)"/>`),
		120, 100)
	expectPixel(t, img, 92, 50, pixelGreen)
	expectPixel(t, img, 100, 50, pixelWhite) // beyond the small square
	expectPixel(t, img, 92, 45, pixelWhite)
}

func TestMarkerClosedPathSeam(t *testing.T) {
	// Closed triangle: every vertex gets a dot; the seam vertex (20,20) gets
	// both start and end markers.
	svg := `<svg viewBox="0 0 100 100" width="100" height="100">` +
		`<defs><marker id="dot" markerWidth="8" markerHeight="8" refX="4" refY="4" ` +
		`markerUnits="userSpaceOnUse"><circle cx="4" cy="4" r="3" fill="blue"/></marker></defs>` +
		`<path d="M20,20 L80,20 L50,80 Z" fill="none" stroke="none" ` +
		`marker-start="url(#dot)" marker-mid="url(#dot)" marker-end="url(#dot)"/>` +
		`</svg>`
	img := renderSVG(t, svg, 100, 100)
	expectPixel(t, img, 20, 20, pixelBlue) // seam vertex
	expectPixel(t, img, 80, 20, pixelBlue) // mid vertex
	expectPixel(t, img, 50, 80, pixelBlue) // mid vertex
	expectPixel(t, img, 50, 50, pixelWhite)
	expectPixel(t, img, 30, 20, pixelWhite) // on the edge, but no marker there
}

func TestMarkerZeroLengthSegments(t *testing.T) {
	// The first segment is zero-length; the start marker must still be placed
	// at (10,50) with the direction of the following segment (to the right).
	img := renderSVG(t, arrowMarkerSVG(
		`<path d="M10,50 L10,50 L90,50" fill="none" stroke="none" `+
			`marker-start="url(#arrow)" marker-end="url(#arrow)"/>`, ""),
		100, 100)
	// Start arrow at (10,50) pointing right: body spans x in [0,10].
	expectPixel(t, img, 5, 50, pixelRed)
	expectPixel(t, img, 5, 45, pixelWhite)
	// End arrow at (90,50) pointing right.
	expectPixel(t, img, 85, 50, pixelRed)
}

func TestMarkerDegeneratePath(t *testing.T) {
	// A subpath with only zero-length segments produces no markers.
	img := renderSVG(t, arrowMarkerSVG(
		`<path d="M50,50 L50,50 Z" fill="none" stroke="none" `+
			`marker-start="url(#arrow)" marker-mid="url(#arrow)" marker-end="url(#arrow)"/>`, ""),
		100, 100)
	expectPixel(t, img, 50, 50, pixelWhite)
	expectPixel(t, img, 45, 50, pixelWhite)
	expectPixel(t, img, 55, 50, pixelWhite)
}

func TestMarkerFixedOrient(t *testing.T) {
	// orient="90" rotates the arrow 90 degrees clockwise regardless of the
	// horizontal line direction: body extends upward from the vertex.
	img := renderSVG(t, arrowMarkerSVG(
		`<line x1="10" y1="50" x2="90" y2="50" stroke="none" marker-end="url(#arrow)"/>`, `orient="90"`),
		100, 100)
	expectPixel(t, img, 90, 45, pixelRed)
	expectPixel(t, img, 85, 45, pixelWhite)
	expectPixel(t, img, 90, 55, pixelWhite)
}

func TestMarkerAutoStartReverse(t *testing.T) {
	// With auto-start-reverse the start marker points back along the path:
	// tip at (10,50), body to the right of it.
	img := renderSVG(t, arrowMarkerSVG(
		`<line x1="10" y1="50" x2="90" y2="50" stroke="none" marker-start="url(#arrow)"/>`,
		`orient="auto-start-reverse"`),
		100, 100)
	expectPixel(t, img, 15, 50, pixelRed)
	expectPixel(t, img, 18, 50, pixelRed)
	expectPixel(t, img, 5, 50, pixelWhite)
}

func TestMarkerFollowsTransform(t *testing.T) {
	// The group transform shifts the line 20 units right; the marker must follow.
	img := renderSVG(t, arrowMarkerSVG(
		`<g transform="translate(20,0)">`+
			`<line x1="10" y1="50" x2="70" y2="50" stroke="none" marker-end="url(#arrow)"/>`+
			`</g>`, ""),
		100, 100)
	// Tip at (90,50); body spans x in [80,90].
	expectPixel(t, img, 85, 50, pixelRed)
	expectPixel(t, img, 65, 50, pixelWhite) // where the body would be untransformed
}

func TestMarkerFollowsOpacity(t *testing.T) {
	// Element opacity 0.5 applies to the marker: red at 50% over white.
	img := renderSVG(t, arrowMarkerSVG(
		`<line x1="10" y1="50" x2="90" y2="50" stroke="none" opacity="0.5" marker-end="url(#arrow)"/>`, ""),
		100, 100)
	expectPixelNear(t, img, 85, 50, color.RGBA{0xff, 0x7f, 0x7f, 0xff}, 3)
}

func TestMarkerUnreferencedDefNotDrawn(t *testing.T) {
	// A marker defined but never referenced must leave the image unchanged.
	img := renderSVG(t, arrowMarkerSVG(
		`<line x1="10" y1="50" x2="90" y2="50" stroke="none"/>`, ""), 100, 100)
	for _, pt := range [][2]int{{85, 50}, {50, 50}, {10, 50}, {90, 45}} {
		expectPixel(t, img, pt[0], pt[1], pixelWhite)
	}
}

func TestMarkerUnknownReference(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100" width="100" height="100">` +
		`<line x1="10" y1="50" x2="90" y2="50" stroke="none" marker-end="url(#nope)"/></svg>`
	// Warn mode: no error, nothing drawn.
	img := renderSVG(t, svg, 100, 100, WarnErrorMode)
	expectPixel(t, img, 85, 50, pixelWhite)
	expectPixel(t, img, 90, 50, pixelWhite)
	// Strict mode: the unknown reference is an error.
	if _, err := ReadIconStream(strings.NewReader(svg), StrictErrorMode); err == nil {
		t.Error("expected error for unknown marker reference in StrictErrorMode")
	}
}

func TestMarkerInvalidSize(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100" width="100" height="100">` +
		`<defs><marker id="bad" markerWidth="-5" refX="0" refY="0">` +
		`<path d="M0,0 L10,5 L0,10 z" fill="red"/></marker></defs>` +
		`<line x1="10" y1="50" x2="90" y2="50" stroke="none" marker-end="url(#bad)"/></svg>`
	// Warn mode: the invalid marker is skipped, the image stays blank.
	img := renderSVG(t, svg, 100, 100, WarnErrorMode)
	expectPixel(t, img, 85, 50, pixelWhite)
	// Strict mode: invalid marker definitions are an error.
	if _, err := ReadIconStream(strings.NewReader(svg), StrictErrorMode); err == nil {
		t.Error("expected error for negative markerWidth in StrictErrorMode")
	}
}

func TestMarkerZeroWidthDisabled(t *testing.T) {
	// markerWidth 0 disables rendering per spec, without an error.
	svg := `<svg viewBox="0 0 100 100" width="100" height="100">` +
		`<defs><marker id="zero" markerWidth="0" refX="0" refY="0">` +
		`<path d="M0,0 L10,5 L0,10 z" fill="red"/></marker></defs>` +
		`<line x1="10" y1="50" x2="90" y2="50" stroke="none" marker-end="url(#zero)"/></svg>`
	img := renderSVG(t, svg, 100, 100, StrictErrorMode)
	expectPixel(t, img, 85, 50, pixelWhite)
}

func TestMarkerSelfReferenceNoRecursion(t *testing.T) {
	// A marker whose content references itself must terminate: markers inside
	// marker content are not resolved, so the content draws without markers.
	svg := `<svg viewBox="0 0 100 100" width="100" height="100">` +
		`<defs><marker id="a" viewBox="0 0 10 10" refX="10" refY="5" ` +
		`markerWidth="10" markerHeight="10" orient="auto">` +
		`<path d="M0,0 L10,5 L0,10 z" fill="red" marker-end="url(#a)"/>` +
		`</marker></defs>` +
		`<line x1="10" y1="50" x2="90" y2="50" stroke="none" marker-end="url(#a)"/></svg>`
	img := renderSVG(t, svg, 100, 100, WarnErrorMode)
	expectPixel(t, img, 85, 50, pixelRed)
}

func TestMarkerUnsupportedOrientUnit(t *testing.T) {
	// orient in radians is not supported: warn and skip the marker.
	svg := `<svg viewBox="0 0 100 100" width="100" height="100">` +
		`<defs><marker id="o" viewBox="0 0 10 10" refX="10" refY="5" orient="1.5rad">` +
		`<path d="M0,0 L10,5 L0,10 z" fill="red"/></marker></defs>` +
		`<line x1="10" y1="50" x2="90" y2="50" stroke="none" marker-end="url(#o)"/></svg>`
	img := renderSVG(t, svg, 100, 100, WarnErrorMode)
	expectPixel(t, img, 85, 50, pixelWhite)
	if _, err := ReadIconStream(strings.NewReader(svg), StrictErrorMode); err == nil {
		t.Error("expected error for unsupported orient unit in StrictErrorMode")
	}
}

// TestMarkerVertexAngles checks the extracted vertex directions directly.
func TestMarkerVertexAngles(t *testing.T) {
	deg := func(r float64) float64 { return r * 180 / math.Pi }
	c := &PathCursor{}
	if err := c.CompilePath("M0,0 L10,0 L10,10"); err != nil {
		t.Fatal(err)
	}
	verts := markerVerticesForPath(c.Path)
	if len(verts) != 3 {
		t.Fatalf("expected 3 vertices, got %d", len(verts))
	}
	if verts[0].kind != markerKindStart || verts[1].kind != markerKindMid || verts[2].kind != markerKindEnd {
		t.Fatalf("wrong vertex kinds: %v", verts)
	}
	if a := deg(verts[0].angle); math.Abs(a-0) > 1e-9 {
		t.Errorf("start angle: got %v, want 0", a)
	}
	// Mid vertex: bisector of +x and +y directions is 45 degrees.
	if a := deg(verts[1].angle); math.Abs(a-45) > 1e-9 {
		t.Errorf("mid angle: got %v, want 45", a)
	}
	if a := deg(verts[2].angle); math.Abs(a-90) > 1e-9 {
		t.Errorf("end angle: got %v, want 90", a)
	}
	if verts[1].x != 10 || verts[1].y != 0 {
		t.Errorf("mid vertex at (%v,%v), want (10,0)", verts[1].x, verts[1].y)
	}
}

// TestMarkerClosedSeamAngles checks that the closing segment determines the
// end direction at the seam of a closed subpath.
func TestMarkerClosedSeamAngles(t *testing.T) {
	deg := func(r float64) float64 { return r * 180 / math.Pi }
	c := &PathCursor{}
	if err := c.CompilePath("M0,0 L10,0 L10,10 Z"); err != nil {
		t.Fatal(err)
	}
	verts := markerVerticesForPath(c.Path)
	if len(verts) != 4 { // start, mid, mid, end(seam)
		t.Fatalf("expected 4 vertices, got %d", len(verts))
	}
	// End vertex coincides with the start vertex at (0,0) and points along
	// the closing segment from (10,10) to (0,0): 225 degrees.
	if verts[3].kind != markerKindEnd || verts[3].x != 0 || verts[3].y != 0 {
		t.Fatalf("bad seam end vertex: %+v", verts[3])
	}
	if a := deg(verts[3].angle); math.Abs(a-(-135)) > 1e-9 {
		t.Errorf("seam end angle: got %v, want -135", a)
	}
}
