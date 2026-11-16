// Copyright 2026 The oksvg Authors. All rights reserved.
//
// svg_marker.go implements basic SVG marker rendering: marker elements
// defined in defs can be referenced by the marker-start, marker-mid and
// marker-end properties of line, polyline, polygon and path elements.

package oksvg

import (
	"encoding/xml"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/srwiley/rasterx"
	"golang.org/x/image/math/fixed"
)

// MarkerUnits indicates how the marker viewport is sized.
type MarkerUnits uint8

const (
	// StrokeWidthUnits scales the marker viewport by the stroke width of the
	// referencing element. This is the SVG default.
	StrokeWidthUnits MarkerUnits = iota
	// UserSpaceOnUseUnits sizes the marker viewport in the user space of the
	// referencing element.
	UserSpaceOnUseUnits
)

// OrientMode indicates how a marker is rotated at its vertex.
type OrientMode uint8

const (
	// OrientAuto aligns the marker x axis with the direction of the path.
	OrientAuto OrientMode = iota
	// OrientAutoStartReverse is OrientAuto, but start markers are rotated 180 degrees.
	OrientAutoStartReverse
	// OrientAngle rotates the marker by the fixed angle in Marker.Orient (degrees).
	OrientAngle
)

// Marker holds a compiled marker definition: the parsed marker element
// attributes plus the icon paths that make up the marker's contents.
type Marker struct {
	RefX, RefY    float64
	Width, Height float64 // markerWidth/markerHeight, default 3
	Units         MarkerUnits
	OrientMode    OrientMode
	Orient        float64 // fixed rotation in degrees when OrientMode == OrientAngle
	ViewBox       struct{ X, Y, W, H float64 }
	HasViewBox    bool
	// Disabled is set when the marker is well formed but must not render,
	// e.g. zero markerWidth/markerHeight or a non-positive viewBox.
	Disabled bool
	// Paths are the compiled contents of the marker. Markers referenced by
	// these paths are intentionally not resolved, so marker content can
	// never recurse.
	Paths []SvgPath

	parNone, parSlice bool
	alignX, alignY    float64 // preserveAspectRatio alignment: 0 min, 0.5 mid, 1 max
}

// newMarker returns a Marker with the SVG default attribute values.
func newMarker() *Marker {
	mk := &Marker{Width: 3, Height: 3, Units: StrokeWidthUnits, OrientMode: OrientAuto}
	mk.alignX, mk.alignY = 0.5, 0.5 // xMidYMid meet
	return mk
}

