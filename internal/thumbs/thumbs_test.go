package thumbs

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

// 8x8 lossy WebP, 64 bytes. Verified decodable by golang.org/x/image/webp.
const tinyWebP = "UklGRjgAAABXRUJQVlA4ICwAAACQAQCdASoIAAgAAgA0JaACdLoAA5gA/vBsr/c1FbFvQ/9pZ/9Sz/6ln+KgAA=="

// 4x2 JPEG carrying EXIF Orientation=6 (rotate 90 CW to display).
// Verified: goexif reads orientation 6; image/jpeg decodes 4x2.
const orient6JPEG = "/9j/4QAiRXhpZgAASUkqAAgAAAABABIBAwABAAAABgAAAAAAAAD/4AAQSkZJRgABAQAAAQABAAD/2wBDAAMCAgICAgMCAgIDAwMDBAYEBAQEBAgGBgUGCQgKCgkICQkKDA8MCgsOCwkJDRENDg8QEBEQCgwSExIQEw8PEBAQ/9sAQwEDAwMEAwQIBAQIEAsJCxAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBD/wAARCAACAAQDAREAAhEBAxEB/8QAFAABAAAAAAAAAAAAAAAAAAAACP/EABoQAAEFAQAAAAAAAAAAAAAAAAUAAhZVlNH/xAAVAQEBAAAAAAAAAAAAAAAAAAADCf/EABwRAAAHAQEAAAAAAAAAAAAAAAADBAYXVdKTlP/aAAwDAQACEQMRAD8AA8hP3hDS/qo1GDHpknnJwAkR4Wynubsf/9k="

// makeImage builds a w*h gradient PNG/JPEG/GIF in memory.
func makeImage(t *testing.T, w, h int, enc string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 255 / w), G: uint8(y * 255 / h), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	switch enc {
	case "jpeg":
		if err := jpeg.Encode(&buf, img, nil); err != nil {
			t.Fatal(err)
		}
	case "png":
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
	case "gif":
		if err := gif.Encode(&buf, img, nil); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

func decodeDims(t *testing.T, b []byte, ext string) (int, int) {
	t.Helper()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("thumb does not decode as %s: %v", ext, err)
	}
	return cfg.Width, cfg.Height
}

func TestGenerateFormatMapping(t *testing.T) {
	cases := []struct {
		name string
		src  []byte
		ext  string // expected thumb extension
	}{
		{"jpeg source -> jpg", makeImage(t, 900, 600, "jpeg"), "jpg"},
		{"webp source -> jpg", mustB64(t, tinyWebP), "jpg"},
		{"png source -> png", makeImage(t, 900, 600, "png"), "png"},
		{"gif source -> gif", makeImage(t, 900, 600, "gif"), "gif"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			thumb, ext, err := Generate(tc.src)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if ext != tc.ext {
				t.Fatalf("ext = %q, want %q", ext, tc.ext)
			}
			w, h := decodeDims(t, thumb, ext)
			if w > 480 || h > 480 {
				t.Fatalf("thumb %dx%d exceeds 480 long side: %dx%d", w, h, w, h)
			}
		})
	}
}

func TestGenerateNoUpscale(t *testing.T) {
	thumb, ext, err := Generate(mustB64(t, tinyWebP)) // 8x8 source
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if w, h := decodeDims(t, thumb, ext); w != 8 || h != 8 {
		t.Fatalf("8x8 upscaled to %dx%d; never upscale", w, h)
	}
}

func TestGenerateEXIFOrientation6(t *testing.T) {
	// 4x2 landscape + orientation 6 (rotate 90 CW) => displayed 2x4 portrait.
	thumb, ext, err := Generate(mustB64(t, orient6JPEG))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if ext != "jpg" {
		t.Fatalf("ext = %q, want jpg", ext)
	}
	if w, h := decodeDims(t, thumb, ext); w != 2 || h != 4 {
		t.Fatalf("oriented thumb = %dx%d, want 2x4", w, h)
	}
}

// A 2x1 image, red at (0,0) and blue at (1,0), distinguishes transpose
// (orientation 5) from anti-transpose (orientation 7) by pixel color.
func TestApplyOrientationTranspose(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	red := color.RGBA{R: 255, A: 255}
	blue := color.RGBA{B: 255, A: 255}
	src.Set(0, 0, red)
	src.Set(1, 0, blue)

	// Orientation 5 = transpose: 1 wide x 2 tall, red top, blue bottom.
	o5 := applyOrientation(src, 5)
	if b := o5.Bounds(); b.Dx() != 1 || b.Dy() != 2 {
		t.Fatalf("orientation 5 bounds = %v, want 1x2", b)
	}
	assertPixel(t, o5, 0, 0, red)
	assertPixel(t, o5, 0, 1, blue)

	// Orientation 7 = anti-transpose: 1 wide x 2 tall, blue top, red bottom.
	o7 := applyOrientation(src, 7)
	if b := o7.Bounds(); b.Dx() != 1 || b.Dy() != 2 {
		t.Fatalf("orientation 7 bounds = %v, want 1x2", b)
	}
	assertPixel(t, o7, 0, 0, blue)
	assertPixel(t, o7, 0, 1, red)
}

func assertPixel(t *testing.T, img image.Image, x, y int, want color.RGBA) {
	t.Helper()
	got := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
	if got != want {
		t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, got, want)
	}
}

func TestGenerateCorruptInput(t *testing.T) {
	junk := append([]byte{}, makeImage(t, 40, 40, "jpeg")...)
	junk = junk[:len(junk)/2] // truncated mid-stream
	if _, _, err := Generate(junk); err == nil {
		t.Fatal("truncated jpeg: expected error, got nil")
	}
	if _, _, err := Generate([]byte("not an image at all")); err == nil {
		t.Fatal("garbage: expected error, got nil")
	}
}

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("bad fixture: %v", err)
	}
	return b
}
