package sceneparam

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
