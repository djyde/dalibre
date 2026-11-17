package usbms

import (
	"bytes"
	"image"
	"image/jpeg"

	_ "image/gif"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"golang.org/x/image/draw"
)

// Thumbnail is img, a cover, as a JPEG height pixels high, as calibre
// keeps covers for readers; nil when img isn't an image.
func Thumbnail(img []byte, height int) []byte {
	src, _, err := image.Decode(bytes.NewReader(img))
	if err != nil {
		return nil
	}
	b := src.Bounds()
	if b.Dy() <= 0 || b.Dx() <= 0 {
		return nil
	}
	if height <= 0 || b.Dy() <= height {
		height = b.Dy()
	}
	width := max(1, b.Dx()*height/b.Dy())
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 88}); err != nil {
		return nil
	}
	return out.Bytes()
}

// imageSize is the width and height of an encoded image, 0 when unknown.
func imageSize(data []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}
