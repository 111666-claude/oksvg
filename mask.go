// Copyright 2026 The oksvg Authors. All rights reserved.
//
// mask.go implements basic SVG alpha mask rendering. A mask defined in
// defs out of ordinary shapes and gradients is rendered into an offscreen
// buffer, and its alpha channel is applied to the element or group that
// references it. Only maskUnits/maskContentUnits of userSpaceOnUse are
// supported; anything else is reported and the content is drawn unmasked.

package oksvg

import (
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"log"
	"math"
	"strings"

	"github.com/srwiley/rasterx"
)

// maxMaskBufferPixels bounds the pixel count of the offscreen buffers used
// for mask rendering. Masks needing a larger buffer are skipped (the content
// is drawn unmasked) to avoid excessive memory allocation.
var maxMaskBufferPixels int64 = 1 << 24

// maskReference identifies the mask applied by a PathStyle. All SvgPaths
// produced by the same masked element share one maskReference, so they are
// composited together as a unit before the mask alpha is applied.
type maskReference struct {
	id    string           // id of the mask definition in SvgIcon.Defs
	group int              // unique id of the masked element
	m     rasterx.Matrix2D // user space transform in effect where the mask is referenced
}

// maskAttributes holds the parsed mask region in user space coordinates.
type maskAttributes struct {
	x, y, w, h float64
}

// parseMaskAttributes reads the mask element attributes. Only
// userSpaceOnUse mask units and alpha masks are supported.
func parseMaskAttributes(attrs []xml.Attr, vbW, vbH float64) (ma maskAttributes, err error) {
	// Defaults per SVG 1.1: x, y = -10% and width, height = 120% of the viewport.
	ma = maskAttributes{x: -0.1 * vbW, y: -0.1 * vbH, w: 1.2 * vbW, h: 1.2 * vbH}
	for _, attr := range attrs {
		switch attr.Name.Local {
		case "id":
			// already used to store the definition
		case "x":
			ma.x, err = parseMaskLength(attr.Value, vbW)
		case "y":
			ma.y, err = parseMaskLength(attr.Value, vbH)
		case "width":
			ma.w, err = parseMaskLength(attr.Value, vbW)
		case "height":
			ma.h, err = parseMaskLength(attr.Value, vbH)
		case "maskUnits":
			if strings.TrimSpace(attr.Value) != "userSpaceOnUse" {
				return ma, fmt.Errorf("maskUnits %q is not supported, only userSpaceOnUse", attr.Value)
			}
		case "maskContentUnits":
			if strings.TrimSpace(attr.Value) != "userSpaceOnUse" {
				return ma, fmt.Errorf("maskContentUnits %q is not supported, only userSpaceOnUse", attr.Value)
			}
		case "mask-type":
			if strings.TrimSpace(attr.Value) != "alpha" {
				return ma, fmt.Errorf("mask-type %q is not supported, only alpha", attr.Value)
			}
		}
		if err != nil {
			return ma, err
		}
	}
	return ma, nil
}

// parseMaskLength parses a mask coordinate or length; percentages are
// resolved against the given viewport dimension.
func parseMaskLength(v string, viewport float64) (float64, error) {
	v = strings.TrimSpace(v)
	if strings.HasSuffix(v, "%") {
		f, err := parseFloat(strings.TrimSuffix(v, "%"), 64)
		return f / 100 * viewport, err
	}
	return parseFloat(v, 64)
}

// transformedRectBounds returns the device space bounding rectangle of the
// mask region transformed by t.
func (ma maskAttributes) transformedRectBounds(t rasterx.Matrix2D) image.Rectangle {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, p := range [][2]float64{
		{ma.x, ma.y}, {ma.x + ma.w, ma.y},
		{ma.x, ma.y + ma.h}, {ma.x + ma.w, ma.y + ma.h},
	} {
		x, y := t.Transform(p[0], p[1])
		minX, minY = math.Min(minX, x), math.Min(minY, y)
		maxX, maxY = math.Max(maxX, x), math.Max(maxY, y)
	}
	return image.Rect(int(math.Floor(minX)), int(math.Floor(minY)),
		int(math.Ceil(maxX)), int(math.Ceil(maxY)))
}

