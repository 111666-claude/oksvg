// Copyright 2024 The oksvg Authors. All rights reserved.
//
// filter.go implements a subset of SVG filter effects: the feDropShadow
// primitive, commonly used to separate foreground artwork from its
// background. A filter is supported when it contains exactly one
// feDropShadow primitive and nothing else; any other content marks the
// filter unsupported and it is ignored at draw time.

package oksvg

import (
	"encoding/xml"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"

	"github.com/srwiley/rasterx"
)

// MaxFilterOffscreenPixels bounds the size of the temporary image used to
// rasterize a filtered element or group. If the destination image has more
// pixels than this, the filter is skipped and the source is drawn without
// a shadow. It is a variable so applications can tune the limit.
var MaxFilterOffscreenPixels = 1 << 24

// maxBlurKernelRadius bounds the gaussian blur kernel radius in device
// pixels; larger standard deviations are clamped to keep the offscreen
// work and memory bounded.
const maxBlurKernelRadius = 512

// errNegativeStdDev reports a negative feDropShadow stdDeviation value.
var errNegativeStdDev = errors.New("feDropShadow stdDeviation must not be negative")

// DropShadow holds the parameters of a single feDropShadow primitive.
type DropShadow struct {
	Dx, Dy           float64     // shadow offset in user units
	StdDevX, StdDevY float64     // gaussian blur standard deviation in user units
	FloodColor       color.Color // shadow color, default black
	FloodOpacity     float64     // shadow opacity, default 1
}

// Filter holds a parsed SVG filter element. Only filters containing exactly
// one feDropShadow primitive are rendered; any other content sets
// Unsupported and the filter is ignored at draw time.
type Filter struct {
	ID string
	// Filter region. Values carry a Percent flag; plain numbers are
	// fractions of the bounding box for objectBoundingBox units and
	// user units for userSpaceOnUse.
	X, Y, W, H                             float64
	XPercent, YPercent, WPercent, HPercent bool
	UserSpaceOnUse                         bool // filterUnits="userSpaceOnUse"; default is objectBoundingBox
	DropShadow                             *DropShadow
	Unsupported                            bool // true if the filter content is not a supported drop shadow
}

// filterRef identifies one occurrence of a filter attribute in the parsed
// document. Paths parsed under the same attribute share the pointer, so
// consecutive paths with the same *filterRef form one filtered group whose
// composite alpha produces a single shadow.
type filterRef struct{ id string }

// parseFilterRef parses a filter style value. It returns nil for "none",
// empty, malformed or non-url values (CSS filter functions are unsupported),
// so those all explicitly mean "no filter".
func parseFilterRef(v string) *filterRef {
	v = strings.TrimSpace(v)
	if v == "" || v == "none" {
		return nil
	}
	if !strings.HasPrefix(v, "url(") || !strings.HasSuffix(v, ")") {
		return nil
	}
	id := strings.TrimSpace(v[4 : len(v)-1])
	id = strings.Trim(id, "\"'")
	id = strings.TrimPrefix(strings.TrimSpace(id), "#")
	if id == "" {
		return nil
	}
	return &filterRef{id: id}
}

// parseFilterLength parses a filter region length, reporting a % suffix.
func parseFilterLength(v string) (val float64, percent bool, err error) {
	v = strings.TrimSpace(v)
	if strings.HasSuffix(v, "%") {
		percent = true
		v = strings.TrimSuffix(v, "%")
	}
	val, err = parseFloat(v, 64)
	return
}

