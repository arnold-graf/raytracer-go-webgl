// Package render rasterizes the scene into an 8-bit RGBA framebuffer. It owns
// the resolution, the block-upscaling ("pixel size") optimization, ordered
// dithering, and the goroutine fan-out across CPU cores.
package render

import (
	"math"
	"runtime"
	"sync"

	"raytracer/internal/camera"
	"raytracer/internal/trace"
	"raytracer/internal/vec"
)

// bayer4 is the 4x4 ordered-dither matrix from the original renderer.
var bayer4 = [4][4]int{
	{0, 8, 2, 10},
	{12, 4, 14, 6},
	{3, 11, 1, 9},
	{15, 7, 13, 5},
}

// Renderer produces frames at a fixed internal resolution.
type Renderer struct {
	W, H     int
	fovScale float64
	aspect   float64
	workers  int
	// tex is a persistent per-pixel texture cache (one Texel per pixel), reused
	// across frames so a static view skips re-evaluating procedural textures.
	tex []trace.Texel
}

// New creates a renderer for the given internal resolution.
func New(w, h int) *Renderer {
	return &Renderer{
		W:        w,
		H:        h,
		fovScale: math.Tan(60 * math.Pi / 360),
		aspect:   float64(w) / float64(h),
		workers:  runtime.NumCPU(),
		tex:      make([]trace.Texel, w*h),
	}
}

// Render fills buf (len = W*H*4, RGBA) by tracing the scene from cam. pixSize
// renders one ray per pixSize x pixSize block and replicates the result,
// trading resolution for speed. Work is split across CPU cores by row.
func (r *Renderer) Render(buf []byte, cam *camera.Camera, tr *trace.Tracer, pixSize int) {
	if pixSize < 1 {
		pixSize = 1
	}
	fwd, right, up := cam.Basis()

	// Each worker strides over block-rows, so work is interleaved across cores
	// without allocating a per-frame schedule.
	stride := r.workers * pixSize
	var wg sync.WaitGroup
	for w := 0; w < r.workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for py := id * pixSize; py < r.H; py += stride {
				r.renderRow(buf, py, pixSize, cam, tr, fwd, right, up)
			}
		}(w)
	}
	wg.Wait()
}

// renderRow renders a single block-row (height pixSize) into buf.
func (r *Renderer) renderRow(buf []byte, py, pixSize int, cam *camera.Camera, tr *trace.Tracer, fwd, right, up vec.V) {
	for px := 0; px < r.W; px += pixSize {
		u := (float64(px)+float64(pixSize)*0.5)/float64(r.W)*2 - 1
		v := 1 - (float64(py)+float64(pixSize)*0.5)/float64(r.H)*2
		ray := cam.Ray(fwd, right, up, u, v, r.aspect, r.fovScale)

		col := trace.ToneMap(tr.TracePixel(ray, &r.tex[py*r.W+px]))
		bdt := (float64(bayer4[py&3][px&3])/16 - 0.5) * 6
		cr := clampByte(col.X*255 + bdt)
		cg := clampByte(col.Y*255 + bdt)
		cb := clampByte(col.Z*255 + bdt)

		for dy := 0; dy < pixSize && py+dy < r.H; dy++ {
			row := (py + dy) * r.W
			for dx := 0; dx < pixSize && px+dx < r.W; dx++ {
				i := (row + px + dx) * 4
				buf[i] = cr
				buf[i+1] = cg
				buf[i+2] = cb
				buf[i+3] = 255
			}
		}
	}
}

func clampByte(x float64) byte {
	if x < 0 {
		return 0
	}
	if x > 255 {
		return 255
	}
	return byte(x)
}
