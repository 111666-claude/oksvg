// Copyright 2022 The oksvg Authors. All rights reserved.
//
// Whitebox tests for symbol/use viewport handling, preserveAspectRatio math
// and the error handling for circular, too deep and malformed references.
package oksvg

import (
	"bytes"
	"testing"
)

// newTestCursor parses the given SVG in the requested ErrorMode and returns a
// cursor bound to the resulting icon, suitable for whitebox helpers such as
// parseSymbolMeta.
func newTestCursor(t *testing.T, svg string, mode ErrorMode) *IconCursor {
	t.Helper()
	icon, err := ReadIconStream(bytes.NewBufferString(svg), mode)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	c := &IconCursor{icon: icon}
	c.ErrorMode = mode
	return c
}

func TestParsePreserveAspectRatio(t *testing.T) {
	cases := []struct {
		in        string
		align     string
		meetSlice string
		wantErr   bool
	}{
		{"xMidYMid meet", alignXMidYMid, "meet", false},
		{"xMaxYMax slice", alignXMaxYMax, "slice", false},
		{"none", alignNone, "meet", false},               // meet/slice ignored for none
		{"defer xMinYMax", alignXMinYMax, "meet", false}, // defer is tolerated
		{"  xMinYMin   slice  ", alignXMinYMin, "slice", false},
		{"bogus meet", "", "", true},
		{"xMidYMid wobble", "", "", true},
		{"", "", "", true},
	}
	for _, tc := range cases {
		m := symbolMeta{align: alignXMidYMid, meetOrSlice: "meet"}
		err := parsePreserveAspectRatio(tc.in, &m)
		if tc.wantErr {
			if err == nil {
				t.Errorf("input %q: expected error, got none", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("input %q: unexpected error %v", tc.in, err)
			continue
		}
		if m.align != tc.align || m.meetOrSlice != tc.meetSlice {
			t.Errorf("input %q: got (%s,%s), want (%s,%s)",
				tc.in, m.align, m.meetOrSlice, tc.align, tc.meetSlice)
		}
	}
}

func TestSymbolViewportMatrixMeet(t *testing.T) {
	// viewBox 0 0 100 50 mapped into an 80x80 viewport with default meet.
	meta := symbolMeta{meetOrSlice: "meet", align: alignXMidYMid}
	meta.viewBox.X, meta.viewBox.Y, meta.viewBox.W, meta.viewBox.H = 0, 0, 100, 50
	meta.hasViewBox = true
	m := symbolViewportMatrix(meta, 80, 80)
	// meet scale is min(80/100, 80/50) = 0.8; content is 80 wide and 40 high,
	// centered vertically with a 20 unit band on each side.
	x0, y0 := m.Transform(0, 0)
	x1, y1 := m.Transform(100, 50)
	assertApprox(t, x0, 0, "meet x0")
	assertApprox(t, x1, 80, "meet x1")
	assertApprox(t, y0, 20, "meet y0")
	assertApprox(t, y1, 60, "meet y1")
}

func TestSymbolViewportMatrixSlice(t *testing.T) {
	meta := symbolMeta{meetOrSlice: "slice", align: alignXMidYMid}
	meta.viewBox.X, meta.viewBox.Y, meta.viewBox.W, meta.viewBox.H = 0, 0, 100, 50
	meta.hasViewBox = true
	m := symbolViewportMatrix(meta, 80, 80)
	// slice scale is max(0.8, 1.6) = 1.6. The 100-unit viewBox becomes 160
	// units wide; centered in the 80-wide viewport it starts at -40, so the
	// rect overflows 40 units on each side and is cropped by the viewport.
	x0, y0 := m.Transform(0, 0)
	x1, y1 := m.Transform(100, 50)
	assertApprox(t, x0, -40, "slice x0")
	assertApprox(t, x1, 120, "slice x1")
	assertApprox(t, y0, 0, "slice y0")
	assertApprox(t, y1, 80, "slice y1")
}

// TestSymbolViewportMatrixSliceCoverage checks which viewBox coordinates land
// on the viewport boundaries after the slice crop.
func TestSymbolViewportMatrixSliceCoverage(t *testing.T) {
	meta := symbolMeta{meetOrSlice: "slice", align: alignXMidYMid}
	meta.viewBox.W, meta.viewBox.H = 100, 50
	meta.hasViewBox = true
	m := symbolViewportMatrix(meta, 80, 80)
	// The viewport left edge (screen x=0) cuts the scaled content at viewBox
	// x=25, and its right edge at x=75: 1.6*25-40 = 0, 1.6*75-40 = 80.
	vx0, _ := m.Transform(25, 0)
	vx1, _ := m.Transform(75, 0)
	assertApprox(t, vx0, 0, "slice coverage left")
	assertApprox(t, vx1, 80, "slice coverage right")
}

func TestSymbolViewportMatrixNone(t *testing.T) {
	meta := symbolMeta{meetOrSlice: "meet", align: alignNone}
	meta.viewBox.X, meta.viewBox.Y, meta.viewBox.W, meta.viewBox.H = 10, 20, 100, 50
	meta.hasViewBox = true
	m := symbolViewportMatrix(meta, 50, 25)
	// Non-uniform scale sx=50/100=0.5, sy=25/50=0.5, applied after subtracting
	// the viewBox origin.
	x0, y0 := m.Transform(10, 20)
	x1, y1 := m.Transform(110, 70)
	assertApprox(t, x0, 0, "none x0")
	assertApprox(t, y0, 0, "none y0")
	assertApprox(t, x1, 50, "none x1")
	assertApprox(t, y1, 25, "none y1")
}

func TestSymbolViewportMatrixMeetAlign(t *testing.T) {
	// Same geometry as meet test but with explicit corner alignments.
	meta := symbolMeta{meetOrSlice: "meet", align: alignXMinYMin}
	meta.viewBox.W, meta.viewBox.H = 100, 50
	meta.hasViewBox = true
	m := symbolViewportMatrix(meta, 80, 80)
	_, y0 := m.Transform(0, 0)
	assertApprox(t, y0, 0, "xMinYMin y0")

	meta.align = alignXMaxYMax
	m = symbolViewportMatrix(meta, 80, 80)
	_, y0 = m.Transform(0, 0)
	assertApprox(t, y0, 40, "xMaxYMax y0") // 80 - 40 content height
}

func TestParseSymbolMeta(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">
		<defs><symbol id="s" viewBox="5 6 40 20" width="40" height="20"
			preserveAspectRatio="xMaxYMin slice">
			<rect width="10" height="10"/></symbol></defs></svg>`
	c := newTestCursor(t, svg, StrictErrorMode)
	defs := c.icon.Defs["s"]
	meta, isSymbol, err := parseSymbolMeta(c, defs)
	if err != nil || !isSymbol {
		t.Fatalf("expected symbol meta without error, got isSymbol=%v err=%v", isSymbol, err)
	}
	if !meta.hasViewBox || meta.viewBox.X != 5 || meta.viewBox.Y != 6 ||
		meta.viewBox.W != 40 || meta.viewBox.H != 20 {
		t.Errorf("unexpected viewBox: %+v", meta.viewBox)
	}
	if !meta.hasW || meta.w != 40 || !meta.hasH || meta.h != 20 {
		t.Errorf("unexpected default size: w=%v(%v) h=%v(%v)", meta.w, meta.hasW, meta.h, meta.hasH)
	}
	if meta.align != alignXMaxYMin || meta.meetOrSlice != "slice" {
		t.Errorf("unexpected PAR: %s %s", meta.align, meta.meetOrSlice)
	}

	// Non-symbol defs report isSymbol=false.
	shapeSVG := `<svg xmlns="http://www.w3.org/2000/svg"><defs>
		<g id="g"><rect width="1" height="1"/></g></defs></svg>`
	c2 := newTestCursor(t, shapeSVG, StrictErrorMode)
	if _, isG, err := parseSymbolMeta(c2, c2.icon.Defs["g"]); isG || err != nil {
		t.Errorf("group def must not be treated as symbol, got %v %v", isG, err)
	}
}

func TestSymbolBadViewBox(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><defs>
		<symbol id="s" viewBox="0 0 100 0"><rect width="10" height="10"/></symbol></defs>
		<use href="#s" x="0" y="0" width="50" height="50"/></svg>`
	if _, err := ReadIconStream(bytes.NewBufferString(svg), StrictErrorMode); err == nil {
		t.Error("strict mode must reject an illegal symbol viewBox")
	}
	// Warn and ignore modes survive the malformed symbol.
	if _, err := ReadIconStream(bytes.NewBufferString(svg), WarnErrorMode); err != nil {
		t.Errorf("warn mode must not fail: %v", err)
	}
	if _, err := ReadIconStream(bytes.NewBufferString(svg), IgnoreErrorMode); err != nil {
		t.Errorf("ignore mode must not fail: %v", err)
	}
}

func TestSymbolBadPreserveAspectRatio(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><defs>
		<symbol id="s" viewBox="0 0 50 50" preserveAspectRatio="bogus">
		<rect width="10" height="10"/></symbol></defs>
		<use href="#s" x="0" y="0" width="50" height="50"/></svg>`
	if _, err := ReadIconStream(bytes.NewBufferString(svg), StrictErrorMode); err == nil {
		t.Error("strict mode must reject an illegal preserveAspectRatio")
	}
	if _, err := ReadIconStream(bytes.NewBufferString(svg), IgnoreErrorMode); err != nil {
		t.Errorf("ignore mode must not fail: %v", err)
	}
}

func TestCircularUse(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><defs>
		<symbol id="a" viewBox="0 0 50 50"><use href="#b" width="50" height="50"/></symbol>
		<symbol id="b" viewBox="0 0 50 50"><use href="#a" width="50" height="50"/></symbol>
		</defs><use href="#a" x="0" y="0" width="100" height="100"/></svg>`
	if _, err := ReadIconStream(bytes.NewBufferString(svg), StrictErrorMode); err == nil {
		t.Error("strict mode must reject a circular use reference")
	}
	icon, err := ReadIconStream(bytes.NewBufferString(svg), IgnoreErrorMode)
	if err != nil {
		t.Errorf("ignore mode must break the cycle without error: %v", err)
	}
	// The expansion terminates instead of hanging or overflowing the stack.
	if icon == nil {
		t.Error("expected a partially parsed icon back")
	}
}

func TestDeepUseNesting(t *testing.T) {
	icon, err := ReadIcon("testdata/testIcons/symbolDeep.svg", IgnoreErrorMode)
	if err != nil {
		t.Fatalf("deep nesting must be cut gracefully in ignore mode: %v", err)
	}
	// The chain terminates at maxUseDepth instead of overflowing the stack, so
	// the icon is returned with the levels beyond the limit cut off.
	if icon == nil {
		t.Error("expected a partially parsed icon back")
	}
	if _, err := ReadIcon("testdata/testIcons/symbolDeep.svg", StrictErrorMode); err == nil {
		t.Error("strict mode must report the depth limit")
	}
}

func TestUnknownAndNonPositiveUse(t *testing.T) {
	// Unknown href: strict error, ignore survives.
	unknown := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">
		<use href="#missing" x="0" y="0" width="50" height="50"/></svg>`
	if _, err := ReadIconStream(bytes.NewBufferString(unknown), StrictErrorMode); err == nil {
		t.Error("strict mode must reject an unknown href")
	}
	if _, err := ReadIconStream(bytes.NewBufferString(unknown), IgnoreErrorMode); err != nil {
		t.Errorf("ignore mode must skip unknown href: %v", err)
	}

	// Non-positive viewport: the instance contributes no paths.
	nonPos := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><defs>
		<symbol id="s" viewBox="0 0 50 50"><rect width="50" height="50" fill="red"/></symbol></defs>
		<use href="#s" x="0" y="0" width="0" height="50"/>
		<use href="#s" x="0" y="0" width="50" height="-3"/></svg>`
	icon, err := ReadIconStream(bytes.NewBufferString(nonPos), StrictErrorMode)
	if err != nil {
		t.Errorf("non-positive viewport is skipped, not fatal: %v", err)
	}
	if len(icon.SVGPaths) != 0 {
		t.Errorf("non-positive viewports must not render, got %d paths", len(icon.SVGPaths))
	}

	// Missing width/height fall back to the symbol's own width/height attributes.
	fallback := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><defs>
		<symbol id="s" viewBox="0 0 50 50" width="50" height="50">
		<rect width="50" height="50" fill="red"/></symbol></defs>
		<use href="#s" x="10" y="10"/></svg>`
	icon, err = ReadIconStream(bytes.NewBufferString(fallback), StrictErrorMode)
	if err != nil {
		t.Fatalf("symbol-sized fallback must render: %v", err)
	}
	if len(icon.SVGPaths) != 1 {
		t.Fatalf("expected one rendered instance, got %d", len(icon.SVGPaths))
	}
	// The viewBox (50x50) mapped into the fallback 50x50 viewport at x/y 10
	// puts the rect corner at (10,10).
	x, y := icon.SVGPaths[0].mAdder.M.Transform(0, 0)
	assertApprox(t, x, 10, "fallback x")
	assertApprox(t, y, 10, "fallback y")
}

func TestSymbolWithoutViewBox(t *testing.T) {
	// A symbol without a viewBox does not establish a new viewport: width and
	// height are ignored and only the use x/y translation applies, exactly as
	// for a referenced plain group.
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><defs>
		<symbol id="s"><rect x="0" y="0" width="20" height="20" fill="red"/></symbol></defs>
		<use href="#s" x="30" y="40" width="999" height="999"/></svg>`
	icon, err := ReadIconStream(bytes.NewBufferString(svg), StrictErrorMode)
	if err != nil {
		t.Fatalf("symbol without viewBox must render: %v", err)
	}
	if len(icon.SVGPaths) != 1 {
		t.Fatalf("expected one instance, got %d", len(icon.SVGPaths))
	}
	x0, y0 := icon.SVGPaths[0].mAdder.M.Transform(0, 0)
	x1, y1 := icon.SVGPaths[0].mAdder.M.Transform(20, 20)
	assertApprox(t, x0, 30, "no-viewBox x0")
	assertApprox(t, y0, 40, "no-viewBox y0")
	assertApprox(t, x1, 50, "no-viewBox x1")
	assertApprox(t, y1, 60, "no-viewBox y1")
}

func TestNestedUseTransform(t *testing.T) {
	// An outer symbol references an inner one. The inner viewBox mapping must
	// compose inside the outer one, and the shared symbol may be reused at two
	// sizes without the instances sharing mutated matrix state.
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 200"><defs>
		<symbol id="inner" viewBox="0 0 50 50">
			<rect x="0" y="0" width="50" height="50" fill="red"/>
		</symbol>
		<symbol id="outer" viewBox="0 0 100 100">
			<use href="#inner" x="0" y="0" width="50" height="50"/>
			<use href="#inner" x="50" y="50" width="50" height="50"/>
		</symbol></defs>
		<use href="#outer" x="0" y="0" width="100" height="100"/>
		<use href="#outer" x="100" y="100" width="50" height="50"/>
	</svg>`
	icon, err := ReadIconStream(bytes.NewBufferString(svg), StrictErrorMode)
	if err != nil {
		t.Fatalf("nested use must expand: %v", err)
	}
	// Two outer instances, each with two inner rects.
	if len(icon.SVGPaths) != 4 {
		t.Fatalf("expected 4 rendered rects, got %d", len(icon.SVGPaths))
	}
	// First instance: the two inner 50x50 viewports map 1:1 inside a 100x100
	// outer viewport, so their rect corners land on (0,0) and (50,50).
	x0, y0 := icon.SVGPaths[0].mAdder.M.Transform(0, 0)
	assertApprox(t, x0, 0, "nested first x0")
	assertApprox(t, y0, 0, "nested first y0")
	x1, y1 := icon.SVGPaths[1].mAdder.M.Transform(0, 0)
	assertApprox(t, x1, 50, "nested second x0")
	assertApprox(t, y1, 50, "nested second y0")
	// Second outer instance is half size at (100,100): its first inner rect
	// starts at (100,100) and is scaled by 0.5.
	x2, y2 := icon.SVGPaths[2].mAdder.M.Transform(0, 0)
	assertApprox(t, x2, 100, "scaled instance x0")
	assertApprox(t, y2, 100, "scaled instance y0")
	ex, _ := icon.SVGPaths[2].mAdder.M.Transform(50, 0)
	assertApprox(t, ex, 125, "scaled instance width")
}

func assertApprox(t *testing.T, got, want float64, name string) {
	t.Helper()
	const eps = 1e-9
	d := got - want
	if d < -eps || d > eps {
		t.Errorf("%s: got %v, want %v", name, got, want)
	}
}
