// Package thumbs generates small derived preview images (longest side <=
// 480px) from uploaded photos, so lists never transfer originals.
package thumbs

import (
	"bytes"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"

	"github.com/rwcarlsen/goexif/exif"
	xdraw "golang.org/x/image/draw"

	_ "golang.org/x/image/webp" // register webp decoding
)

const MaxLongSide = 480

// Generate returns a preview of src. ext is "jpg", "png", or "gif" (no dot),
// chosen by source format: jpeg/webp encode as JPEG (photos; webp has no
// stdlib encoder), png stays PNG (alpha-safe), gif stays GIF (first frame).
func Generate(src []byte) (thumb []byte, ext string, err error) {
	img, format, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, "", err
	}
	if format == "jpeg" {
		if o := jpegOrientation(src); o > 1 {
			img = applyOrientation(img, o)
		}
	}
	img = fitLongSide(img, MaxLongSide)

	var buf bytes.Buffer
	switch format {
	case "png":
		ext = "png"
		err = png.Encode(&buf, img)
	case "gif":
		ext = "gif"
		err = gif.Encode(&buf, img, nil)
	default: // jpeg, webp
		ext = "jpg"
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 78})
	}
	if err != nil {
		return nil, "", err
	}
	return buf.Bytes(), ext, nil
}

// jpegOrientation returns the EXIF Orientation tag (1..8), or 0 when absent
// or unreadable (degrade gracefully: browsers honor EXIF on the original,
// so an unrotated thumb is the only failure mode).
func jpegOrientation(src []byte) int {
	x, err := exif.Decode(bytes.NewReader(src))
	if err != nil {
		return 0
	}
	tag, err := x.Get(exif.Orientation)
	if err != nil {
		return 0
	}
	v, err := tag.Int(0)
	if err != nil || v < 1 || v > 8 {
		return 0
	}
	return v
}

// applyOrientation returns img transformed for EXIF orientation o so it
// displays upright (browsers do this implicitly for originals).
func applyOrientation(img image.Image, o int) image.Image {
	switch o {
	case 2:
		return flipH(img)
	case 3:
		return rotate180(img)
	case 4:
		return flipV(img)
	case 5:
		return rotate90(flipH(img))
	case 6:
		return rotate90(img)
	case 7:
		return rotate270(flipH(img))
	case 8:
		return rotate270(img)
	}
	return img
}

func toRGBA(src image.Image) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

func rotate90(src image.Image) *image.RGBA { // clockwise
	s := toRGBA(src)
	b := s.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(b.Dy()-1-y, x, s.At(x, y))
		}
	}
	return dst
}

func rotate180(src image.Image) *image.RGBA {
	s := toRGBA(src)
	b := s.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(b.Dx()-1-x, b.Dy()-1-y, s.At(x, y))
		}
	}
	return dst
}

func rotate270(src image.Image) *image.RGBA { // counterclockwise
	s := toRGBA(src)
	b := s.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(y, b.Dx()-1-x, s.At(x, y))
		}
	}
	return dst
}

func flipH(src image.Image) *image.RGBA {
	s := toRGBA(src)
	b := s.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(b.Dx()-1-x, y, s.At(x, y))
		}
	}
	return dst
}

func flipV(src image.Image) *image.RGBA {
	s := toRGBA(src)
	b := s.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(x, b.Dy()-1-y, s.At(x, y))
		}
	}
	return dst
}

// fitLongSide scales img down so its longest side is <= max; never upscales.
func fitLongSide(img image.Image, max int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	long := w
	if h > long {
		long = h
	}
	if long <= max {
		return img
	}
	scale := float64(max) / float64(long)
	dw, dh := int(float64(w)*scale), int(float64(h)*scale)
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	return dst
}
