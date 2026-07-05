package texture

import (
	"bytes"
	"image"
	"image/color"
	"image/png"

	"raytracer/internal/textlayout"
)

// RasterizeDocument renders headline and paragraphs into a fixed-size RGBA8
// bitmap suitable for a document texture slot.
func RasterizeDocument(headline string, paragraphs []string, fontPath string, fontSizePx int) (*CaptureImage, error) {
	style := textlayout.PaperStyle(DocumentTexW, DocumentTexH, fontSizePx)
	style.PostFill = addPaperGrain
	bmp, err := textlayout.Rasterize(headline, paragraphs, fontPath, style)
	if err != nil {
		return nil, err
	}
	return BitmapFromLayout(bmp), nil
}

// RasterizeText renders arbitrary text into a bitmap with a custom layout style.
// Use this for signs, labels, screens, or other non-document surfaces.
func RasterizeText(headline string, paragraphs []string, fontPath string, style textlayout.Style) (*CaptureImage, error) {
	bmp, err := textlayout.Rasterize(headline, paragraphs, fontPath, style)
	if err != nil {
		return nil, err
	}
	return BitmapFromLayout(bmp), nil
}

// NormalizeParagraphs splits long strings on blank lines when authors use a single string.
func NormalizeParagraphs(ps []string) []string { return textlayout.NormalizeParagraphs(ps) }

// addPaperGrain lightly modulates the flat fill so the page is not perfectly uniform.
func addPaperGrain(img *image.RGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			n := 0.5 + 0.5*perlin(float64(x)*0.35, float64(y)*0.35, 0)
			c := img.RGBAAt(x, y)
			scale := 0.97 + 0.03*n
			c.R = uint8(float64(c.R) * scale)
			c.G = uint8(float64(c.G) * scale)
			c.B = uint8(float64(c.B) * scale)
			img.SetRGBA(x, y, c)
		}
	}
}

// EncodeDocumentPNG is a test helper that encodes a rasterized document as PNG.
func EncodeDocumentPNG(headline string, paragraphs []string, fontPath string, fontSizePx int) ([]byte, error) {
	img, err := RasterizeDocument(headline, paragraphs, fontPath, fontSizePx)
	if err != nil {
		return nil, err
	}
	rgba := image.NewRGBA(image.Rect(0, 0, img.Width, img.Height))
	for j := 0; j < img.Height; j++ {
		for i := 0; i < img.Width; i++ {
			off := (j*img.Width + i) * 4
			rgba.SetRGBA(i, j, color.RGBA{img.RGBA[off], img.RGBA[off+1], img.RGBA[off+2], img.RGBA[off+3]})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, rgba); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