// drawMasked renders a group of SvgPaths sharing one maskReference into an
// offscreen buffer, renders the referenced mask into a second buffer, and
// composites the content onto the destination through the mask's alpha.
// Any problem with the mask is logged and resolved to a defined result:
// unknown, cyclic or unsupported masks leave the content unmasked, while a
// non-positive mask region hides the content entirely.
func (s *SvgIcon) drawMasked(r *rasterx.Dasher, opacity float64, mr *maskReference, paths []SvgPath) {
	drawUnmasked := func() {
		for i := range paths {
			paths[i].DrawTransformed(r, opacity, s.Transform)
		}
	}
	def, ok := s.Defs[mr.id]
	if !ok || len(def) == 0 || def[0].Tag != "mask" {
		log.Printf("oksvg: mask #%s is not defined in defs; drawing unmasked", mr.id)
		drawUnmasked()
		return
	}
	ma, err := parseMaskAttributes(def[0].Attrs, s.ViewBox.W, s.ViewBox.H)
	if err != nil {
		log.Printf("oksvg: mask #%s: %v; drawing unmasked", mr.id, err)
		drawUnmasked()
		return
	}
	if ma.w <= 0 || ma.h <= 0 {
		log.Printf("oksvg: mask #%s has a non-positive region; masked content is not drawn", mr.id)
		return
	}
	gv, ok := r.Scanner.(*rasterx.ScannerGV)
	if !ok {
		log.Println("oksvg: masks require a rasterx.ScannerGV destination; drawing unmasked")
		drawUnmasked()
		return
	}
	dest := gv.Dest

	// Mask region in device (destination) coordinates.
	t := s.Transform.Mult(mr.m)
	rect := ma.transformedRectBounds(t).Intersect(dest.Bounds())
	if rect.Empty() {
		return // The mask region covers no destination pixel; nothing is visible.
	}
	if int64(rect.Dx())*int64(rect.Dy()) > maxMaskBufferPixels {
		log.Printf("oksvg: mask #%s region %v is too large; drawing unmasked", mr.id, rect)
		drawUnmasked()
		return
	}

	w, h := rect.Dx(), rect.Dy()
	off := rasterx.Identity.Translate(-float64(rect.Min.X), -float64(rect.Min.Y))

	// Render the masked content offscreen.
	content := image.NewRGBA(image.Rect(0, 0, w, h))
	contentDasher := rasterx.NewDasher(w, h, rasterx.NewScannerGV(w, h, content, content.Bounds()))
	contentT := off.Mult(s.Transform)
	for i := range paths {
		paths[i].DrawTransformed(contentDasher, opacity, contentT)
	}

	// Render the mask content offscreen; only its alpha channel is used.
	maskPaths, err := s.compileMaskPaths(def, off.Mult(t), mr.id)
	if err != nil {
		log.Printf("oksvg: mask #%s: %v; drawing unmasked", mr.id, err)
		drawUnmasked()
		return
	}
	maskImg := image.NewRGBA(image.Rect(0, 0, w, h))
	maskDasher := rasterx.NewDasher(w, h, rasterx.NewScannerGV(w, h, maskImg, maskImg.Bounds()))
	for i := range maskPaths {
		maskPaths[i].DrawTransformed(maskDasher, 1.0, rasterx.Identity)
	}

	draw.DrawMask(dest, rect, content, image.Point{}, maskImg, image.Point{}, draw.Over)
}

// compileMaskPaths converts the shapes stored in a mask definition into
// SvgPaths transformed by base. Mask references inside mask content
// (cyclic or nested) are reported as errors.
func (s *SvgIcon) compileMaskPaths(def []definition, base rasterx.Matrix2D, maskID string) ([]SvgPath, error) {
	iconCopy := *s // mask content must not append to the icon's own path list
	iconCopy.SVGPaths = nil
	c := &IconCursor{icon: &iconCopy, StyleStack: []PathStyle{DefaultStyle}}
	c.StyleStack[0].mAdder.M = base
	var paths []SvgPath
	for _, d := range def[1:] { // def[0] is the mask element itself
		if d.Tag == "endg" {
			c.StyleStack = c.StyleStack[:len(c.StyleStack)-1]
			continue
		}
		if d.Tag == "mask" {
			return nil, errors.New("nested mask elements are not supported")
		}
		if err := c.PushStyle(d.Attrs); err != nil {
			return nil, err
		}
		df, ok := drawFuncs[d.Tag]
		if !ok {
			return nil, fmt.Errorf("cannot process svg element %s in mask content", d.Tag)
		}
		if err := df(c, d.Attrs); err != nil {
			return nil, err
		}
		if len(c.Path) > 0 {
			// The cursor parsed a path from the definition
			pathCopy := make(rasterx.Path, len(c.Path))
			copy(pathCopy, c.Path)
			paths = append(paths, SvgPath{c.StyleStack[len(c.StyleStack)-1], pathCopy})
			c.Path = c.Path[:0]
		}
		// Collect any paths a "use" element appended to the scratch icon.
		paths = append(paths, iconCopy.SVGPaths...)
		iconCopy.SVGPaths = nil
		if d.Tag != "g" {
			// pop style
			c.StyleStack = c.StyleStack[:len(c.StyleStack)-1]
		}
	}
	for _, p := range paths {
		if p.maskRef != nil {
			if p.maskRef.id == maskID {
				return nil, fmt.Errorf("cyclic mask reference to #%s", maskID)
			}
			return nil, fmt.Errorf("mask content may not reference another mask (#%s)", p.maskRef.id)
		}
	}
	return paths, nil
}
