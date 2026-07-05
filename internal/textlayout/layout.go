package textlayout

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"strings"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Bitmap is a CPU-side RGBA8 image suitable for GPU upload.
type Bitmap struct {
	Width, Height int
	RGBA          []byte
}

// Style configures text layout and rasterization.
type Style struct {
	Width, Height int
	Background    color.Color
	Margin        int
	BodySizePx    int
	HeadlineScale float64 // body size multiplier for headline; 0 = same as body
	LineGap       int     // 0 = BodySizePx/4
	ParaGap       int     // 0 = BodySizePx
	HeadlineColor color.Color
	BodyColor     color.Color
	// PostFill runs after the background fill (e.g. paper grain).
	PostFill func(img *image.RGBA)
}

// PaperStyle returns defaults for a readable document page at the given size.
func PaperStyle(width, height, bodySizePx int) Style {
	if bodySizePx <= 0 {
		bodySizePx = 18
	}
	margin := width / 14
	if margin <= 0 {
		margin = 8
	}
	return Style{
		Width:         width,
		Height:        height,
		Background:    color.RGBA{245, 242, 235, 255},
		Margin:        margin,
		BodySizePx:    bodySizePx,
		HeadlineScale: 1.35,
		HeadlineColor: color.RGBA{20, 18, 16, 255},
		BodyColor:     color.RGBA{30, 28, 26, 255},
	}
}

// Rasterize renders headline and paragraphs into a bitmap using style.
func Rasterize(headline string, paragraphs []string, fontPath string, style Style) (*Bitmap, error) {
	w, h := style.Width, style.Height
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("textlayout: invalid size %dx%d", w, h)
	}
	bodyPx := style.BodySizePx
	if bodyPx <= 0 {
		bodyPx = 18
	}
	face, err := LoadFace(fontPath, bodyPx)
	if err != nil {
		return nil, err
	}
	headPx := bodyPx
	if style.HeadlineScale > 0 {
		headPx = int(float64(bodyPx) * style.HeadlineScale)
	}
	headFace, err := LoadFace(fontPath, headPx)
	if err != nil {
		return nil, err
	}

	margin := style.Margin
	if margin <= 0 {
		margin = w / 14
	}
	maxWidth := w - 2*margin
	lineGap := style.LineGap
	if lineGap <= 0 {
		lineGap = bodyPx / 4
	}
	paraGap := style.ParaGap
	if paraGap <= 0 {
		paraGap = bodyPx
	}
	bg := style.Background
	if bg == nil {
		bg = color.White
	}
	headCol := style.HeadlineColor
	if headCol == nil {
		headCol = color.Black
	}
	bodyCol := style.BodyColor
	if bodyCol == nil {
		bodyCol = color.Black
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	if style.PostFill != nil {
		style.PostFill(img)
	}

	y := margin + headlineHeight(headFace, headline)
	if headline != "" {
		y = drawWrapped(img, headline, headFace, margin, y, maxWidth, lineGap, headCol)
		y += paraGap
	}
	for i, para := range paragraphs {
		if strings.TrimSpace(para) == "" {
			continue
		}
		y = drawWrapped(img, para, face, margin, y, maxWidth, lineGap, bodyCol)
		if i+1 < len(paragraphs) {
			y += paraGap
		}
	}

	rgba := make([]byte, w*h*4)
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			off := (j*w + i) * 4
			r, g, b, a := img.At(i, j).RGBA()
			rgba[off] = byte(r >> 8)
			rgba[off+1] = byte(g >> 8)
			rgba[off+2] = byte(b >> 8)
			rgba[off+3] = byte(a >> 8)
		}
	}
	return &Bitmap{Width: w, Height: h, RGBA: rgba}, nil
}

// LoadFace opens fontPath at sizePx, falling back to Go Regular when missing.
func LoadFace(fontPath string, sizePx int) (font.Face, error) {
	data, err := os.ReadFile(fontPath)
	if err != nil {
		data = goregular.TTF
	}
	f, err := opentype.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse font: %w", err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    float64(sizePx),
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, fmt.Errorf("new face: %w", err)
	}
	return face, nil
}

// NormalizeParagraphs splits long strings on blank lines when authors use a single string.
func NormalizeParagraphs(ps []string) []string {
	if len(ps) == 0 {
		return ps
	}
	var out []string
	for _, p := range ps {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		for _, chunk := range strings.Split(p, "\n\n") {
			chunk = strings.TrimSpace(chunk)
			if chunk != "" {
				out = append(out, collapseSpace(chunk))
			}
		}
	}
	return out
}

func headlineHeight(face font.Face, headline string) int {
	if headline == "" {
		return 0
	}
	return face.Metrics().Ascent.Ceil()
}

func drawWrapped(img *image.RGBA, text string, face font.Face, x, y, maxWidth, lineGap int, col color.Color) int {
	for _, line := range WrapText(text, face, maxWidth) {
		d := &font.Drawer{
			Dst:  img,
			Src:  image.NewUniform(col),
			Face: face,
			Dot:  fixed.P(x, y+face.Metrics().Ascent.Ceil()),
		}
		d.DrawString(line)
		y += face.Metrics().Height.Ceil() + lineGap
	}
	return y
}

// WrapText breaks text into lines that fit maxWidth using font metrics.
func WrapText(text string, face font.Face, maxWidth int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		next := line + " " + w
		if textWidth(face, next) <= maxWidth {
			line = next
			continue
		}
		lines = append(lines, line)
		line = w
	}
	lines = append(lines, line)
	return lines
}

func textWidth(face font.Face, s string) int {
	return font.MeasureString(face, s).Ceil()
}

func collapseSpace(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
