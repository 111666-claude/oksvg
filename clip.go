// Copyright 2026 The oksvg Authors. All rights reserved.
//
// clip.go implements SVG clipPath support: parsing of clipPath definitions,
// resolution of clip-path url references, and mask based clipping at draw time.
//
// Deterministic behavior for edge cases:
//   - A clip-path reference that cannot be resolved (missing id, or the
//     referenced element is not a clipPath) is ignored: the element is drawn
//     unclipped.
//   - A clipPath with no drawable content clips everything: the element is
//     not drawn.
//   - Circular clip-path references are broken by ignoring the reference
//     that closes the cycle.
//   - Nested clip-path references (on elements inside a clipPath) intersect
//     with the containing clipPath.
//   - Unsupported nodes inside a clipPath (anything but path, rect, circle,
//     ellipse, line, polyline, polygon and g) contribute nothing to the
//     clipping region.
package oksvg

import (
	"image"
	"image/color"
	"image/draw"
	"log"
	"strings"

	"github.com/srwiley/rasterx"
)

// clipUnits is the coordinate system a clipPath's content is expressed in.
type clipUnits uint8

const (
	userSpaceOnUse clipUnits = iota
	objectBoundingBox
)

// clipShape is a single compiled shape of a clipPath definition.
type clipShape struct {
	path  rasterx.Path
	m     rasterx.Matrix2D // transform accumulated within the clipPath content
	clips []*clipPath      // nested clips referenced by the shape itself
}

// clipPath is a compiled clipPath definition.
type clipPath struct {
	units  clipUnits
	shapes []clipShape
}

// clipShapeTags are the element tags that may contribute to a clipping region.
var clipShapeTags = map[string]bool{
	"path": true, "rect": true, "circle": true, "ellipse": true,
	"line": true, "polyline": true, "polygon": true, "g": true,
}

// parseClipURL extracts the id from a clip-path value of the form url(#id).
func parseClipURL(v string) (id string, ok bool) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "url(") && strings.HasSuffix(v, ")") {
		id = strings.TrimSpace(v[4 : len(v)-1])
		id = strings.Trim(id, "\"'")
		if strings.HasPrefix(id, "#") && len(id) > 1 {
			return id[1:], true
		}
	}
	return "", false
}

// resolveClipPaths compiles the clip-path references collected in the styles
// of all parsed SVGPaths. Unresolvable references are dropped, which leaves
// the corresponding elements unclipped.
func (icon *SvgIcon) resolveClipPaths(errMode ErrorMode) {
	for i := range icon.SVGPaths {
		sp := &icon.SVGPaths[i]
		for _, ref := range sp.clipPathRefs {
			if cp := icon.buildClipPath(ref, make(map[string]bool), errMode); cp != nil {
				sp.clips = append(sp.clips, cp)
			}
		}
	}
}

// buildClipPath compiles the clipPath definition stored under id. It returns
// nil if the reference cannot be resolved or closes a reference cycle; both
// are treated as "no clip" by the caller. A clipPath that resolves but has no
// drawable content is returned with zero shapes, which clips everything.
func (icon *SvgIcon) buildClipPath(id string, visiting map[string]bool, errMode ErrorMode) *clipPath {
	if visiting[id] {
		return nil // circular reference: ignore the reference closing the cycle
	}
	def, ok := icon.Defs[id]
	if !ok || len(def) == 0 || def[0].Tag != "clipPath" {
		return nil // missing reference, or not a clipPath element
	}
	visiting[id] = true
	defer delete(visiting, id)

	cp := &clipPath{units: userSpaceOnUse}
	c := &IconCursor{icon: icon, StyleStack: []PathStyle{DefaultStyle}}
	c.ErrorMode = errMode
	for _, attr := range def[0].Attrs {
		if attr.Name.Local == "clipPathUnits" && strings.TrimSpace(attr.Value) == "objectBoundingBox" {
			cp.units = objectBoundingBox
		}
	}
	// Push the clipPath element's own attributes (transform, clip-path, ...)
	// so they are inherited by its children.
	if err := c.PushStyle(def[0].Attrs); err != nil && errMode == WarnErrorMode {
		log.Println("error parsing clipPath attributes:", err)
	}
	for _, d := range def[1:] {
		if d.Tag == "endg" {
			if len(c.StyleStack) > 1 {
				c.StyleStack = c.StyleStack[:len(c.StyleStack)-1]
			}
			continue
		}
		if !clipShapeTags[d.Tag] {
			// Unsupported node inside a clipPath: it contributes nothing
			// to the clipping region.
			if errMode == WarnErrorMode {
				log.Println("Ignoring unsupported clipPath element " + d.Tag)
			}
			continue
		}
		if err := c.PushStyle(d.Attrs); err != nil && errMode == WarnErrorMode {
			log.Println("error parsing clipPath child attributes:", err)
		}
		if err := drawFuncs[d.Tag](c, d.Attrs); err != nil && errMode == WarnErrorMode {
			log.Println("error compiling clipPath child "+d.Tag+":", err)
		}
		if len(c.Path) > 0 {
			pathCopy := make(rasterx.Path, len(c.Path))
			copy(pathCopy, c.Path)
			top := c.StyleStack[len(c.StyleStack)-1]
			sh := clipShape{path: pathCopy, m: top.mAdder.M}
			for _, ref := range top.clipPathRefs {
				if n := icon.buildClipPath(ref, visiting, errMode); n != nil {
					sh.clips = append(sh.clips, n)
				}
			}
			cp.shapes = append(cp.shapes, sh)
			c.Path = c.Path[:0]
		}
		if d.Tag != "g" {
			// pop style
			c.StyleStack = c.StyleStack[:len(c.StyleStack)-1]
		}
	}
	return cp
}

