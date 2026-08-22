package scene

// PhysicsMode describes how an included object participates in Jolt simulation.
type PhysicsMode string

const (
	PhysicsStatic    PhysicsMode = "static"
	PhysicsCompound  PhysicsMode = "compound"
	PhysicsPieces    PhysicsMode = "pieces"
	PhysicsKinematic PhysicsMode = "kinematic"
	PhysicsDynamic   PhysicsMode = "dynamic" // single dynamic body (alias for compound)
)

// PhysicsSpec is scene-authored simulation metadata (mass in kilograms).
type PhysicsSpec struct {
	Mode        PhysicsMode
	MassKg      float64 // 0 = estimate from volume × default density
	Friction    float64 // 0 = engine default
	Restitution float64
	Sleep       bool
}

// PhysicsGroup binds one simulation body to merged primitive spans.
type PhysicsGroup struct {
	Name string
	Spec PhysicsSpec
	Body DynamicBody
}

// DefaultPropDensity is used when mass is omitted (kg/m³).
const DefaultPropDensity = 600.0
