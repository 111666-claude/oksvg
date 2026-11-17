// Copyright 2022 The oksvg Authors. All rights reserved.
//
// symbol.go implements rendering of SVG symbol elements referenced by use
// elements, including symbol viewBox mapping onto the use viewport with the
// preserveAspectRatio meet, slice and none scaling/alignment modes.

package oksvg

import (
	"encoding/xml"
	"errors"
	"log"
	"math"
	"strings"

	"github.com/srwiley/rasterx"
)

// preserveAspectRatio alignment modes, see the SVG specification.
const (
	alignNone     = "none"
	alignXMinYMin = "xMinYMin"
	alignXMidYMin = "xMidYMin"
	alignXMaxYMin = "xMaxYMin"
	alignXMinYMid = "xMinYMid"
	alignXMidYMid = "xMidYMid"
	alignXMaxYMid = "xMaxYMid"
	alignXMinYMax = "xMinYMax"
	alignXMidYMax = "xMidYMax"
	alignXMaxYMax = "xMaxYMax"
)

// symbolMeta holds the viewport related attributes of a symbol element.
type symbolMeta struct {
	viewBox     struct{ X, Y, W, H float64 }
	hasViewBox  bool
	w, h        float64 // symbol width/height attributes, used as use defaults
	hasW, hasH  bool
	align       string // preserveAspectRatio align value
	meetOrSlice string // "meet" (default) or "slice"; ignored when align == none
}

// Errors returned while expanding use elements. They are wrapped with context
// and handled according to the cursor ErrorMode by readStartElement.
var (
	errUseNoHref        = errors.New("only use tags with href is supported")
	errUseBadSelector   = errors.New("only the ID CSS selector is supported")
	errUseUnknownID     = errors.New("href ID in use statement was not found in saved defs")
	errUseCircular      = errors.New("circular use reference detected")
	errUseTooDeep       = errors.New("use element nesting is too deep")
	errUseNonPositive   = errors.New("use viewport width and height must be positive")
	errSymbolBadViewBox = errors.New("symbol viewBox is invalid")
)

// symbolF is invoked when a symbol definition is replayed by a use element.
// The symbol itself only establishes a style context; its geometry comes from
// its child definitions. The viewport transform is applied by useF before the
// replay, so symbolF does nothing but accept the element.
var symbolF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
	return nil
}

// useF expands a use element by replaying the referenced definitions. The use
// style (including its transform) is already on the style stack when useF is
// called. For symbol references the symbol viewBox is mapped onto the use
// x/y/width/height viewport honoring preserveAspectRatio.
var useF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
	var (
		href       string
		x, y       float64
		w, h       float64
		hasW, hasH bool
		err        error
	)
	for _, attr := range attrs {
		switch attr.Name.Local {
		case "href": // also matches xlink:href, whose local name is href
			href = attr.Value
		case "x":
			x, err = parseFloat(attr.Value, 64)
		case "y":
			y, err = parseFloat(attr.Value, 64)
		case "width":
			w, err = parseFloat(attr.Value, 64)
			hasW = true
		case "height":
			h, err = parseFloat(attr.Value, 64)
			hasH = true
		}
		if err != nil {
			return err
		}
	}
	// The use style already carries any transform attribute of the use
	// element. Capture it so the final frame matrix can be composed in the
	// correct SVG order: use transform, then the x/y translation, then the
	// symbol viewport mapping (which must be closest to the geometry).
	topStyle := &c.StyleStack[len(c.StyleStack)-1]
	base := topStyle.mAdder.M

	if href == "" {
		return errUseNoHref
	}
	if !strings.HasPrefix(href, "#") {
		return errUseBadSelector
	}
	id := href[1:]
	defs, ok := c.icon.Defs[id]
	if !ok {
		return errUseUnknownID
	}
	if c.useChain != nil && c.useChain[id] {
		return errUseCircular
	}
	if c.useDepth >= maxUseDepth {
		return errUseTooDeep
	}

	// Frame contributed by the use x/y attributes.
	frame := rasterx.Identity.Translate(x, y)

	// Only symbol elements establish a viewBox based viewport. For any other
	// referenced element (g, path, shape ...) width/height are ignored, as in
	// the SVG specification.
	meta, isSymbol, err := parseSymbolMeta(c, defs)
	if err != nil {
		return err
	}
	if isSymbol {
		if !hasW {
			w = meta.w
			hasW = meta.hasW
		}
		if !hasH {
			h = meta.h
			hasH = meta.hasH
		}
		if !hasW || !hasH || w <= 0 || h <= 0 || math.IsNaN(w) || math.IsNaN(h) ||
			math.IsInf(w, 0) || math.IsInf(h, 0) {
			// A non-rendering, non-positive viewport: skip the instance. This is
			// not a malformed document, so warn in WarnErrorMode and stay silent
			// in Ignore and Strict modes rather than aborting the whole parse.
			if c.ErrorMode == WarnErrorMode {
				log.Println(errUseNonPositive.Error())
			}
			return nil
		}
		if meta.hasViewBox {
			// The viewport matrix already folds the alignment offset and
			// viewBox origin into one mapping; the use x/y translation is
			// applied outside it. Mult applies its argument first.
			frame = frame.Mult(symbolViewportMatrix(meta, w, h))
		}
	}
	topStyle.mAdder.M = base.Mult(frame)

	// Track the reference chain for circular use detection and bound the
	// recursion depth, restoring the state no matter how replay ends.
	if c.useChain == nil {
		c.useChain = make(map[string]bool)
	}
	c.useChain[id] = true
	c.useDepth++
	defer func() {
		delete(c.useChain, id)
		c.useDepth--
	}()
	return c.replayDefs(defs)
}

