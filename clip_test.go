// Copyright 2026 The oksvg Authors. All rights reserved.
// created: 2026
package oksvg_test

import (
	"image"
	"strings"
	"testing"

	. "github.com/srwiley/oksvg"
	. "github.com/srwiley/rasterx"
)

func readClipIcon(t *testing.T, svg string) *SvgIcon {
	t.Helper()
	icon, err := ReadIconStream(strings.NewReader(svg), StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	return icon
}

func drawClipIcon(t *testing.T, icon *SvgIcon, w, h int) *image.RGBA {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	scanner := NewScannerGV(w, h, img, img.Bounds())
	raster := NewDasher(w, h, scanner)
	icon.Draw(raster, 1.0)
	return img
}

func pixelDrawn(img *image.RGBA, x, y int) bool {
	_, _, _, a := img.At(x, y).RGBA()
	return a != 0
}

func expectDrawn(t *testing.T, img *image.RGBA, x, y int) {
	t.Helper()
	if !pixelDrawn(img, x, y) {
		t.Errorf("expected drawn pixel at (%d, %d)", x, y)
	}
}

func expectBlank(t *testing.T, img *image.RGBA, x, y int) {
	t.Helper()
	if pixelDrawn(img, x, y) {
		t.Errorf("expected blank pixel at (%d, %d)", x, y)
	}
}

// rowTransition returns the first x >= xStart on row y whose pixel is blank.
func rowTransition(img *image.RGBA, y, xStart int) int {
	for x := xStart; x < img.Bounds().Max.X; x++ {
		if !pixelDrawn(img, x, y) {
			return x
		}
	}
	return -1
}

// colTransition returns the first y >= yStart in column x whose pixel is blank.
func colTransition(img *image.RGBA, x, yStart int) int {
	for y := yStart; y < img.Bounds().Max.Y; y++ {
		if !pixelDrawn(img, x, y) {
			return y
		}
	}
	return -1
}

func expectBoundary(t *testing.T, got, want, tol int, what string) {
	t.Helper()
	if got < want-tol || got > want+tol {
		t.Errorf("%s boundary at %d, want %d +/- %d", what, got, want, tol)
	}
}

// TestClipLineBoundary clips a straight edged bar with a rectangle and
// verifies the clip boundary.
func TestClipLineBoundary(t *testing.T) {
	icon, err := ReadIcon("testdata/TestClipLine.svg", StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	img := drawClipIcon(t, icon, 100, 100)
	expectDrawn(t, img, 25, 50)
	expectBlank(t, img, 75, 50)
	expectBlank(t, img, 25, 20) // outside the bar itself
	expectBoundary(t, rowTransition(img, 50, 0), 50, 3, "line clip")
}

// TestClipBezierBoundary clips with a clipPath made of a cubic Bezier and
// verifies the curved clip boundary.
func TestClipBezierBoundary(t *testing.T) {
	icon, err := ReadIcon("testdata/TestClipBezier.svg", StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	img := drawClipIcon(t, icon, 100, 100)
	// The cubic from (100,50) to (0,50) with control points (60,90),(40,90)
	// passes through (50,80); the clip region is above the curve.
	expectDrawn(t, img, 50, 70)
	expectBlank(t, img, 50, 90)
	expectBoundary(t, colTransition(img, 50, 0), 80, 4, "bezier clip")
}

// TestClipRotatedGroup checks that a clip-path on a transformed group is
// applied in the group's user space: both content and clip rotate together.
func TestClipRotatedGroup(t *testing.T) {
	icon, err := ReadIcon("testdata/TestClipRotatedGroup.svg", StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	img := drawClipIcon(t, icon, 100, 100)
	// rotate(90 50 50) maps local (x,y) to device (100-y, x): the horizontal
	// bar becomes the vertical strip x in [40,60] and the local clip rect
	// x in [0,60] becomes device y in [0,60].
	expectDrawn(t, img, 50, 30)
	expectBlank(t, img, 50, 80) // beyond the rotated clip rect
	expectBlank(t, img, 30, 30) // outside the rotated bar
	expectBoundary(t, colTransition(img, 50, 0), 60, 3, "rotated group clip")
}

// TestClipObjectBoundingBox checks clipPathUnits="objectBoundingBox".
func TestClipObjectBoundingBox(t *testing.T) {
	icon, err := ReadIcon("testdata/TestClipOBB.svg", StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	img := drawClipIcon(t, icon, 100, 100)
	// Element bbox is (20,20,60,60); the clip rect is the left half of it.
	expectDrawn(t, img, 30, 50)
	expectBlank(t, img, 70, 50)
	expectBoundary(t, rowTransition(img, 50, 20), 50, 3, "objectBoundingBox clip")
}

// TestClipWithTargetTransform verifies the icon transform is applied to the
// clip path as well as to the clipped element.
func TestClipWithTargetTransform(t *testing.T) {
	icon, err := ReadIcon("testdata/TestClipLine.svg", StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	icon.SetTarget(0, 0, 200, 200)
	img := drawClipIcon(t, icon, 200, 200)
	expectDrawn(t, img, 50, 100)
	expectBlank(t, img, 150, 100)
	expectBoundary(t, rowTransition(img, 100, 0), 100, 4, "transformed clip")
}

// TestClipEmpty verifies that an empty clipPath hides the clipped element.
func TestClipEmpty(t *testing.T) {
	icon := readClipIcon(t, `<svg viewBox="0 0 100 100">
		<defs><clipPath id="empty"></clipPath></defs>
		<rect x="0" y="0" width="100" height="100" fill="rgb(255,0,0)" clip-path="url(#empty)"/>
	</svg>`)
	img := drawClipIcon(t, icon, 100, 100)
	expectBlank(t, img, 50, 50)
	expectBlank(t, img, 10, 10)
}

// TestClipMissingReference verifies that an unresolvable clip-path reference
// leaves the element unclipped.
func TestClipMissingReference(t *testing.T) {
	icon := readClipIcon(t, `<svg viewBox="0 0 100 100">
		<rect x="0" y="0" width="100" height="100" fill="rgb(255,0,0)" clip-path="url(#nope)"/>
	</svg>`)
	img := drawClipIcon(t, icon, 100, 100)
	expectDrawn(t, img, 50, 50)
	expectDrawn(t, img, 90, 90)
}

// TestClipSelfReference verifies a clipPath referencing itself does not hang
// and the cyclic reference is ignored.
func TestClipSelfReference(t *testing.T) {
	icon := readClipIcon(t, `<svg viewBox="0 0 100 100">
		<defs>
			<clipPath id="self">
				<rect x="0" y="0" width="50" height="100" clip-path="url(#self)"/>
			</clipPath>
		</defs>
		<rect x="0" y="0" width="100" height="100" fill="rgb(255,0,0)" clip-path="url(#self)"/>
	</svg>`)
	img := drawClipIcon(t, icon, 100, 100)
	expectDrawn(t, img, 25, 50)
	expectBlank(t, img, 75, 50)
}

// TestClipCircularReference verifies mutually referencing clipPaths resolve
// deterministically: the reference closing the cycle is ignored, so the
// result is the intersection of both rectangles.
func TestClipCircularReference(t *testing.T) {
	icon := readClipIcon(t, `<svg viewBox="0 0 100 100">
		<defs>
			<clipPath id="c1">
				<rect x="0" y="0" width="60" height="100" clip-path="url(#c2)"/>
			</clipPath>
			<clipPath id="c2">
				<rect x="0" y="0" width="100" height="60" clip-path="url(#c1)"/>
			</clipPath>
		</defs>
		<rect x="0" y="0" width="100" height="100" fill="rgb(255,0,0)" clip-path="url(#c1)"/>
	</svg>`)
	img := drawClipIcon(t, icon, 100, 100)
	expectDrawn(t, img, 30, 30)
	expectBlank(t, img, 80, 30)
	expectBlank(t, img, 30, 80)
}

// TestClipNested verifies a clip-path on an element inside a clipPath
// intersects with the containing clipPath.
func TestClipNested(t *testing.T) {
	icon := readClipIcon(t, `<svg viewBox="0 0 100 100">
		<defs>
			<clipPath id="inner">
				<rect x="0" y="0" width="60" height="100"/>
			</clipPath>
			<clipPath id="outer">
				<rect x="0" y="0" width="100" height="60" clip-path="url(#inner)"/>
			</clipPath>
		</defs>
		<rect x="0" y="0" width="100" height="100" fill="rgb(255,0,0)" clip-path="url(#outer)"/>
	</svg>`)
	img := drawClipIcon(t, icon, 100, 100)
	expectDrawn(t, img, 30, 30)
	expectBlank(t, img, 80, 30)
	expectBlank(t, img, 30, 80)
}

// TestClipUnsupportedNode verifies unsupported nodes inside a clipPath are
// skipped: they contribute nothing to the clipping region.
func TestClipUnsupportedNode(t *testing.T) {
	icon := readClipIcon(t, `<svg viewBox="0 0 100 100">
		<defs>
			<clipPath id="mixed">
				<text x="10" y="10">hi</text>
				<rect x="0" y="0" width="50" height="100"/>
			</clipPath>
			<clipPath id="textOnly">
				<text x="10" y="10">hi</text>
			</clipPath>
		</defs>
		<rect x="0" y="0" width="100" height="50" fill="rgb(255,0,0)" clip-path="url(#mixed)"/>
		<rect x="0" y="50" width="100" height="50" fill="rgb(0,200,0)" clip-path="url(#textOnly)"/>
	</svg>`)
	img := drawClipIcon(t, icon, 100, 100)
	// The rect still clips, the text contributes nothing.
	expectDrawn(t, img, 25, 25)
	expectBlank(t, img, 75, 25)
	// A clipPath with only unsupported content clips everything.
	expectBlank(t, img, 25, 75)
	expectBlank(t, img, 75, 75)
}

// TestClipGroup verifies a clip-path on a group clips every child.
func TestClipGroup(t *testing.T) {
	icon := readClipIcon(t, `<svg viewBox="0 0 100 100">
		<defs>
			<clipPath id="half"><rect x="0" y="0" width="50" height="100"/></clipPath>
		</defs>
		<g clip-path="url(#half)">
			<rect x="0" y="10" width="100" height="30" fill="rgb(255,0,0)"/>
			<circle cx="50" cy="70" r="20" fill="rgb(255,0,0)"/>
		</g>
	</svg>`)
	img := drawClipIcon(t, icon, 100, 100)
	expectDrawn(t, img, 25, 25)
	expectBlank(t, img, 75, 25)
	expectDrawn(t, img, 40, 70)
	expectBlank(t, img, 60, 70)
}

// TestClipPathOutsideDefs verifies a clipPath defined outside a defs section
// is collected as a definition and never drawn directly.
func TestClipPathOutsideDefs(t *testing.T) {
	icon := readClipIcon(t, `<svg viewBox="0 0 100 100">
		<clipPath id="o"><rect x="0" y="0" width="50" height="100"/></clipPath>
		<rect x="0" y="40" width="100" height="20" fill="rgb(255,0,0)" clip-path="url(#o)"/>
	</svg>`)
	img := drawClipIcon(t, icon, 100, 100)
	expectDrawn(t, img, 25, 50)
	expectBlank(t, img, 75, 50)
	// The clipPath's own rectangle must not be painted.
	expectBlank(t, img, 25, 10)
}

// TestClipForwardReference verifies a clip-path may reference a clipPath
// defined later in the document.
func TestClipForwardReference(t *testing.T) {
	icon := readClipIcon(t, `<svg viewBox="0 0 100 100">
		<rect x="0" y="0" width="100" height="100" fill="rgb(255,0,0)" clip-path="url(#late)"/>
		<defs>
			<clipPath id="late"><rect x="0" y="0" width="50" height="100"/></clipPath>
		</defs>
	</svg>`)
	img := drawClipIcon(t, icon, 100, 100)
	expectDrawn(t, img, 25, 50)
	expectBlank(t, img, 75, 50)
}

// TestClipPathChildWithID verifies a clipPath child carrying its own id
// stays part of the clipPath definition.
func TestClipPathChildWithID(t *testing.T) {
	icon := readClipIcon(t, `<svg viewBox="0 0 100 100">
		<defs>
			<clipPath id="c"><rect id="r" x="0" y="0" width="50" height="100"/></clipPath>
		</defs>
		<rect x="0" y="0" width="100" height="100" fill="rgb(255,0,0)" clip-path="url(#c)"/>
	</svg>`)
	img := drawClipIcon(t, icon, 100, 100)
	expectDrawn(t, img, 25, 50)
	expectBlank(t, img, 75, 50)
}

// TestClipPathContentTransform verifies transforms inside the clipPath
// content are honored.
func TestClipPathContentTransform(t *testing.T) {
	icon := readClipIcon(t, `<svg viewBox="0 0 100 100">
		<defs>
			<clipPath id="ct">
				<rect x="0" y="0" width="50" height="100" transform="translate(50 0)"/>
			</clipPath>
		</defs>
		<rect x="0" y="0" width="100" height="100" fill="rgb(255,0,0)" clip-path="url(#ct)"/>
	</svg>`)
	img := drawClipIcon(t, icon, 100, 100)
	expectBlank(t, img, 25, 50)
	expectDrawn(t, img, 75, 50)
}
