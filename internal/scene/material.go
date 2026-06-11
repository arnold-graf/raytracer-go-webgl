package scene

// Material identifiers. The numeric values match the original JavaScript
// renderer so the scene data ports across unchanged.
const (
	MatDiffuse = 0 // Lambertian diffuse
	MatMirror  = 1 // perfect / rough reflector
	MatMetal   = 3 // tinted reflector (treated like a colored mirror)
	MatGlass   = 4 // dielectric refraction with total-internal-reflection
	MatEmit    = 5 // emissive light source
	MatChecker = 6 // diffuse with a procedural checkerboard albedo
)

const eps = 1e-4 // shared ray-epsilon to avoid self-intersection
