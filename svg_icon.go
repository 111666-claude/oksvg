// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley
//
// utils.go implements translation of an SVG2.0 path into a rasterx Path.

package oksvg

import (
	"github.com/srwiley/rasterx"
)

// SvgIcon holds data from parsed SVGs.
type SvgIcon struct {
	ViewBox      struct{ X, Y, W, H float64 }
	Titles       []string // Title elements collect here
	Descriptions []string // Description elements collect here
	Grads        map[string]*rasterx.Gradient
	Defs         map[string][]definition
	SVGPaths     []SvgPath
	Transform    rasterx.Matrix2D
	classes      map[string]styleAttribute
}

// Draw the compiled SVG icon into the GraphicContext.
// All elements should be contained by the Bounds rectangle of the SvgIcon.
func (s *SvgIcon) Draw(r *rasterx.Dasher, opacity float64) {
	for i := 0; i < len(s.SVGPaths); {
		mr := s.SVGPaths[i].maskRef
		if mr == nil {
			s.SVGPaths[i].DrawTransformed(r, opacity, s.Transform)
			i++
			continue
		}
		// All consecutive paths sharing one maskReference belong to the
		// same masked element and are composited as a unit.
		j := i + 1
		for j < len(s.SVGPaths) && s.SVGPaths[j].maskRef == mr {
			j++
		}
		s.drawMasked(r, opacity, mr, s.SVGPaths[i:j])
		i = j
	}
}

// SetTarget sets the Transform matrix to draw within the bounds of the rectangle arguments
func (s *SvgIcon) SetTarget(x, y, w, h float64) {
	scaleW := w / s.ViewBox.W
	scaleH := h / s.ViewBox.H
	s.Transform = rasterx.Identity.Translate(x-s.ViewBox.X, y-s.ViewBox.Y).Scale(scaleW, scaleH)
}
