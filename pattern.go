// Copyright 2021 The oksvg Authors. All rights reserved.
//
// pattern.go implements basic SVG pattern fills: patterns built from the
// supported basic shapes are tiled in user space (or object bounding box
// space) and used as fill or stroke paints through fill="url(#id)".

package oksvg

import (
	"encoding/xml"
	"errors"
	"image"
	"image/color"
	"math"
	"strings"

	"github.com/srwiley/rasterx"
	"golang.org/x/image/math/fixed"
)

// PatternUnits mirrors the SVG patternUnits / patternContentUnits attributes.
type PatternUnits byte

const (
	// PatternObjectBoundingBox is the default value of patternUnits and
	// maps coordinates onto the filled shape's bounding box.
	PatternObjectBoundingBox PatternUnits = iota
	// PatternUserSpaceOnUse maps coordinates onto the user coordinate
	// system in place where the pattern is referenced.
	PatternUserSpaceOnUse
)

// maxPatternTilePixels bounds the memory a single pattern tile may occupy.
// Tiles that would be larger are rendered at a reduced resolution and scaled
// up when sampled, so a malicious or malformed SVG cannot trigger unbounded
// allocations.
const maxPatternTilePixels = 2048 * 2048

// maxPatternTileSide is the largest supported tile edge in pixels.
const maxPatternTileSide = 4096

// SvgPattern holds a pattern definition parsed from an SVG <pattern> element.
// The pattern content is the same SvgPath list used for regular shapes, so it
// supports every paint (solid color, gradient and nested pattern) the rest of
// the library understands.
type SvgPattern struct {
	// X, Y, Width, Height are the raw values of the homonymous attributes.
	X, Y, Width, Height float64
	// PatternUnits selects how X/Y/Width/Height are interpreted.
	PatternUnits PatternUnits
	// ContentUnits selects how the contents of the pattern are interpreted.
	ContentUnits PatternUnits
	// Transform holds the patternTransform attribute.
	Transform rasterx.Matrix2D
	// Paths are the shapes making up one tile, in pattern content space.
	Paths []SvgPath
	// unsupported is set for attributes this implementation cannot honor
	// (viewBox, preserveAspectRatio, href/ xlink:href). Such a pattern is
	// parsed without error but not painted, instead of being rendered with
	// silently wrong semantics.
	unsupported bool
}

// patternRef is stored in a PathStyle while parsing whenever a fill or stroke
// references a pattern. After the whole document has been read the references
// are resolved against SvgIcon.Patterns, which also handles forward
// references.
type patternRef struct{ id string }

// drawContext carries the state needed while an icon is rendered. It is passed
// down when pattern tiles render their own content so nested pattern fills can
// be detected and terminated instead of recursing forever.
type drawContext struct {
	icon    *SvgIcon
	raster  *rasterx.Dasher
	opacity float64
	// active holds the patterns currently being rendered, keyed by pointer,
	// and implements the cycle guard for pattern referencing itself.
	active map[*SvgPattern]bool
	// userMatrix maps the user space the paint was defined in onto the
	// rasterizer's device pixels. It is Identity for normal icon drawing;
	// pattern tiles set it so userSpaceOnUse gradients sample correctly.
	userMatrix rasterx.Matrix2D
}

// readPatternURL returns a patternRef when v is a "url(#id)" paint reference.
// Gradient references are handled separately by ReadGradURL.
func readPatternURL(v string) (patternRef, bool) {
	if strings.HasPrefix(v, "url(") && strings.HasSuffix(v, ")") {
		urlStr := strings.TrimSpace(v[4 : len(v)-1])
		if strings.HasPrefix(urlStr, "#") && len(urlStr) > 1 {
			return patternRef{id: urlStr[1:]}, true
		}
	}
	return patternRef{}, false
}

