// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley
//
// utils.go implements translation of an SVG2.0 path into a rasterx Path.

package oksvg

import (
	"image/color"

	"github.com/srwiley/rasterx"
	"golang.org/x/image/math/fixed"
)

// SvgPath binds a style to a path.
type SvgPath struct {
	PathStyle
	Path rasterx.Path
}

// Draw the compiled SvgPath into the Dasher.
func (svgp *SvgPath) Draw(r *rasterx.Dasher, opacity float64) {
	svgp.DrawTransformed(r, opacity, rasterx.Identity)
}

// gradientColorFunc produces the per-pixel color function for a gradient
// paint. Pattern tiles pass a drawContext whose userMatrix maps content
// coordinates to tile pixels; ordinary drawing passes nil or an identity
// matrix, which preserves the original behavior.
func gradientColorFunc(g rasterx.Gradient, opacity float64, dc *drawContext) interface{} {
	if dc == nil || dc.userMatrix == (rasterx.Matrix2D{}) {
		return g.GetColorFunction(opacity)
	}
	// Inside a pattern tile the rasterizer's device space is tile pixels,
	// while the gradient was expressed in pattern content space. Its color
	// function evaluates at pixel coordinates, so route every unit mode
	// through GetColorFunctionUS with the content-to-pixel matrix. For
	// objectBoundingBox gradients Bounds have already been set to the
	// content's bbox, and the identity matrix inside that path leaves the
	// already-correct frame coordinates untouched until objMatrix maps them
	// into pixels.
	g2 := g
	if g2.Units == rasterx.ObjectBoundingBox {
		g2.Matrix = rasterx.Identity
	}
	return g2.GetColorFunctionUS(opacity, dc.userMatrix)
}

// DrawTransformed draws the compiled SvgPath into the Dasher while applying transform t.
func (svgp *SvgPath) DrawTransformed(r *rasterx.Dasher, opacity float64, t rasterx.Matrix2D) {
	svgp.drawTransformed(r, opacity, t, nil)
}

// drawTransformed is the internal implementation of DrawTransformed. The
// drawContext is non-nil while rendering an icon or a pattern tile and gives
// pattern fills access to the icon's definitions plus the cycle guard.
func (svgp *SvgPath) drawTransformed(r *rasterx.Dasher, opacity float64, t rasterx.Matrix2D, dc *drawContext) {
	m := svgp.mAdder.M
	svgp.mAdder.M = t.Mult(m)
	defer func() { svgp.mAdder.M = m }() // Restore untransformed matrix
	// Full user-to-device transform of this path; pattern tiles need it to
	// orient and position their contents in device pixels.
	deviceM := svgp.mAdder.M
	if svgp.fillerColor != nil {
		r.Clear()
		rf := &r.Filler
		rf.SetWinding(svgp.UseNonZeroWinding)
		svgp.mAdder.Adder = rf // This allows transformations to be applied
		svgp.Path.AddTo(&svgp.mAdder)

		switch fillerColor := svgp.fillerColor.(type) {
		case color.Color:
			rf.SetColor(rasterx.ApplyOpacity(fillerColor, svgp.FillOpacity*opacity))
		case rasterx.Gradient:
			if fillerColor.Units == rasterx.ObjectBoundingBox {
				fRect := rf.Scanner.GetPathExtent()
				mnx, mny := float64(fRect.Min.X)/64, float64(fRect.Min.Y)/64
				mxx, mxy := float64(fRect.Max.X)/64, float64(fRect.Max.Y)/64
				fillerColor.Bounds.X, fillerColor.Bounds.Y = mnx, mny
				fillerColor.Bounds.W, fillerColor.Bounds.H = mxx-mnx, mxy-mny
			}
			rf.SetColor(gradientColorFunc(fillerColor, svgp.FillOpacity*opacity, dc))
		case *SvgPattern:
			bbox := pathBoundsInUserSpace(svgp.Path, deviceM)
			cf := fillerColor.colorFunc(dc, svgp.FillOpacity*opacity, deviceM, bbox)
			if cf == nil {
				// Invalid, cyclic or otherwise unpaintable pattern: leave the
				// shape unfilled rather than emitting a default color.
				rf.SetWinding(true)
				return
			}
			rf.SetColor(cf)
		}
		rf.Draw()
		// default is true
		rf.SetWinding(true)
	}
	if svgp.linerColor != nil {
		r.Clear()
		svgp.mAdder.Adder = r
		lineGap := svgp.LineGap
		if lineGap == nil {
			lineGap = DefaultStyle.LineGap
		}
		lineCap := svgp.LineCap
		if lineCap == nil {
			lineCap = DefaultStyle.LineCap
		}
		leadLineCap := lineCap
		if svgp.LeadLineCap != nil {
			leadLineCap = svgp.LeadLineCap
		}
		r.SetStroke(fixed.Int26_6(svgp.LineWidth*64),
			fixed.Int26_6(svgp.MiterLimit*64), leadLineCap, lineCap,
			lineGap, svgp.LineJoin, svgp.Dash, svgp.DashOffset)
		svgp.Path.AddTo(&svgp.mAdder)
		switch linerColor := svgp.linerColor.(type) {
		case color.Color:
			r.SetColor(rasterx.ApplyOpacity(linerColor, svgp.LineOpacity*opacity))
		case rasterx.Gradient:
			if linerColor.Units == rasterx.ObjectBoundingBox {
				fRect := r.Scanner.GetPathExtent()
				mnx, mny := float64(fRect.Min.X)/64, float64(fRect.Min.Y)/64
				mxx, mxy := float64(fRect.Max.X)/64, float64(fRect.Max.Y)/64
				linerColor.Bounds.X, linerColor.Bounds.Y = mnx, mny
				linerColor.Bounds.W, linerColor.Bounds.H = mxx-mnx, mxy-mny
			}
			r.SetColor(gradientColorFunc(linerColor, svgp.LineOpacity*opacity, dc))
		case *SvgPattern:
			bbox := pathBoundsInUserSpace(svgp.Path, deviceM)
			cf := linerColor.colorFunc(dc, svgp.LineOpacity*opacity, deviceM, bbox)
			if cf == nil {
				return
			}
			r.SetColor(cf)
		}
		r.Draw()
	}
}

// GetFillColor returns the fill color of the SvgPath if one is defined and otherwise returns colornames.Black
func (svgp *SvgPath) GetFillColor() color.Color {
	return getColor(svgp.fillerColor)
}

// GetLineColor returns the stroke color of the SvgPath if one is defined and otherwise returns colornames.Black
func (svgp *SvgPath) GetLineColor() color.Color {
	return getColor(svgp.linerColor)
}

// SetFillColor sets the fill color of the SvgPath
func (svgp *SvgPath) SetFillColor(clr color.Color) {
	svgp.fillerColor = clr
}

// SetLineColor sets the line color of the SvgPath
func (svgp *SvgPath) SetLineColor(clr color.Color) {
	svgp.linerColor = clr
}
