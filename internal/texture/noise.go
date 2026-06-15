package texture

import "math"

// perm is Ken Perlin's reference permutation table, duplicated to 512 entries
// so index arithmetic never needs a modulo.
var perm [512]int

func init() {
	base := [256]int{
		151, 160, 137, 91, 90, 15, 131, 13, 201, 95, 96, 53, 194, 233, 7, 225,
		140, 36, 103, 30, 69, 142, 8, 99, 37, 240, 21, 10, 23, 190, 6, 148,
		247, 120, 234, 75, 0, 26, 197, 62, 94, 252, 219, 203, 117, 35, 11, 32,
		57, 177, 33, 88, 237, 149, 56, 87, 174, 20, 125, 136, 171, 168, 68, 175,
		74, 165, 71, 134, 139, 48, 27, 166, 77, 146, 158, 231, 83, 111, 229, 122,
		60, 211, 133, 230, 220, 105, 92, 41, 55, 46, 245, 40, 244, 102, 143, 54,
		65, 25, 63, 161, 1, 216, 80, 73, 209, 76, 132, 187, 208, 89, 18, 169,
		200, 196, 135, 130, 116, 188, 159, 86, 164, 100, 109, 198, 173, 186, 3, 64,
		52, 217, 226, 250, 124, 123, 5, 202, 38, 147, 118, 126, 255, 82, 85, 212,
		207, 206, 59, 227, 47, 16, 58, 17, 182, 189, 28, 42, 223, 183, 170, 213,
		119, 248, 152, 2, 44, 154, 163, 70, 221, 153, 101, 155, 167, 43, 172, 9,
		129, 22, 39, 253, 19, 98, 108, 110, 79, 113, 224, 232, 178, 185, 112, 104,
		218, 246, 97, 228, 251, 34, 242, 193, 238, 210, 144, 12, 191, 179, 162, 241,
		81, 51, 145, 235, 249, 14, 239, 107, 49, 192, 214, 31, 181, 199, 106, 157,
		184, 84, 204, 176, 115, 121, 50, 45, 127, 4, 150, 254, 138, 236, 205, 93,
		222, 114, 67, 29, 24, 72, 243, 141, 128, 195, 78, 66, 215, 61, 156, 180,
	}
	for i := 0; i < 256; i++ {
		perm[i] = base[i]
		perm[i+256] = base[i]
	}
}

// PermTable returns a copy of Perlin's 512-entry permutation table so other
// packages (e.g. the WebGPU packer) can upload the exact same gradients the CPU
// noise uses, keeping GPU procedural textures bit-faithful to the CPU.
func PermTable() [512]int { return perm }

func fade(t float64) float64 { return t * t * t * (t*(t*6-15) + 10) }

func lerp(t, a, b float64) float64 { return a + t*(b-a) }

// grad returns the dot product of a pseudo-random gradient (selected by hash)
// with the offset vector (x,y,z), per Perlin's reference implementation.
func grad(hash int, x, y, z float64) float64 {
	h := hash & 15
	u := x
	if h >= 8 {
		u = y
	}
	v := y
	if h >= 4 {
		if h == 12 || h == 14 {
			v = x
		} else {
			v = z
		}
	}
	if h&1 != 0 {
		u = -u
	}
	if h&2 != 0 {
		v = -v
	}
	return u + v
}

// perlin returns 3D Perlin noise in the range [-1, 1].
func perlin(x, y, z float64) float64 {
	xi := int(math.Floor(x)) & 255
	yi := int(math.Floor(y)) & 255
	zi := int(math.Floor(z)) & 255
	x -= math.Floor(x)
	y -= math.Floor(y)
	z -= math.Floor(z)
	u, v, w := fade(x), fade(y), fade(z)

	a := perm[xi] + yi
	aa := perm[a] + zi
	ab := perm[a+1] + zi
	b := perm[xi+1] + yi
	ba := perm[b] + zi
	bb := perm[b+1] + zi

	return lerp(w,
		lerp(v,
			lerp(u, grad(perm[aa], x, y, z), grad(perm[ba], x-1, y, z)),
			lerp(u, grad(perm[ab], x, y-1, z), grad(perm[bb], x-1, y-1, z))),
		lerp(v,
			lerp(u, grad(perm[aa+1], x, y, z-1), grad(perm[ba+1], x-1, y, z-1)),
			lerp(u, grad(perm[ab+1], x, y-1, z-1), grad(perm[bb+1], x-1, y-1, z-1))))
}

// fbm sums `octaves` of Perlin noise (fractal Brownian motion), each octave at
// double the frequency and roughly half the amplitude. Result is ~[-1, 1].
func fbm(x, y, z float64, octaves int) float64 {
	sum, amp, freq, norm := 0.0, 1.0, 1.0, 0.0
	for i := 0; i < octaves; i++ {
		sum += amp * perlin(x*freq, y*freq, z*freq)
		norm += amp
		amp *= 0.5
		freq *= 2.0
	}
	if norm == 0 {
		return 0
	}
	return sum / norm
}

// FBM exposes fractal Brownian motion for other packages (e.g. terrain height
// fields). See fbm.
func FBM(x, y, z float64, octaves int) float64 { return fbm(x, y, z, octaves) }

// Perlin exposes raw 3D Perlin noise in [-1,1] (e.g. for water ripples).
func Perlin(x, y, z float64) float64 { return perlin(x, y, z) }

// Turbulence exposes the billowy absolute-value fBm in ~[0,1] (e.g. for clouds).
func Turbulence(x, y, z float64, octaves int) float64 { return turbulence(x, y, z, octaves) }

// turbulence is the absolute-value variant of fbm, producing the billowy,
// vein-like patterns used by wood and marble.
func turbulence(x, y, z float64, octaves int) float64 {
	sum, amp, freq, norm := 0.0, 1.0, 1.0, 0.0
	for i := 0; i < octaves; i++ {
		sum += amp * math.Abs(perlin(x*freq, y*freq, z*freq))
		norm += amp
		amp *= 0.5
		freq *= 2.0
	}
	if norm == 0 {
		return 0
	}
	return sum / norm
}