// patternF parses a <pattern> start element. The element itself only collects
// its attributes; child shapes are captured by readStartElement while
// inPattern is set and stored on the SvgPattern directly.
var patternF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
	p := &SvgPattern{
		Transform:    rasterx.Identity,
		PatternUnits: PatternObjectBoundingBox, // SVG default
		ContentUnits: PatternUserSpaceOnUse,    // SVG default
	}
	var id string
	var err error
	for _, attr := range attrs {
		switch attr.Name.Local {
		case "id":
			id = attr.Value
		case "x":
			p.X, err = parseFloat(attr.Value, 64)
		case "y":
			p.Y, err = parseFloat(attr.Value, 64)
		case "width":
			p.Width, err = parseFloat(attr.Value, 64)
		case "height":
			p.Height, err = parseFloat(attr.Value, 64)
		case "patternUnits":
			p.PatternUnits, err = parsePatternUnits(attr.Value)
		case "patternContentUnits":
			p.ContentUnits, err = parsePatternUnits(attr.Value)
		case "patternTransform":
			p.Transform, err = c.parseTransform(attr.Value)
		case "viewBox", "preserveAspectRatio", "href":
			// These pattern attributes change tiling semantics this
			// implementation does not model; disable the paint rather than
			// rendering it with the wrong coordinate system.
			p.unsupported = true
		default:
			// Other unknown attributes have no effect on the paint.
		}
		if err != nil {
			return err
		}
	}
	if id == "" {
		return errZeroLengthID
	}
	c.icon.Patterns[id] = p
	c.patternStack = append(c.patternStack, p)
	c.inPattern = true
	return nil
}

func parsePatternUnits(v string) (PatternUnits, error) {
	switch strings.TrimSpace(v) {
	case "", "objectBoundingBox":
		return PatternObjectBoundingBox, nil
	case "userSpaceOnUse":
		return PatternUserSpaceOnUse, nil
	default:
		return PatternObjectBoundingBox, errors.New("unknown pattern units: " + v)
	}
}

// finiteAndPositive reports whether d is a usable, strictly positive dimension.
func finiteAndPositive(d float64) bool {
	return !math.IsNaN(d) && !math.IsInf(d, 0) && d > 0
}