// readFilterElement handles the filter start element and everything nested
// inside it. Called from readStartElement before any other processing.
func (c *IconCursor) readFilterElement(se xml.StartElement) error {
	if se.Name.Local == "filter" {
		if c.inFilter {
			c.filter.Unsupported = true // nested filter elements are not valid
			return nil
		}
		// Spec defaults: x="-10%" y="-10%" width="120%" height="120%"
		f := &Filter{X: -10, Y: -10, W: 120, H: 120,
			XPercent: true, YPercent: true, WPercent: true, HPercent: true}
		for _, attr := range se.Attr {
			var err error
			switch attr.Name.Local {
			case "id":
				f.ID = attr.Value
			case "x":
				f.X, f.XPercent, err = parseFilterLength(attr.Value)
			case "y":
				f.Y, f.YPercent, err = parseFilterLength(attr.Value)
			case "width":
				f.W, f.WPercent, err = parseFilterLength(attr.Value)
			case "height":
				f.H, f.HPercent, err = parseFilterLength(attr.Value)
			case "filterUnits":
				f.UserSpaceOnUse = strings.TrimSpace(attr.Value) == "userSpaceOnUse"
			}
			if err != nil {
				return err
			}
		}
		c.filter = f
		c.inFilter = true
		return nil
	}
	if se.Name.Local == "feDropShadow" && !c.filter.Unsupported && c.filter.DropShadow == nil {
		ds, err := parseFeDropShadow(se.Attr)
		if err != nil {
			errStr := "unsupported feDropShadow: " + err.Error()
			if c.returnError(errStr) {
				return errors.New(errStr)
			}
			c.filter.Unsupported = true
			return nil
		}
		c.filter.DropShadow = ds
		return nil
	}
	// Anything else (feGaussianBlur, feMerge, extra primitives, ...) is an
	// unsupported complex filter chain; the filter is explicitly ignored.
	errStr := "unsupported filter content " + se.Name.Local
	if c.returnError(errStr) {
		return errors.New(errStr)
	}
	c.filter.Unsupported = true
	return nil
}

// endFilter stores the parsed filter in the icon when the filter end tag
// is reached.
func (c *IconCursor) endFilter() {
	if c.inFilter && c.filter != nil && c.filter.ID != "" {
		c.icon.Filters[c.filter.ID] = c.filter
	}
	c.inFilter = false
	c.filter = nil
}

// parseFeDropShadow parses the attributes of an feDropShadow element.
func parseFeDropShadow(attrs []xml.Attr) (*DropShadow, error) {
	ds := &DropShadow{FloodColor: color.NRGBA{A: 0xff}, FloodOpacity: 1}
	var stylePairs []string
	apply := func(k, v string) error {
		var err error
		switch k {
		case "dx":
			ds.Dx, err = parseFloat(v, 64)
		case "dy":
			ds.Dy, err = parseFloat(v, 64)
		case "stddeviation":
			err = parseStdDeviation(v, ds)
		case "flood-color":
			var clr color.Color
			clr, err = ParseSVGColor(v)
			if err == nil {
				if clr == nil { // "none": a fully transparent shadow
					clr = color.NRGBA{}
				}
				ds.FloodColor = clr
			}
		case "flood-opacity":
			ds.FloodOpacity, err = parseFloat(v, 64)
		}
		return err
	}
	for _, attr := range attrs {
		if strings.EqualFold(attr.Name.Local, "style") {
			stylePairs = append(stylePairs, strings.Split(attr.Value, ";")...)
			continue
		}
		if err := apply(strings.ToLower(attr.Name.Local), strings.TrimSpace(attr.Value)); err != nil {
			return nil, err
		}
	}
	// A style attribute overrides the presentation attributes.
	for _, pair := range stylePairs {
		kv := strings.SplitN(pair, ":", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(kv[0]))
		if k != "flood-color" && k != "flood-opacity" {
			continue
		}
		if err := apply(k, strings.TrimSpace(kv[1])); err != nil {
			return nil, err
		}
	}
	return ds, nil
}

// parseStdDeviation parses the one or two value stdDeviation attribute.
func parseStdDeviation(v string, ds *DropShadow) error {
	parts := splitOnCommaOrSpace(strings.TrimSpace(v))
	if len(parts) == 0 || len(parts) > 2 {
		return errParamMismatch
	}
	x, err := parseFloat(parts[0], 64)
	if err != nil {
		return err
	}
	y := x
	if len(parts) == 2 {
		y, err = parseFloat(parts[1], 64)
		if err != nil {
			return err
		}
	}
	if x < 0 || y < 0 {
		return errNegativeStdDev
	}
	ds.StdDevX, ds.StdDevY = x, y
	return nil
}

// resolveFilter returns the Filter a filterRef points to, or nil when the
// reference is unknown or the filter is not a supported drop shadow; both
// explicitly mean the paths are drawn without a filter effect.
func (s *SvgIcon) resolveFilter(fr *filterRef) *Filter {
	if fr == nil {
		return nil
	}
	f, ok := s.Filters[fr.id]
	if !ok || f == nil || f.Unsupported || f.DropShadow == nil {
		return nil
	}
	return f
}