// parseAttrs reads the marker element's attributes. Unsupported or invalid
// values produce an error; the caller decides (per ErrorMode) whether the
// marker is skipped or the whole icon fails to load.
func (mk *Marker) parseAttrs(attrs []xml.Attr) error {
	for _, attr := range attrs {
		switch attr.Name.Local {
		case "refX", "refY", "markerWidth", "markerHeight":
			v, err := parseFloat(strings.TrimSpace(attr.Value), 64)
			if err != nil {
				return fmt.Errorf("marker %s: %v", attr.Name.Local, err)
			}
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("marker %s: invalid value %q", attr.Name.Local, attr.Value)
			}
			switch attr.Name.Local {
			case "refX":
				mk.RefX = v
			case "refY":
				mk.RefY = v
			case "markerWidth":
				if v < 0 {
					return fmt.Errorf("markerWidth must not be negative: %v", v)
				}
				mk.Width = v
				if v == 0 { // zero width disables rendering per SVG spec
					mk.Disabled = true
				}
			case "markerHeight":
				if v < 0 {
					return fmt.Errorf("markerHeight must not be negative: %v", v)
				}
				mk.Height = v
				if v == 0 {
					mk.Disabled = true
				}
			}
		case "markerUnits":
			switch strings.TrimSpace(attr.Value) {
			case "strokeWidth":
				mk.Units = StrokeWidthUnits
			case "userSpaceOnUse":
				mk.Units = UserSpaceOnUseUnits
			default:
				return fmt.Errorf("unsupported markerUnits %q", attr.Value)
			}
		case "orient":
			if err := mk.parseOrient(attr.Value); err != nil {
				return err
			}
		case "viewBox":
			pc := &PathCursor{}
			if err := pc.GetPoints(attr.Value); err != nil {
				return fmt.Errorf("marker viewBox: %v", err)
			}
			if len(pc.points) != 4 {
				return fmt.Errorf("marker viewBox requires 4 numbers, got %d", len(pc.points))
			}
			mk.ViewBox.X, mk.ViewBox.Y = pc.points[0], pc.points[1]
			mk.ViewBox.W, mk.ViewBox.H = pc.points[2], pc.points[3]
			mk.HasViewBox = true
			if mk.ViewBox.W <= 0 || mk.ViewBox.H <= 0 {
				// A viewBox with non-positive dimensions disables rendering
				// of the element per SVG spec.
				mk.Disabled = true
			}
		case "preserveAspectRatio":
			if err := mk.parsePreserveAspectRatio(attr.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (mk *Marker) parseOrient(v string) error {
	v = strings.TrimSpace(v)
	switch v {
	case "auto":
		mk.OrientMode = OrientAuto
		return nil
	case "auto-start-reverse":
		mk.OrientMode = OrientAutoStartReverse
		return nil
	}
	if strings.HasSuffix(v, "rad") || strings.HasSuffix(v, "grad") {
		return fmt.Errorf("unsupported orient unit in %q", v)
	}
	f, err := parseFloat(strings.TrimSuffix(v, "deg"), 64)
	if err != nil {
		return fmt.Errorf("invalid orient value %q", v)
	}
	mk.Orient = f
	mk.OrientMode = OrientAngle
	return nil
}

func (mk *Marker) parsePreserveAspectRatio(v string) error {
	fields := strings.Fields(v)
	if len(fields) == 0 || len(fields) > 2 {
		return fmt.Errorf("invalid preserveAspectRatio %q", v)
	}
	if fields[0] == "none" {
		mk.parNone = true
	} else {
		aligns := map[string][2]float64{
			"xMinYMin": {0, 0}, "xMinYMid": {0, 0.5}, "xMinYMax": {0, 1},
			"xMidYMin": {0.5, 0}, "xMidYMid": {0.5, 0.5}, "xMidYMax": {0.5, 1},
			"xMaxYMin": {1, 0}, "xMaxYMid": {1, 0.5}, "xMaxYMax": {1, 1},
		}
		a, ok := aligns[fields[0]]
		if !ok {
			return fmt.Errorf("unsupported preserveAspectRatio alignment %q", fields[0])
		}
		mk.alignX, mk.alignY = a[0], a[1]
	}
	if len(fields) == 2 {
		switch fields[1] {
		case "meet":
		case "slice":
			mk.parSlice = true
		default:
			return fmt.Errorf("unsupported preserveAspectRatio value %q", fields[1])
		}
	}
	return nil
}

// viewBoxMatrix maps marker content (viewBox) coordinates into the marker
// viewport of Width x Height, honoring preserveAspectRatio.
func (mk *Marker) viewBoxMatrix() rasterx.Matrix2D {
	vb := mk.ViewBox
	sx, sy := mk.Width/vb.W, mk.Height/vb.H
	if mk.parNone {
		return rasterx.Identity.Scale(sx, sy).Translate(-vb.X, -vb.Y)
	}
	s := math.Min(sx, sy)
	if mk.parSlice {
		s = math.Max(sx, sy)
	}
	ex := (mk.Width - vb.W*s) * mk.alignX
	ey := (mk.Height - vb.H*s) * mk.alignY
	return rasterx.Identity.Translate(ex, ey).Scale(s, s).Translate(-vb.X, -vb.Y)
}

// markerKind identifies which marker position a vertex corresponds to.
type markerKind uint8

const (
	markerKindStart markerKind = iota
	markerKindMid
	markerKindEnd
)

// markerVertex is a path vertex with the direction of the path at that point.
type markerVertex struct {
	x, y, angle float64
	kind        markerKind
}

// pathSegment is a drawable path segment with tangent directions at both ends.
type pathSegment struct {
	x0, y0, x1, y1 float64
	inX, inY       float64 // tangent at the start point
	outX, outY     float64 // tangent at the end point
}

func fx(v fixed.Int26_6) float64 { return float64(v) / 64 }

// markerVerticesForPath walks a compiled path and returns the vertices at
// which markers are placed, each with a deterministic direction:
//   - zero-length segments are dropped and do not produce vertices; tangents
//     fall back through degenerate control points to the segment chord;
//   - a closed subpath's seam vertex yields both a start and an end marker,
//     including the implicit closing segment in the direction computation;
//   - mid vertices use the bisector of the incoming and outgoing tangents,
//     falling back to the incoming direction for exact reversals.
func markerVerticesForPath(p rasterx.Path) []markerVertex {
	var verts []markerVertex
	var segs []pathSegment
	var sx, sy, cx, cy float64
	inSubpath := false

	addSeg := func(s pathSegment) {
		if s.x0 == s.x1 && s.y0 == s.y1 && s.inX == 0 && s.inY == 0 && s.outX == 0 && s.outY == 0 {
			return // zero-length segment: no position or direction information
		}
		segs = append(segs, s)
	}
	flush := func() {
		// A closed subpath's seam needs no special casing here: the closing
		// segment was appended above, so the start and end vertices coincide
		// and both a start and an end marker are emitted at the seam.
		verts = append(verts, verticesForSubpath(segs)...)
		segs = segs[:0]
	}

	for i := 0; i < len(p); {
		switch rasterx.PathCommand(p[i]) {
		case rasterx.PathMoveTo:
			if inSubpath {
				flush()
			}
			cx, cy = fx(p[i+1]), fx(p[i+2])
			sx, sy = cx, cy
			inSubpath = true
			i += 3
		case rasterx.PathLineTo:
			nx, ny := fx(p[i+1]), fx(p[i+2])
			addSeg(pathSegment{x0: cx, y0: cy, x1: nx, y1: ny,
				inX: nx - cx, inY: ny - cy, outX: nx - cx, outY: ny - cy})
			cx, cy = nx, ny
			i += 3
		case rasterx.PathQuadTo:
			qx, qy := fx(p[i+1]), fx(p[i+2])
			nx, ny := fx(p[i+3]), fx(p[i+4])
			seg := pathSegment{x0: cx, y0: cy, x1: nx, y1: ny,
				inX: qx - cx, inY: qy - cy, outX: nx - qx, outY: ny - qy}
			if seg.inX == 0 && seg.inY == 0 {
				seg.inX, seg.inY = nx-cx, ny-cy
			}
			if seg.outX == 0 && seg.outY == 0 {
				seg.outX, seg.outY = nx-cx, ny-cy
			}
			addSeg(seg)
			cx, cy = nx, ny
			i += 5
		case rasterx.PathCubicTo:
			c1x, c1y := fx(p[i+1]), fx(p[i+2])
			c2x, c2y := fx(p[i+3]), fx(p[i+4])
			nx, ny := fx(p[i+5]), fx(p[i+6])
			seg := pathSegment{x0: cx, y0: cy, x1: nx, y1: ny,
				inX: c1x - cx, inY: c1y - cy, outX: nx - c2x, outY: ny - c2y}
			if seg.inX == 0 && seg.inY == 0 {
				seg.inX, seg.inY = c2x-cx, c2y-cy
			}
			if seg.inX == 0 && seg.inY == 0 {
				seg.inX, seg.inY = nx-cx, ny-cy
			}
			if seg.outX == 0 && seg.outY == 0 {
				seg.outX, seg.outY = nx-c1x, ny-c1y
			}
			if seg.outX == 0 && seg.outY == 0 {
				seg.outX, seg.outY = nx-cx, ny-cy
			}
			addSeg(seg)
			cx, cy = nx, ny
			i += 7
		case rasterx.PathClose:
			if cx != sx || cy != sy {
				addSeg(pathSegment{x0: cx, y0: cy, x1: sx, y1: sy,
					inX: sx - cx, inY: sy - cy, outX: sx - cx, outY: sy - cy})
			}
			flush()
			inSubpath = false
			cx, cy = sx, sy
			i++
		default:
			i++
		}
	}
	if inSubpath {
		flush()
	}
	return verts
}

// verticesForSubpath converts the segments of one subpath into marker vertices.
func verticesForSubpath(segs []pathSegment) []markerVertex {
	if len(segs) == 0 {
		return nil
	}
	n := len(segs)
	verts := make([]markerVertex, 0, n+1)
	first, last := segs[0], segs[n-1]
	verts = append(verts, markerVertex{
		x: first.x0, y: first.y0, angle: math.Atan2(first.inY, first.inX), kind: markerKindStart})
	for i := 0; i < n-1; i++ {
		verts = append(verts, markerVertex{
			x: segs[i].x1, y: segs[i].y1,
			angle: bisectorAngle(segs[i].outX, segs[i].outY, segs[i+1].inX, segs[i+1].inY),
			kind:  markerKindMid})
	}
	verts = append(verts, markerVertex{
		x: last.x1, y: last.y1, angle: math.Atan2(last.outY, last.outX), kind: markerKindEnd})
	return verts
}

// bisectorAngle returns the direction halfway between two tangent vectors.
// For exactly opposing vectors the incoming direction is used, so the result
// is always deterministic.
func bisectorAngle(ax, ay, bx, by float64) float64 {
	la, lb := math.Hypot(ax, ay), math.Hypot(bx, by)
	if la == 0 {
		return math.Atan2(by, bx)
	}
	if lb == 0 {
		return math.Atan2(ay, ax)
	}
	sx, sy := ax/la+bx/lb, ay/la+by/lb
	if math.Hypot(sx, sy) < 1e-9 {
		return math.Atan2(ay, ax)
	}
	return math.Atan2(sy, sx)
}

// drawMarkers renders the start, mid and end markers of the path. It must be
// called from DrawTransformed while svgp.mAdder.M holds the caller's combined
// transform, so markers follow the same transform and opacity as the path.
func (svgp *SvgPath) drawMarkers(r *rasterx.Dasher, opacity float64) {
	st := &svgp.PathStyle
	if st.markerStart == nil && st.markerMid == nil && st.markerEnd == nil {
		return
	}
	base := svgp.mAdder.M
	// The element's opacity applies to its markers as a whole; LineOpacity
	// carries the element opacity for stroked elements.
	opacity *= st.LineOpacity
	for _, v := range markerVerticesForPath(svgp.Path) {
		var mk *Marker
		switch v.kind {
		case markerKindStart:
			mk = st.markerStart
		case markerKindMid:
			mk = st.markerMid
		case markerKindEnd:
			mk = st.markerEnd
		}
		if mk == nil || mk.Disabled {
			continue
		}
		mk.drawAt(r, opacity, base, v, st.LineWidth)
	}
}

// drawAt draws the marker contents at vertex v. base is the combined
// transform of the referencing path; lineWidth is its stroke width.
func (mk *Marker) drawAt(r *rasterx.Dasher, opacity float64, base rasterx.Matrix2D,
	v markerVertex, lineWidth float64) {
	var angle float64
	switch mk.OrientMode {
	case OrientAuto:
		angle = v.angle
	case OrientAutoStartReverse:
		angle = v.angle
		if v.kind == markerKindStart {
			angle += math.Pi
		}
	default: // OrientAngle
		angle = mk.Orient * math.Pi / 180
	}
	m := rasterx.Identity.Translate(v.x, v.y).Rotate(angle)
	if mk.Units == StrokeWidthUnits {
		m = m.Scale(lineWidth, lineWidth)
	}
	if mk.HasViewBox {
		m = m.Mult(mk.viewBoxMatrix())
	}
	m = m.Translate(-mk.RefX, -mk.RefY)
	t := base.Mult(m)
	for i := range mk.Paths {
		mk.Paths[i].DrawTransformed(r, opacity, t)
	}
}

// parseMarkerRef parses a marker property value: "none" or url(#id).
// It returns the referenced id, or "" for none.
func parseMarkerRef(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" || v == "none" {
		return "", nil
	}
	if strings.HasPrefix(v, "url(") && strings.HasSuffix(v, ")") {
		u := strings.Trim(strings.TrimSpace(v[4:len(v)-1]), "'\"")
		if strings.HasPrefix(u, "#") && len(u) > 1 {
			return u[1:], nil
		}
		return "", fmt.Errorf("unsupported marker reference %q", v)
	}
	return "", fmt.Errorf("invalid marker reference %q", v)
}

// resolveMarkers compiles the marker elements collected in the icon's defs
// and resolves the marker id references of every parsed path. Unknown ids
// and invalid marker definitions are reported according to the cursor's
// ErrorMode; in all modes the affected markers are simply not drawn.
func (c *IconCursor) resolveMarkers() error {
	icon := c.icon
	for id, defs := range icon.Defs {
		if len(defs) == 0 || defs[0].Tag != "marker" {
			continue
		}
		mk, err := icon.buildMarker(defs, c.ErrorMode)
		if err != nil {
			if c.returnError(fmt.Sprintf("error in marker %q: %s", id, err.Error())) {
				return err
			}
			continue
		}
		icon.Markers[id] = mk
	}
	for i := range icon.SVGPaths {
		st := &icon.SVGPaths[i].PathStyle
		for _, slot := range []struct {
			id  string
			dst **Marker
		}{
			{st.markerStartID, &st.markerStart},
			{st.markerMidID, &st.markerMid},
			{st.markerEndID, &st.markerEnd},
		} {
			if slot.id == "" {
				continue
			}
			mk, ok := icon.Markers[slot.id]
			if !ok {
				if c.returnError("marker reference #" + slot.id + " was not found in saved defs") {
					return errors.New("marker reference #" + slot.id + " was not found in saved defs")
				}
				continue
			}
			*slot.dst = mk
		}
	}
	return nil
}

// buildMarker compiles one marker definition: the marker element attributes
// in defs[0] and the marker contents in the remaining definitions.
func (icon *SvgIcon) buildMarker(defs []definition, errMode ErrorMode) (*Marker, error) {
	mk := newMarker()
	if err := mk.parseAttrs(defs[0].Attrs); err != nil {
		return nil, err
	}
	// Compile the marker contents with a fresh cursor on a scratch icon that
	// shares defs, gradients and classes so nested use/gradient/class
	// references resolve as they would at the top level.
	sub := &SvgIcon{
		ViewBox: icon.ViewBox,
		Defs:    icon.Defs,
		Grads:   icon.Grads,
		Markers: icon.Markers,
		classes: icon.classes,
	}
	cur := &IconCursor{StyleStack: []PathStyle{DefaultStyle}, icon: sub}
	cur.ErrorMode = errMode
	if err := cur.drawDefs(defs[1:]); err != nil {
		return nil, err
	}
	mk.Paths = sub.SVGPaths
	return mk, nil
}
