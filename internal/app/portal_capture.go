package app

import (
	"math"

	"raytracer/internal/render"
	"raytracer/internal/texture"
)

// portalShot describes a yaw/pitch offset (degrees) from the player's view when
// taking one of the five cube-wall captures.
type portalShot struct {
	yawDeg, pitchDeg float64
}

// portalShots order matches texture.CaptureIDs: forward, left, right, up, down.
// Yaw offsets follow the engine convention (+yaw looks toward +X).
var portalShots = []portalShot{
	{0, 0},
	{60, 0},
	{-60, 0},
	{0, 60},
	{0, -60},
}

func (g *Game) capturePortalViews() texture.PortalCapture {
	saved := g.cam.Pose()
	savedPix := g.pixSize
	savedQuant := g.colorQuant
	g.pixSize = 1
	g.colorQuant = 3 // raw RGB in shader (no dither/quant)

	defer func() {
		g.cam.SetPose(saved)
		g.pixSize = savedPix
		g.colorQuant = savedQuant
	}()

	size := g.rw
	if g.rh > size {
		size = g.rh
	}
	cap := texture.PortalCapture{Width: size, Height: size}
	buf := make([]byte, size*size*4)
	square, ok := g.ren.(render.SquareCapturer)
	view := g.view()
	deg := math.Pi / 180

	for i, shot := range portalShots {
		g.cam.SetPose(saved)
		g.cam.Yaw += shot.yawDeg * deg
		g.cam.Pitch = clampCapturePitch(saved.Pitch + shot.pitchDeg*deg)
		if ok {
			square.RenderSquare(buf, size, g.cam, view)
		} else {
			g.ren.Render(g.buf, g.cam, view, g.pixSize)
			cropSquareFromBuffer(buf, size, g.buf, g.rw, g.rh)
		}
		img := make([]byte, len(buf))
		copy(img, buf)
		cap.Images[i] = img
	}
	return cap
}

// cropSquareFromBuffer copies the largest centered square from a rectangular RGBA frame.
func cropSquareFromBuffer(dst []byte, size int, src []byte, sw, sh int) {
	if size <= 0 || len(dst) < size*size*4 {
		return
	}
	sq := sw
	if sh < sq {
		sq = sh
	}
	if size > sq {
		size = sq
	}
	x0 := (sw - size) / 2
	y0 := (sh - size) / 2
	for y := 0; y < size; y++ {
		srcOff := ((y0 + y) * sw) * 4
		dstOff := y * size * 4
		copy(dst[dstOff:dstOff+size*4], src[srcOff+x0*4:srcOff+x0*4+size*4])
	}
}

func clampCapturePitch(p float64) float64 {
	const maxP = math.Pi/2 - 0.01
	if p > maxP {
		return maxP
	}
	if p < -maxP {
		return -maxP
	}
	return p
}
