// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley
//
// svg_icon.go defines the parsed SVG icon representation and its rendering.

package oksvg

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"

	"github.com/srwiley/rasterx"
)

// SvgIcon holds data from parsed SVGs.
type SvgIcon struct {
	ViewBox      struct{ X, Y, W, H float64 }
	Titles       []string // Title elements collect here
	Descriptions []string // Description elements collect here
	Grads        map[string]*rasterx.Gradient
	Defs         map[string][]definition
	ClipPaths    map[string]*ClipPath
	SVGPaths     []SvgPath
	Transform    rasterx.Matrix2D
	classes      map[string]styleAttribute
}

// ClipPath holds the shapes parsed from an SVG clipPath element.
// A ClipPath is never drawn directly; the union of its shapes defines
// the region through which a referencing element stays visible.
type ClipPath struct {
	Shapes []SvgPath
}

// Draw the compiled SVG icon into the GraphicContext.
// All elements should be contained by the Bounds rectangle of the SvgIcon.
func (s *SvgIcon) Draw(r *rasterx.Dasher, opacity float64) {
	for _, svgp := range s.SVGPaths {
		if len(svgp.clipPathIDs) > 0 {
			s.drawClipped(r, &svgp, opacity)
		} else {
			svgp.DrawTransformed(r, opacity, s.Transform)
		}
	}
}

// SetTarget sets the Transform matrix to draw within the bounds of the rectangle arguments
func (s *SvgIcon) SetTarget(x, y, w, h float64) {
	scaleW := w / s.ViewBox.W
	scaleH := h / s.ViewBox.H
	s.Transform = rasterx.Identity.Translate(x-s.ViewBox.X, y-s.ViewBox.Y).Scale(scaleW, scaleH)
}

// drawClipped draws a single SvgPath restricted to the intersection of the
// clip paths listed in its style. The path is rendered into a temporary
// image and composited onto the destination through the clip mask, so
// pixels outside the clip keep their original background.
func (s *SvgIcon) drawClipped(r *rasterx.Dasher, svgp *SvgPath, opacity float64) {
	sc, ok := r.Scanner.(*rasterx.ScannerGV)
	if !ok {
		log.Println("oksvg: clip-path is only supported with rasterx.ScannerGV; drawing without clip")
		svgp.DrawTransformed(r, opacity, s.Transform)
		return
	}
	bounds := sc.Dest.Bounds()
	// Clip shapes are in the user space of the clipped element, which
	// includes the element's own transform.
	elemMatrix := s.Transform.Mult(svgp.mAdder.M)
	mask, err := s.clipMask(svgp.clipPathIDs, elemMatrix, bounds, make(map[string]bool))
	if err != nil {
		log.Println("oksvg:", err, "-- clipped element not drawn")
		return
	}
	tmp := image.NewRGBA(bounds)
	scanner := rasterx.NewScannerGV(bounds.Dx(), bounds.Dy(), tmp, bounds)
	dasher := rasterx.NewDasher(bounds.Dx(), bounds.Dy(), scanner)
	svgp.DrawTransformed(dasher, opacity, s.Transform)
	draw.DrawMask(sc.Dest, bounds, tmp, bounds.Min, mask, bounds.Min, draw.Over)
}

// clipMask builds the alpha mask for a list of clip path ids referenced by a
// single element. Multiple ids (e.g. a clipped group inside a clipped group)
// are intersected. elemMatrix is the full transform of the clipped element.
func (s *SvgIcon) clipMask(ids []string, elemMatrix rasterx.Matrix2D,
	bounds image.Rectangle, visited map[string]bool) (*image.Alpha, error) {
	var mask *image.Alpha
	for _, id := range ids {
		m, err := s.singleClipMask(id, elemMatrix, bounds, visited)
		if err != nil {
			return nil, err
		}
		if mask == nil {
			mask = m
		} else {
			multiplyAlpha(mask, m)
		}
	}
	return mask, nil
}

// singleClipMask renders the union of one clipPath's shapes into an alpha mask.
func (s *SvgIcon) singleClipMask(id string, elemMatrix rasterx.Matrix2D,
	bounds image.Rectangle, visited map[string]bool) (*image.Alpha, error) {
	if visited[id] {
		return nil, fmt.Errorf("circular clipPath reference %q", id)
	}
	cp, ok := s.ClipPaths[id]
	if !ok {
		return nil, fmt.Errorf("clip-path reference %q not found", id)
	}
	visited[id] = true
	defer delete(visited, id)
	mask := image.NewAlpha(bounds)
	for _, shape := range cp.Shapes {
		shapeMatrix := elemMatrix.Mult(shape.mAdder.M)
		if len(shape.clipPathIDs) == 0 {
			fillClipShape(mask, shape.Path, shapeMatrix)
			continue
		}
		// A clip shape with its own clip-path intersects with it.
		sub, err := s.clipMask(shape.clipPathIDs, shapeMatrix, bounds, visited)
		if err != nil {
			return nil, err
		}
		tmp := image.NewAlpha(bounds)
		fillClipShape(tmp, shape.Path, shapeMatrix)
		multiplyAlpha(tmp, sub)
		unionAlpha(mask, tmp)
	}
	return mask, nil
}

// fillClipShape rasterizes path transformed by m into mask, unioning the
// shape's coverage with the mask's current content.
func fillClipShape(mask *image.Alpha, path rasterx.Path, m rasterx.Matrix2D) {
	b := mask.Bounds()
	scanner := rasterx.NewScannerGV(b.Dx(), b.Dy(), mask, b)
	filler := rasterx.NewFiller(b.Dx(), b.Dy(), scanner)
	filler.SetColor(color.NRGBA{0xff, 0xff, 0xff, 0xff})
	mAdder := rasterx.MatrixAdder{M: m, Adder: filler}
	path.AddTo(&mAdder)
	filler.Draw()
}

// multiplyAlpha intersects two masks: dst = dst * src.
// Both masks must have identical bounds.
func multiplyAlpha(dst, src *image.Alpha) {
	for i, b := range src.Pix {
		dst.Pix[i] = uint8(uint16(dst.Pix[i]) * uint16(b) / 0xff)
	}
}

// unionAlpha unions two masks: dst = dst + src - dst*src.
// Both masks must have identical bounds.
func unionAlpha(dst, src *image.Alpha) {
	for i, b := range src.Pix {
		a := dst.Pix[i]
		dst.Pix[i] = uint8(uint16(a) + uint16(b) - uint16(a)*uint16(b)/0xff)
	}
}
