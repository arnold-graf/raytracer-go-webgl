package app

import (
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"

	"raytracer/internal/sceneio"
	"raytracer/internal/textlayout"
)

const (
	hintFontFile   = "M42.ttf"
	hintFontSizePx = 8
	hintMargin     = 24
)

func (g *Game) drawInteractHint(screen *ebiten.Image) {
	if g.activeHint == "" || g.transitionActive {
		return
	}
	sw, sh := screen.Bounds().Dx(), screen.Bounds().Dy()
	img, w, h, ok := g.hintImage(g.activeHint)
	if !ok {
		x := (sw - len(g.activeHint)*8) / 2
		if x < hintMargin {
			x = hintMargin
		}
		y := sh - hintMargin - 14
		ebitenutil.DebugPrintAt(screen, g.activeHint, x, y)
		return
	}
	x := (sw - w) / 2
	if x < hintMargin {
		x = hintMargin
	}
	y := sh - hintMargin - h
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(float64(x), float64(y))
	screen.DrawImage(img, op)
}

func (g *Game) hintImage(text string) (*ebiten.Image, int, int, bool) {
	if text == g.hintCached && g.hintCachedImg != nil {
		return g.hintCachedImg, g.hintCachedW, g.hintCachedH, true
	}
	g.hintCachedImg = nil
	g.hintCached = text

	face, err := g.hintFontFace()
	if err != nil {
		return nil, 0, 0, false
	}
	w := font.MeasureString(face, text).Ceil()
	h := face.Metrics().Height.Ceil()
	if w <= 0 || h <= 0 {
		return nil, 0, 0, false
	}
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	d := &font.Drawer{
		Dst:  rgba,
		Src:  image.NewUniform(color.RGBA{235, 235, 240, 255}),
		Face: face,
		Dot:  fixed.P(0, face.Metrics().Ascent.Ceil()),
	}
	d.DrawString(text)
	g.hintCachedImg = ebiten.NewImageFromImage(rgba)
	g.hintCachedW = w
	g.hintCachedH = h
	return g.hintCachedImg, w, h, true
}

func (g *Game) hintFontFace() (font.Face, error) {
	if g.hintFont != nil {
		return g.hintFont, nil
	}
	path, err := sceneio.ResolveFontPath(hintFontFile)
	if err != nil {
		return nil, err
	}
	face, err := textlayout.LoadFace(path, hintFontSizePx)
	if err != nil {
		return nil, err
	}
	g.hintFont = face
	return face, nil
}
