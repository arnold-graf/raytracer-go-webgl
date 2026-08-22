package jolt

// #include "wrapper/shape.h"
import "C"

// Shape represents collision geometry that can be used to create bodies
type Shape struct {
	handle C.JoltShape
}

// Destroy frees the shape (decrements ref count)
func (s *Shape) Destroy() {
	C.JoltDestroyShape(s.handle)
}

// CreateSphereShape creates a sphere collision shape
func CreateSphere(radius float32) *Shape {
	handle := C.JoltCreateSphere(C.float(radius))
	if handle == nil {
		return nil
	}
	return &Shape{handle: handle}
}

// CreateBoxShape creates a box collision shape
// halfExtent: half-size of the box in each dimension (e.g., Vec3{5,0.5,5} creates 10x1x10 box)
func CreateBox(halfExtent Vec3) *Shape {
	handle := C.JoltCreateBox(
		C.float(halfExtent.X),
		C.float(halfExtent.Y),
		C.float(halfExtent.Z),
	)
	if handle == nil {
		return nil
	}
	return &Shape{handle: handle}
}

// CreateCapsuleShape creates a capsule collision shape (cylinder with hemispherical caps)
// halfHeight: half-height of the cylindrical part (not including the caps)
// radius: radius of the capsule
func CreateCapsule(halfHeight, radius float32) *Shape {
	handle := C.JoltCreateCapsule(
		C.float(halfHeight),
		C.float(radius),
	)
	if handle == nil {
		return nil
	}
	return &Shape{handle: handle}
}

// CreateConvexHullShape creates a convex hull collision shape from a set of points
// points: slice of Vec3 vertices that define the convex hull
func CreateConvexHull(points []Vec3) *Shape {
	if len(points) == 0 {
		return nil
	}
	floatPoints := make([]C.float, len(points)*3)
	for i, p := range points {
		floatPoints[i*3] = C.float(p.X)
		floatPoints[i*3+1] = C.float(p.Y)
		floatPoints[i*3+2] = C.float(p.Z)
	}

	handle := C.JoltCreateConvexHull(
		&floatPoints[0],
		C.int(len(points)),
	)
	if handle == nil {
		return nil
	}
	return &Shape{handle: handle}
}

// CreateMeshShape creates a mesh collision shape from vertices and triangle indices
// vertices: slice of Vec3 vertices
// indices: slice of triangle indices (must be multiple of 3, each triangle is 3 indices)
// Note: Mesh shapes are typically used for static geometry (e.g., terrain, buildings)
func CreateMesh(vertices []Vec3, indices []int32) *Shape {
	if len(vertices) == 0 || len(indices) < 3 {
		return nil
	}
	floatVertices := make([]C.float, len(vertices)*3)
	for i, v := range vertices {
		floatVertices[i*3] = C.float(v.X)
		floatVertices[i*3+1] = C.float(v.Y)
		floatVertices[i*3+2] = C.float(v.Z)
	}

	cIndices := make([]C.int, len(indices))
	for i, idx := range indices {
		cIndices[i] = C.int(idx)
	}

	handle := C.JoltCreateMesh(
		&floatVertices[0],
		C.int(len(vertices)),
		&cIndices[0],
		C.int(len(indices)),
	)
	if handle == nil {
		return nil
	}
	return &Shape{handle: handle}
}

// CompoundPart is one child shape in a compound collider.
type CompoundPart struct {
	Shape    *Shape
	LocalPos Vec3
	LocalRot Quat
}