// colorFunc builds the per-pixel paint of the pattern fill. It renders one
// tile into an offscreen image in tile-frame pixels and samples it with
// wrapping. deviceToUser is the full transform (element transform combined
// with the icon-level target transform) mapping user coordinates to device
// pixels; bbox is the filled path's user-space bounding box, used for
// objectBoundingBox units. It returns nil when the pattern cannot be painted,
// in which case the caller leaves the shape unfilled.
func (p *SvgPattern) colorFunc(dc *drawContext, opacity float64,
	deviceToUser rasterx.Matrix2D, bbox struct{ X, Y, W, H float64 }) rasterx.ColorFunc {
	if p == nil || p.unsupported || len(p.Paths) == 0 {
		return nil
	}
	if !finiteAndPositive(p.Width) || !finiteAndPositive(p.Height) {
		// Non-positive, NaN or infinite tiles disable the paint.
		return nil
	}
	if dc == nil {
		// Standalone DrawTransformed calls have no icon context; supply a
		// minimal one so solid-content patterns still render.
		dc = &drawContext{opacity: 1.0, active: map[*SvgPattern]bool{}, userMatrix: rasterx.Identity}
	}
	if dc.active[p] {
		// Cyclic pattern reference: stop instead of recursing.
		return nil
	}

	// tileToUser maps a point in *tile-local* space (one tile spans
	// [0,1) x [0,1), origin at the pattern's x/y) into the user space of
	// the filled element. u = local*p.W + p.X.
	var tileToUser rasterx.Matrix2D
	switch p.PatternUnits {
	case PatternUserSpaceOnUse:
		tileToUser = rasterx.Matrix2D{
			A: p.Width, D: p.Height, E: p.X, F: p.Y,
		}
	default: // objectBoundingBox: u = (local*p.W + p.X)*bbox.W + bbox.X
		if !finiteAndPositive(bbox.W) || !finiteAndPositive(bbox.H) {
			return nil
		}
		tileToUser = rasterx.Matrix2D{
			A: p.Width * bbox.W, D: p.Height * bbox.H,
			E: p.X*bbox.W + bbox.X, F: p.Y*bbox.H + bbox.Y,
		}
	}

	// contentToLocal maps pattern content coordinates into tile-local space
	// with the tile origin removed, so content destined for any tile lands
	// inside [0,1) x [0,1) of a tile. Rendering relative to the origin is
	// what lets the offscreen rasterizer (which clips to its own bounds)
	// paint it. Matrices are built from literal components to avoid the
	// non-obvious ordering of the Translate/Scale chain helpers.
	var contentToLocal rasterx.Matrix2D
	switch p.ContentUnits {
	case PatternObjectBoundingBox:
		if !finiteAndPositive(bbox.W) || !finiteAndPositive(bbox.H) {
			return nil
		}
		// Both the content and the OBB tile are expressed as bbox fractions,
		// so a content coord maps into tile-local space by simply rescaling
		// by the tile's fractional size (no origin translation).
		contentToLocal = rasterx.Matrix2D{
			A: 1 / p.Width, D: 1 / p.Height,
		}
	default: // userSpaceOnUse: l = (c - p.X) / p.Width
		contentToLocal = rasterx.Matrix2D{
			A: 1 / p.Width, D: 1 / p.Height,
			E: -p.X / p.Width, F: -p.Y / p.Height,
		}
	}

	// Pixel edge vectors of one untransformed tile on the output device.
	edgeM := deviceToUser.Mult(tileToUser)
	if math.Abs(matrixDeterminant(edgeM)) < 1e-12 || !matrixFinite(edgeM) {
		return nil // singular transform: nothing meaningful to tile
	}
	vx, vy := edgeM.TransformVector(1, 0)
	hx, hy := edgeM.TransformVector(0, 1)
	tileWPx := math.Hypot(vx, vy)
	tileHPx := math.Hypot(hx, hy)
	if !finiteAndPositive(tileWPx) || !finiteAndPositive(tileHPx) {
		return nil
	}

	// Cap tile memory: render an oversized tile at a lower resolution; the
	// sampling step scales it back up.
	scale := 1.0
	if tileWPx > maxPatternTileSide || tileHPx > maxPatternTileSide ||
		tileWPx*tileHPx > maxPatternTilePixels {
		sx := maxPatternTileSide / tileWPx
		sy := maxPatternTileSide / tileHPx
		sa := math.Sqrt(maxPatternTilePixels / (tileWPx * tileHPx))
		scale = math.Min(math.Min(sx, sy), sa)
		if !finiteAndPositive(scale) {
			return nil
		}
	}
	tileW := int(math.Ceil(tileWPx * scale))
	tileH := int(math.Ceil(tileHPx * scale))
	if tileW < 1 || tileH < 1 {
		return nil
	}

	// Render the tile contents into a transparent, axis-aligned offscreen
	// image. Contents go through the regular SvgPath pipeline, so their
	// transforms, opacities and nested paints compose exactly as elsewhere.
	tileImg := image.NewRGBA(image.Rect(0, 0, tileW, tileH))
	scanner := rasterx.NewScannerGV(tileW, tileH, tileImg, tileImg.Bounds())
	raster := rasterx.NewDasher(tileW, tileH, scanner)

	// A tile-local point (lx,ly) maps to pixel (lx*tileW, ly*tileH).
	localToTilePixel := rasterx.Identity.Scale(float64(tileW), float64(tileH))
	contentToTilePixel := localToTilePixel.Mult(contentToLocal)

	childDC := &drawContext{
		icon:       dc.icon,
		raster:     raster,
		opacity:    1.0,
		active:     map[*SvgPattern]bool{p: true},
		userMatrix: contentToTilePixel,
	}
	for q := range dc.active {
		childDC.active[q] = true
	}
	for i := range p.Paths {
		p.Paths[i].drawTransformed(raster, 1.0, contentToTilePixel, childDC)
	}

	// Sampling: undo patternTransform and the tile placement to reach
	// tile-local coordinates of the device pixel, then wrap into one tile.
	localToDevice := deviceToUser.Mult(tileToUser.Mult(p.Transform))
	deviceToLocal := localToDevice.Invert()
	if math.Abs(matrixDeterminant(deviceToLocal)) < 1e-12 || !matrixFinite(deviceToLocal) {
		return nil
	}

	return rasterx.ColorFunc(func(x, y int) color.Color {
		// Sample at pixel centers, map to tile-local space, wrap into the
		// [0,1) x [0,1) tile and read the rendered tile image.
		lx, ly := deviceToLocal.Transform(float64(x)+0.5, float64(y)+0.5)
		tx := wrapInt(int(math.Floor(lx*float64(tileW))), tileW)
		ty := wrapInt(int(math.Floor(ly*float64(tileH))), tileH)
		i := tileImg.PixOffset(tx, ty)
		// The tile already carries the contents' own alpha; multiply by the
		// fill opacity of the painted shape.
		a := float64(tileImg.Pix[i+3]) * opacity
		return color.NRGBA{
			R: tileImg.Pix[i],
			G: tileImg.Pix[i+1],
			B: tileImg.Pix[i+2],
			A: clampAlpha(a),
		}
	})
}