// drawFiltered draws a group of consecutive paths sharing one filter
// reference: the group's composite alpha is blurred, tinted and offset to
// form a shadow below the source, and both are clipped to the filter region.
// If the destination cannot be composited on (not a *rasterx.ScannerGV with
// an image destination) or is too large, the source is drawn without a shadow.
func (s *SvgIcon) drawFiltered(r *rasterx.Dasher, paths []SvgPath, f *Filter, opacity float64) {
	drawPlain := func() {
		for i := range paths {
			paths[i].DrawTransformed(r, opacity, s.Transform)
		}
	}
	gv, ok := r.Scanner.(*rasterx.ScannerGV)
	if !ok || gv.Dest == nil {
		drawPlain()
		return
	}
	db := gv.Dest.Bounds()
	w, h := db.Dx(), db.Dy()
	if w <= 0 || h <= 0 || w*h > MaxFilterOffscreenPixels {
		drawPlain() // oversized offscreen would be required
		return
	}
	// Combined transform of the group, used to map the user space blur
	// radius, offset and filter region into device space.
	m := s.Transform.Mult(paths[0].mAdder.M)

	// Render the group into an offscreen image sharing the destination bounds.
	off := image.NewRGBA(db)
	offDasher := rasterx.NewDasher(w, h, rasterx.NewScannerGV(w, h, off, db))
	for i := range paths {
		paths[i].DrawTransformed(offDasher, opacity, s.Transform)
	}
	bbox, ok := opaqueBounds(off)
	if !ok {
		return // the group rendered nothing
	}
	region := s.filterRegion(f, bbox, m).Intersect(db)
	if region.Empty() {
		return // the filter region clips the whole filter result
	}

	// Device space blur radius and offset.
	ux, uy := m.TransformVector(1, 0)
	vx, vy := m.TransformVector(0, 1)
	stdX := f.DropShadow.StdDevX * math.Hypot(ux, uy)
	stdY := f.DropShadow.StdDevY * math.Hypot(vx, vy)
	ox, oy := m.TransformVector(f.DropShadow.Dx, f.DropShadow.Dy)
	iox, ioy := int(math.Round(ox)), int(math.Round(oy))

	fc := color.NRGBAModel.Convert(f.DropShadow.FloodColor).(color.NRGBA)
	fo := f.DropShadow.FloodOpacity
	if fo > 0 && fc.A > 0 {
		alpha := blurAlpha(off, stdX, stdY)
		shadow := image.NewRGBA(region)
		fa := float64(fc.A) / 255
		for y := region.Min.Y; y < region.Max.Y; y++ {
			ay := y - ioy - db.Min.Y
			if ay < 0 || ay >= h {
				continue
			}
			for x := region.Min.X; x < region.Max.X; x++ {
				ax := x - iox - db.Min.X
				if ax < 0 || ax >= w {
					continue
				}
				a := float64(alpha[ay*w+ax]) * fo * fa
				if a <= 0 {
					continue
				}
				if a > 1 {
					a = 1
				}
				i := shadow.PixOffset(x, y)
				shadow.Pix[i] = uint8(float64(fc.R)*a + 0.5)
				shadow.Pix[i+1] = uint8(float64(fc.G)*a + 0.5)
				shadow.Pix[i+2] = uint8(float64(fc.B)*a + 0.5)
				shadow.Pix[i+3] = uint8(a*255 + 0.5)
			}
		}
		draw.Draw(gv.Dest, region, shadow, region.Min, draw.Over)
	}

	// Draw the source over its shadow, clipped to the same filter region.
	gv.SetClip(region)
	defer gv.SetClip(image.ZR)
	for i := range paths {
		paths[i].DrawTransformed(r, opacity, s.Transform)
	}
}

