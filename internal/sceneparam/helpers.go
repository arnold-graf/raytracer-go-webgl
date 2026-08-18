package sceneparam

import "math"

const radPerDeg = math.Pi / 180
const degPerRad = 180 / math.Pi

// Trig helpers take or return degrees to match scene rotate_* fields.

func sinDeg(deg float64) float64 {
	return math.Sin(deg * radPerDeg)
}

func cosDeg(deg float64) float64 {
	return math.Cos(deg * radPerDeg)
}

func tanDeg(deg float64) float64 {
	return math.Tan(deg * radPerDeg)
}

func asinDeg(x float64) float64 {
	return math.Asin(x) * degPerRad
}

func acosDeg(x float64) float64 {
	return math.Acos(x) * degPerRad
}

func atanDeg(x float64) float64 {
	return math.Atan(x) * degPerRad
}

func legX(i int, off, radius float64) float64 {
	if i < 2 {
		return off - radius
	}
	return -off - radius
}

func legZ(i int, off, radius float64) float64 {
	if i == 0 || i == 2 {
		return off - radius
	}
	return -off - radius
}

func ringLerp(i, rings int, top, bot float64) float64 {
	if rings <= 1 {
		return top
	}
	return top - (top-bot)*float64(i)/float64(rings-1)
}

// hash01 returns a deterministic value in [0, 1) from a seed and index.
func hash01(seed, index float64) float64 {
	x := math.Sin(seed*12.9898+index*78.233) * 43758.5453
	return x - math.Floor(x)
}

func bookThickness(seed, index, minT, maxT float64) float64 {
	if maxT < minT {
		minT, maxT = maxT, minT
	}
	return minT + hash01(seed+6.1, index+13.7)*(maxT-minT)
}

func bookClusterCount(seed, width, gap, minT, maxT float64) int {
	if width <= 0 {
		return 0
	}
	used := 0.0
	for i := 0; i < 64; i++ {
		t := bookThickness(seed, float64(i), minT, maxT)
		next := used + t
		if i > 0 {
			next += gap
		}
		if i > 0 && next > width+1e-9 {
			return i
		}
		used = next
	}
	return 64
}

// bookClusterX returns the bottom-centre X of book index i in a left-packed row.
func bookClusterX(seed, index, width, gap, minT, maxT float64) float64 {
	i := int(index)
	if i < 0 {
		return 0
	}
	used := 0.0
	for k := 0; k < i; k++ {
		used += bookThickness(seed, float64(k), minT, maxT) + gap
	}
	t := bookThickness(seed, index, minT, maxT)
	return -width/2 + used + t/2
}