// pathExtent returns the bounding box of p in its local coordinates.
func pathExtent(p rasterx.Path) (x, y, w, h float64) {
	scanner := rasterx.NewScannerGV(1, 1, image.NewAlpha(image.Rect(0, 0, 1, 1)), image.Rect(0, 0, 1, 1))
	filler := rasterx.NewFiller(1, 1, scanner)
	p.AddTo(filler)
	ext := scanner.GetPathExtent()
	return float64(ext.Min.X) / 64, float64(ext.Min.Y) / 64,
		float64(ext.Max.X-ext.Min.X) / 64, float64(ext.Max.Y-ext.Min.Y) / 64
}

// renderShapeMask rasterizes a single path into a new alpha mask.
func renderShapeMask(bounds image.Rectangle, p rasterx.Path, m rasterx.Matrix2D) *image.Alpha {
	mask := image.NewAlpha(bounds)
	scanner := rasterx.NewScannerGV(bounds.Dx(), bounds.Dy(), mask, bounds)
	filler := rasterx.NewFiller(bounds.Dx(), bounds.Dy(), scanner)
	scanner.SetColor(color.Alpha{A: 0xff})
	ma := rasterx.MatrixAdder{M: m, Adder: filler}
	p.AddTo(&ma)
	filler.Draw()
	return mask
}

// renderMask rasterizes the whole clipPath into an alpha mask covering bounds.
// base maps the clip's user space to device pixels.
func (cp *clipPath) renderMask(bounds image.Rectangle, base rasterx.Matrix2D) *image.Alpha {
	mask := image.NewAlpha(bounds)
	for _, sh := range cp.shapes {
		m := base.Mult(sh.m)
		var sm *image.Alpha
		if len(sh.clips) == 0 {
			sm = renderShapeMask(bounds, sh.path, m)
		} else {
			sm = sh.renderClippedMask(bounds, m)
		}
		draw.Draw(mask, bounds, sm, image.Point{}, draw.Over)
	}
	return mask
}

// renderClippedMask renders a clip shape through the intersection of its own
// nested clips. base maps the shape's local user space to device pixels.
func (sh *clipShape) renderClippedMask(bounds image.Rectangle, base rasterx.Matrix2D) *image.Alpha {
	cur := renderShapeMask(bounds, sh.path, base)
	for _, nc := range sh.clips {
		if len(nc.shapes) == 0 {
			return image.NewAlpha(bounds) // empty nested clip hides the shape
		}
		m := base
		if nc.units == objectBoundingBox {
			x, y, w, h := pathExtent(sh.path)
			if w <= 0 || h <= 0 {
				return image.NewAlpha(bounds) // degenerate bounding box
			}
			m = base.Translate(x, y).Scale(w, h)
		}
		nm := nc.renderMask(bounds, m)
		next := image.NewAlpha(bounds)
		draw.DrawMask(next, bounds, cur, image.Point{}, nm, image.Point{}, draw.Src)
		cur = next
	}
	return cur
}

// drawClipped draws the SvgPath clipped by its resolved clip paths. It returns
// false if clipping cannot be performed with the underlying scanner, in which
// case the caller falls back to drawing the path unclipped.
func (svgp *SvgPath) drawClipped(r *rasterx.Dasher, opacity float64, t rasterx.Matrix2D) bool {
	for _, cp := range svgp.clips {
		if len(cp.shapes) == 0 {
			return true // an empty clipPath hides the element
		}
	}
	scanner, ok := r.Filler.Scanner.(*rasterx.ScannerGV)
	if !ok || scanner.Dest == nil {
		return false // the scanner exposes no destination image: draw unclipped
	}
	bounds := scanner.Dest.Bounds()
	// Render the path off screen.
	buf := image.NewNRGBA(bounds)
	bufDasher := rasterx.NewDasher(bounds.Dx(), bounds.Dy(),
		rasterx.NewScannerGV(bounds.Dx(), bounds.Dy(), buf, bounds))
	svgp.drawUnclipped(bufDasher, opacity, t)
	// Intersect the clip masks and apply them to the rendered path.
	base := t.Mult(svgp.mAdder.M)
	var src image.Image = buf
	for _, cp := range svgp.clips {
		m := base
		if cp.units == objectBoundingBox {
			x, y, w, h := pathExtent(svgp.Path)
			if w <= 0 || h <= 0 {
				return true // degenerate bounding box: nothing is drawn
			}
			m = base.Translate(x, y).Scale(w, h)
		}
		mask := cp.renderMask(bounds, m)
		next := image.NewNRGBA(bounds)
		draw.DrawMask(next, bounds, src, image.Point{}, mask, image.Point{}, draw.Src)
		src = next
	}
	draw.Draw(scanner.Dest, bounds, src, image.Point{}, draw.Over)
	return true
}