// CreateStaticCompound builds one compound shape from child shapes and local poses.
func CreateStaticCompound(parts []CompoundPart) *Shape {
	if len(parts) == 0 {
		return nil
	}
	shapes := make([]C.JoltShape, len(parts))
	posX := make([]C.float, len(parts))
	posY := make([]C.float, len(parts))
	posZ := make([]C.float, len(parts))
	rotX := make([]C.float, len(parts))
	rotY := make([]C.float, len(parts))
	rotZ := make([]C.float, len(parts))
	rotW := make([]C.float, len(parts))
	for i, p := range parts {
		shapes[i] = p.Shape.handle
		posX[i] = C.float(p.LocalPos.X)
		posY[i] = C.float(p.LocalPos.Y)
		posZ[i] = C.float(p.LocalPos.Z)
		rotX[i] = C.float(p.LocalRot.X)
		rotY[i] = C.float(p.LocalRot.Y)
		rotZ[i] = C.float(p.LocalRot.Z)
		rotW[i] = C.float(p.LocalRot.W)
	}
	handle := C.JoltCreateStaticCompound(
		&shapes[0],
		&posX[0],
		&posY[0],
		&posZ[0],
		&rotX[0],
		&rotY[0],
		&rotZ[0],
		&rotW[0],
		C.int(len(parts)),
	)
	if handle == nil {
		return nil
	}
	return &Shape{handle: handle}
}

// RRayCast represents a ray for raycasting against shapes
type RRayCast struct {
	Origin    Vec3 // Starting point of the ray
	Direction Vec3 // Direction vector (length determines max distance)
}

// BackfaceMode determines how backfaces are handled during raycasting
type BackfaceMode int

const (
	BackfaceModeIgnore         BackfaceMode = 0 // Ignore backfaces (default)
	BackfaceModeCollideWithAll BackfaceMode = 1 // Collide with both front and backfaces
)

// RayCastSettings contains settings for shape raycasting
type RayCastSettings struct {
	BackfaceMode       BackfaceMode // How to handle backfaces
	TreatConvexAsSolid bool         // Treat convex shapes as solid (true) or hollow (false)
}

// DefaultRayCastSettings returns default raycast settings
func DefaultRayCastSettings() RayCastSettings {
	return RayCastSettings{
		BackfaceMode:       BackfaceModeIgnore,
		TreatConvexAsSolid: true,
	}
}

// RayCastResult contains the result of a shape raycast
type RayCastResult struct {
	Fraction float32 // Fraction along the ray where hit occurred [0, 1]
}

// CastRay casts a ray against this shape and returns the hit result
func (s *Shape) CastRay(ray RRayCast, settings RayCastSettings, result *RayCastResult) bool {
	var cFraction C.float

	hit := C.JoltShapeCastRay(
		s.handle,
		C.float(ray.Origin.X),
		C.float(ray.Origin.Y),
		C.float(ray.Origin.Z),
		C.float(ray.Direction.X),
		C.float(ray.Direction.Y),
		C.float(ray.Direction.Z),
		C.int(settings.BackfaceMode),
		C.int(boolToInt(settings.TreatConvexAsSolid)),
		&cFraction,
	)

	if hit != 0 {
		result.Fraction = float32(cFraction)
		return true
	}
	return false
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TransformedShape combines a shape with a position and rotation for world-space operations
type TransformedShape struct {
	handle C.JoltTransformedShape
}

// CreateTransformedShape creates a transformed shape with the given shape, position, rotation, and optional body ID
func CreateTransformedShape(shape *Shape, position Vec3, rotation Quat, bodyID uint32) *TransformedShape {
	handle := C.JoltCreateTransformedShape(
		shape.handle,
		C.float(position.X),
		C.float(position.Y),
		C.float(position.Z),
		C.float(rotation.X),
		C.float(rotation.Y),
		C.float(rotation.Z),
		C.float(rotation.W),
		C.uint(bodyID),
	)
	return &TransformedShape{handle: handle}
}

// Destroy frees the transformed shape
func (ts *TransformedShape) Destroy() {
	C.JoltDestroyTransformedShape(ts.handle)
}

// CastRay casts a ray against this transformed shape in world space and returns the hit result
func (ts *TransformedShape) CastRay(ray RRayCast, result *RayCastResult) bool {
	var cFraction C.float

	hit := C.JoltTransformedShapeCastRay(
		ts.handle,
		C.float(ray.Origin.X),
		C.float(ray.Origin.Y),
		C.float(ray.Origin.Z),
		C.float(ray.Direction.X),
		C.float(ray.Direction.Y),
		C.float(ray.Direction.Z),
		&cFraction,
	)

	if hit != 0 {
		result.Fraction = float32(cFraction)
		return true
	}
	return false
}