// filterRegion computes the filter region in device coordinates.
func (s *SvgIcon) filterRegion(f *Filter, bbox image.Rectangle, m rasterx.Matrix2D) image.Rectangle {
	if f.UserSpaceOnUse {
		resolve := func(v float64, percent bool, base float64) float64 {
			if percent {
				return v / 100 * base
			}
			return v
		}
		x := resolve(f.X, f.XPercent, s.ViewBox.W)
		y := resolve(f.Y, f.YPercent, s.ViewBox.H)
		w := resolve(f.W, f.WPercent, s.ViewBox.W)
		h := resolve(f.H, f.HPercent, s.ViewBox.H)
		x0, y0 := m.Transform(x, y)
		x1, y1 := m.Transform(x+w, y+h)
		return floatRect(x0, y0, x1, y1)
	}
	// objectBoundingBox: values are fractions of the rendered bounding box.
	frac := func(v float64, percent bool) float64 {
		if percent {
			return v / 100
		}
		return v
	}
	bw, bh := float64(bbox.Dx()), float64(bbox.Dy())
	x0 := float64(bbox.Min.X) + frac(f.X, f.XPercent)*bw
	y0 := float64(bbox.Min.Y) + frac(f.Y, f.YPercent)*bh
	x1 := x0 + frac(f.W, f.WPercent)*bw
	y1 := y0 + frac(f.H, f.HPercent)*bh
	return floatRect(x0, y0, x1, y1)
}

// floatRect converts float corners to an outward rounded integer rectangle.
func floatRect(x0, y0, x1, y1 float64) image.Rectangle {
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	if y1 < y0 {
		y0, y1 = y1, y0
	}
	return image.Rect(int(math.Floor(x0)), int(math.Floor(y0)),
		int(math.Ceil(x1)), int(math.Ceil(y1)))
}

// opaqueBounds returns the bounds of the non-transparent pixels of img.
func opaqueBounds(img *image.RGBA) (image.Rectangle, bool) {
	b := img.Bounds()
	minX, minY := b.Max.X, b.Max.Y
	maxX, maxY := b.Min.X, b.Min.Y
	found := false
	for y := b.Min.Y; y < b.Max.Y; y++ {
		off := img.PixOffset(b.Min.X, y)
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.Pix[off+3] != 0 {
				found = true
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
			off += 4
		}
	}
	if !found {
		return image.Rectangle{}, false
	}
	return image.Rect(minX, minY, maxX+1, maxY+1), true
}

// gaussianKernel returns a normalized gaussian kernel for the given standard
// deviation. The radius is clamped to maxBlurKernelRadius.
func gaussianKernel(std float64) []float32 {
	r := int(math.Ceil(3 * std))
	if r < 1 {
		return []float32{1}
	}
	if r > maxBlurKernelRadius {
		r = maxBlurKernelRadius
	}
	k := make([]float32, 2*r+1)
	var sum float32
	for i := -r; i <= r; i++ {
		v := float32(math.Exp(-float64(i*i) / (2 * std * std)))
		k[i+r] = v
		sum += v
	}
	for i := range k {
		k[i] /= sum
	}
	return k
}

// blurAlpha returns the gaussian blurred alpha channel of img as row-major
// values in [0,1], using a separable kernel with edge clamping.
func blurAlpha(img *image.RGBA, stdX, stdY float64) []float32 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	n := w * h
	src := make([]float32, n)
	for i := 0; i < n; i++ {
		src[i] = float32(img.Pix[i*4+3]) / 255
	}
	dst := make([]float32, n)
	// Horizontal pass.
	kx := gaussianKernel(stdX)
	rx := len(kx) / 2
	for y := 0; y < h; y++ {
		row := y * w
		for x := 0; x < w; x++ {
			var sum float32
			for i := -rx; i <= rx; i++ {
				j := x + i
				if j < 0 {
					j = 0
				} else if j >= w {
					j = w - 1
				}
				sum += kx[i+rx] * src[row+j]
			}
			dst[row+x] = sum
		}
	}
	// Vertical pass.
	ky := gaussianKernel(stdY)
	ry := len(ky) / 2
	for y := 0; y < h; y++ {
		row := y * w
		for x := 0; x < w; x++ {
			var sum float32
			for i := -ry; i <= ry; i++ {
				j := y + i
				if j < 0 {
					j = 0
				} else if j >= h {
					j = h - 1
				}
				sum += ky[i+ry] * dst[j*w+x]
			}
			src[row+x] = sum
		}
	}
	return src
}