// matrixDeterminant returns the determinant of m, used to detect singular
// matrices.
func matrixDeterminant(m rasterx.Matrix2D) float64 {
	return m.A*m.D - m.B*m.C
}

func matrixFinite(m rasterx.Matrix2D) bool {
	values := [...]float64{m.A, m.B, m.C, m.D, m.E, m.F}
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func wrapInt(v, n int) int {
	v %= n
	if v < 0 {
		v += n
	}
	return v
}

func clampAlpha(a float64) uint8 {
	if a <= 0 {
		return 0
	}
	if a >= 255 {
		return 255
	}
	return uint8(math.Round(a))
}

// pathBoundsInUserSpace computes an axis-aligned bounding box of the path in
// user space. rasterx.Path is a list of command functions, so the path is
// replayed onto a collecting adder; transforming every vertex through m and
// taking the extents gives a conservative box sufficient for
// objectBoundingBox pattern coordinates.
func pathBoundsInUserSpace(path rasterx.Path, m rasterx.Matrix2D) (bbox struct{ X, Y, W, H float64 }) {
	collector := &boundsCollector{m: m}
	path.AddTo(collector)
	if !collector.have {
		return bbox
	}
	bbox.X, bbox.Y = collector.minX, collector.minY
	bbox.W, bbox.H = collector.maxX-collector.minX, collector.maxY-collector.minY
	return bbox
}

// boundsCollector is an Adder that records the user-space extents of every
// vertex it receives. Bezier curves are flattened with rasterx's helpers so
// the sampled points cover the curve's bulge, not just its control points.
type boundsCollector struct {
	m                      rasterx.Matrix2D
	cur                    fixed.Point26_6
	minX, minY, maxX, maxY float64
	have                   bool
}

func (b *boundsCollector) add(pt fixed.Point26_6) {
	x, y := b.m.Transform(float64(pt.X)/64, float64(pt.Y)/64)
	if !b.have {
		b.minX, b.maxX = x, x
		b.minY, b.maxY = y, y
		b.have = true
		return
	}
	if x < b.minX {
		b.minX = x
	}
	if x > b.maxX {
		b.maxX = x
	}
	if y < b.minY {
		b.minY = y
	}
	if y > b.maxY {
		b.maxY = y
	}
}

func (b *boundsCollector) f26(x, y float32) fixed.Point26_6 {
	return fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(y * 64)}
}

func (b *boundsCollector) Start(a fixed.Point26_6) {
	b.cur = a
	b.add(a)
}

func (b *boundsCollector) Line(a fixed.Point26_6) {
	b.cur = a
	b.add(a)
}

func (b *boundsCollector) QuadBezier(c1, c2 fixed.Point26_6) {
	rasterx.QuadTo(
		float32(b.cur.X)/64, float32(b.cur.Y)/64,
		float32(c1.X)/64, float32(c1.Y)/64,
		float32(c2.X)/64, float32(c2.Y)/64,
		func(dx, dy float32) { b.Line(b.f26(dx, dy)) })
}

func (b *boundsCollector) CubeBezier(c1, c2, e fixed.Point26_6) {
	rasterx.CubeTo(
		float32(b.cur.X)/64, float32(b.cur.Y)/64,
		float32(c1.X)/64, float32(c1.Y)/64,
		float32(c2.X)/64, float32(c2.Y)/64,
		float32(e.X)/64, float32(e.Y)/64,
		func(dx, dy float32) { b.Line(b.f26(dx, dy)) })
}

func (b *boundsCollector) Stop(_ bool) {}