// replayDefs replays the stored definitions of a referenced element. Each
// definition pushes its own style on top of the current one; container
// elements (g, symbol) balance their style with an end marker definition.
func (c *IconCursor) replayDefs(defs []definition) error {
	var err error
	for _, def := range defs {
		if def.Tag == "endg" || def.Tag == "endsymbol" {
			// pop container style
			c.StyleStack = c.StyleStack[:len(c.StyleStack)-1]
			continue
		}
		if err = c.PushStyle(def.Attrs); err != nil {
			return err
		}
		df, ok := drawFuncs[def.Tag]
		if !ok {
			// Pop the style pushed above so an unsupported element cannot leak
			// its attributes into the following siblings.
			c.StyleStack = c.StyleStack[:len(c.StyleStack)-1]
			errStr := "Cannot process svg element " + def.Tag
			if c.returnError(errStr) {
				return errors.New(errStr)
			}
			continue
		}
		if err = df(c, def.Attrs); err != nil {
			return err
		}
		// Did c.Path get added to during the drawFunction call?
		if len(c.Path) > 0 {
			// The cursor parsed a path from the xml element
			pathCopy := make(rasterx.Path, len(c.Path))
			copy(pathCopy, c.Path)
			c.icon.SVGPaths = append(c.icon.SVGPaths,
				SvgPath{c.StyleStack[len(c.StyleStack)-1], pathCopy})
			c.Path = c.Path[:0]
		}
		if def.Tag != "g" && def.Tag != "symbol" {
			// pop non-container style
			c.StyleStack = c.StyleStack[:len(c.StyleStack)-1]
		}
	}
	return nil
}

// parseSymbolMeta extracts the symbol element's viewport attributes from its
// stored definitions. The first definition of a referenced ID is the element
// itself. A malformed viewBox or preserveAspectRatio is reported through the
// cursor ErrorMode and returned as an error in strict mode.
func parseSymbolMeta(c *IconCursor, defs []definition) (meta symbolMeta, isSymbol bool, err error) {
	if len(defs) == 0 || defs[0].Tag != "symbol" {
		return meta, false, nil
	}
	meta.align = alignXMidYMid
	meta.meetOrSlice = "meet"
	isSymbol = true
	bad := func(e error) (symbolMeta, bool, error) {
		if c.returnError(e.Error()) {
			return meta, isSymbol, e
		}
		return meta, isSymbol, nil
	}
	for _, attr := range defs[0].Attrs {
		switch attr.Name.Local {
		case "viewBox":
			if e := c.GetPoints(attr.Value); e != nil {
				return bad(errSymbolBadViewBox)
			}
			if len(c.points) != 4 {
				return bad(errSymbolBadViewBox)
			}
			meta.viewBox.X, meta.viewBox.Y = c.points[0], c.points[1]
			meta.viewBox.W, meta.viewBox.H = c.points[2], c.points[3]
			for _, p := range c.points {
				if math.IsNaN(p) || math.IsInf(p, 0) {
					return bad(errSymbolBadViewBox)
				}
			}
			if meta.viewBox.W <= 0 || meta.viewBox.H <= 0 {
				return bad(errSymbolBadViewBox)
			}
			meta.hasViewBox = true
		case "preserveAspectRatio":
			if e := parsePreserveAspectRatio(attr.Value, &meta); e != nil {
				return bad(e)
			}
		case "width":
			if f, e := parseFloat(attr.Value, 64); e == nil &&
				!math.IsNaN(f) && !math.IsInf(f, 0) {
				meta.w, meta.hasW = f, true
			}
		case "height":
			if f, e := parseFloat(attr.Value, 64); e == nil &&
				!math.IsNaN(f) && !math.IsInf(f, 0) {
				meta.h, meta.hasH = f, true
			}
		}
	}
	return meta, isSymbol, nil
}

// parsePreserveAspectRatio parses an SVG preserveAspectRatio attribute into
// the symbolMeta align and meetOrSlice fields.
func parsePreserveAspectRatio(v string, meta *symbolMeta) error {
	fields := strings.Fields(strings.TrimSpace(v))
	if len(fields) == 0 {
		return errors.New("empty preserveAspectRatio")
	}
	// "defer" is an optional modifier only meaningful for image elements and
	// is ignored by the SVG specification.
	i := 0
	if fields[i] == "defer" {
		i++
		if i >= len(fields) {
			return errors.New("incomplete preserveAspectRatio")
		}
	}
	align := fields[i]
	switch align {
	case alignNone,
		alignXMinYMin, alignXMidYMin, alignXMaxYMin,
		alignXMinYMid, alignXMidYMid, alignXMaxYMid,
		alignXMinYMax, alignXMidYMax, alignXMaxYMax:
	default:
		return errors.New("invalid preserveAspectRatio align: " + align)
	}
	meta.align = align
	if i+1 < len(fields) {
		switch fields[i+1] {
		case "meet", "slice":
			meta.meetOrSlice = fields[i+1]
		default:
			return errors.New("invalid preserveAspectRatio meet-or-slice: " + fields[i+1])
		}
	}
	return nil
}

// symbolViewportMatrix builds the matrix mapping the symbol viewBox
// coordinate system into the use viewport of size w x h, honoring the symbol
// preserveAspectRatio alignment and meet/slice/none scaling.
//
// The matrix implements p -> align + s*(p - vbOrigin) where the alignment
// offset align is expressed in viewport units and s is the (possibly
// non-uniform) scale, so a single matrix carries both without the offset
// being re-scaled by a later matrix multiplication.
func symbolViewportMatrix(meta symbolMeta, w, h float64) rasterx.Matrix2D {
	vb := meta.viewBox
	if meta.align == alignNone {
		// Non-uniform scale: the viewBox is stretched to fill the viewport,
		// leaving no room for an alignment offset.
		sx, sy := w/vb.W, h/vb.H
		return rasterx.Matrix2D{
			A: sx,
			D: sy,
			E: -vb.X * sx,
			F: -vb.Y * sy,
		}
	}
	scaleX := w / vb.W
	scaleY := h / vb.H
	s := scaleX
	if meta.meetOrSlice == "slice" {
		// Uniform scale that covers the whole viewport.
		if scaleY > s {
			s = scaleY
		}
	} else {
		// meet: uniform scale that fits entirely inside the viewport.
		if scaleY < s {
			s = scaleY
		}
	}
	// Remaining space (meet) or overflow (slice), distributed by alignment.
	dx := w - vb.W*s
	dy := h - vb.H*s
	switch {
	case strings.Contains(meta.align, "xMid"):
		dx /= 2
	case strings.Contains(meta.align, "xMax"):
		// keep dx as is
	default: // xMin
		dx = 0
	}
	switch {
	case strings.Contains(meta.align, "YMid"):
		dy /= 2
	case strings.Contains(meta.align, "YMax"):
		// keep dy as is
	default: // YMin
		dy = 0
	}
	return rasterx.Matrix2D{
		A: s,
		D: s,
		E: dx - vb.X*s,
		F: dy - vb.Y*s,
	}
}

func init() {
	// avoids cyclical static declaration
	// called on package initialization
	drawFuncs["use"] = useF
}
