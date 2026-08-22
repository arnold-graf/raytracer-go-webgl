/*
 * Jolt Physics C Wrapper - Body Operations Implementation
 */

#include "body.h"
#include "physics.h"
#include <Jolt/Jolt.h>
#include <Jolt/Physics/PhysicsSystem.h>
#include <Jolt/Physics/Body/Body.h>
#include <Jolt/Physics/Body/BodyCreationSettings.h>
#include <Jolt/Physics/Body/BodyInterface.h>
#include <Jolt/Physics/Body/MotionType.h>
#include <Jolt/Physics/Body/AllowedDOFs.h>
#include <memory>

using namespace JPH;

// Collision layers (defined in physics.cpp)
namespace Layers
{
	static constexpr ObjectLayer NON_MOVING = 0;
	static constexpr ObjectLayer MOVING = 1;
};

JoltBodyInterface JoltPhysicsSystemGetBodyInterface(JoltPhysicsSystem system)
{
	PhysicsSystemWrapper *wrapper = static_cast<PhysicsSystemWrapper *>(system);
	PhysicsSystem* ps = GetPhysicsSystem(wrapper);
	BodyInterface* bi = &ps->GetBodyInterface();

	return static_cast<JoltBodyInterface>(bi);
}

void JoltGetBodyPosition(const JoltBodyInterface bodyInterface,
						 const JoltBodyID bodyID,
						 float *x, float *y, float *z)
{
	const BodyInterface *bi = static_cast<const BodyInterface *>(bodyInterface);
	const BodyID *bid = static_cast<const BodyID *>(bodyID);

	RVec3 pos = bi->GetPosition(*bid);
	*x = static_cast<float>(pos.GetX());
	*y = static_cast<float>(pos.GetY());
	*z = static_cast<float>(pos.GetZ());
}

void JoltSetBodyPosition(JoltBodyInterface bodyInterface,
						 JoltBodyID bodyID,
						 float x, float y, float z)
{
	BodyInterface *bi = static_cast<BodyInterface *>(bodyInterface);
	const BodyID *bid = static_cast<const BodyID *>(bodyID);

	bi->SetPosition(*bid, RVec3(x, y, z), EActivation::DontActivate);
}

JoltBodyID JoltCreateBody(JoltBodyInterface bodyInterface,
						  JoltShape shape,
						  float x, float y, float z,
						  JoltMotionType motionType,
						  int isSensor)
{
	BodyInterface *bi = static_cast<BodyInterface *>(bodyInterface);
	const Shape *s = static_cast<const Shape *>(shape);

	// Convert motion type
	EMotionType joltMotionType;
	ObjectLayer layer;
	switch (motionType)
	{
	case JoltMotionTypeStatic:
		joltMotionType = EMotionType::Static;
		layer = Layers::NON_MOVING;
		break;
	case JoltMotionTypeKinematic:
		joltMotionType = EMotionType::Kinematic;
		layer = Layers::MOVING;
		break;
	case JoltMotionTypeDynamic:
		joltMotionType = EMotionType::Dynamic;
		layer = Layers::MOVING;
		break;
	default:
		joltMotionType = EMotionType::Static;
		layer = Layers::NON_MOVING;
		break;
	}

	BodyCreationSettings body_settings(
		s,
		RVec3(x, y, z),
		Quat::sIdentity(),
		joltMotionType,
		layer);

	// Set sensor flag if requested
	body_settings.mIsSensor = (isSensor != 0);

	Body *body = bi->CreateBody(body_settings);
	if (!body)
	{
		return nullptr;
	}

	// Don't activate yet - caller will activate when ready
	bi->AddBody(body->GetID(), EActivation::DontActivate);

	// Use smart pointer for exception safety, then release to caller
	auto bodyIDPtr = std::make_unique<BodyID>(body->GetID());
	return static_cast<JoltBodyID>(bodyIDPtr.release());
}

void JoltActivateBody(JoltBodyInterface bodyInterface, JoltBodyID bodyID)
{
	BodyInterface *bi = static_cast<BodyInterface *>(bodyInterface);
	const BodyID *bid = static_cast<const BodyID *>(bodyID);

	bi->ActivateBody(*bid);
}

void JoltDeactivateBody(JoltBodyInterface bodyInterface, JoltBodyID bodyID)
{
	BodyInterface *bi = static_cast<BodyInterface *>(bodyInterface);
	const BodyID *bid = static_cast<const BodyID *>(bodyID);

	bi->DeactivateBody(*bid);
}

void JoltSetBodyShape(JoltBodyInterface bodyInterface,
					 JoltBodyID bodyID,
					 JoltShape shape,
					 int updateMassProperties)
{
	BodyInterface *bi = static_cast<BodyInterface *>(bodyInterface);
	const BodyID *bid = static_cast<const BodyID *>(bodyID);
	const Shape *s = static_cast<const Shape *>(shape);

	bi->SetShape(*bid, s, updateMassProperties != 0, EActivation::Activate);
}

void JoltDestroyBodyID(JoltBodyID bodyID)
{
	BodyID *bid = static_cast<BodyID *>(bodyID);
	delete bid;
}

void JoltGetBodyRotation(const JoltBodyInterface bodyInterface,
						 const JoltBodyID bodyID,
						 float *qx, float *qy, float *qz, float *qw)
{
	const BodyInterface *bi = static_cast<const BodyInterface *>(bodyInterface);
	const BodyID *bid = static_cast<const BodyID *>(bodyID);
	Quat q = bi->GetRotation(*bid);
	*qx = static_cast<float>(q.GetX());
	*qy = static_cast<float>(q.GetY());
	*qz = static_cast<float>(q.GetZ());
	*qw = static_cast<float>(q.GetW());
}

void JoltSetBodyPositionAndRotation(JoltBodyInterface bodyInterface,
									JoltBodyID bodyID,
									float x, float y, float z,
									float qx, float qy, float qz, float qw,
									int activate)
{
	BodyInterface *bi = static_cast<BodyInterface *>(bodyInterface);
	const BodyID *bid = static_cast<const BodyID *>(bodyID);
	EActivation act = activate ? EActivation::Activate : EActivation::DontActivate;
	bi->SetPositionAndRotation(*bid, RVec3(x, y, z), Quat(qx, qy, qz, qw), act);
}

JoltBodyID JoltCreateBodyEx(JoltBodyInterface bodyInterface,
							JoltShape shape,
							float x, float y, float z,
							float qx, float qy, float qz, float qw,
							JoltMotionType motionType,
							int isSensor,
							float massKg,
							float friction,
							float restitution,
							int allowSleeping)
{
	BodyInterface *bi = static_cast<BodyInterface *>(bodyInterface);
	const Shape *s = static_cast<const Shape *>(shape);

	EMotionType joltMotionType;
	ObjectLayer layer;
	switch (motionType)
	{
	case JoltMotionTypeStatic:
		joltMotionType = EMotionType::Static;
		layer = Layers::NON_MOVING;
		break;
	case JoltMotionTypeKinematic:
		joltMotionType = EMotionType::Kinematic;
		layer = Layers::MOVING;
		break;
	case JoltMotionTypeDynamic:
		joltMotionType = EMotionType::Dynamic;
		layer = Layers::MOVING;
		break;
	default:
		joltMotionType = EMotionType::Static;
		layer = Layers::NON_MOVING;
		break;
	}

	BodyCreationSettings body_settings(
		s,
		RVec3(x, y, z),
		Quat(qx, qy, qz, qw),
		joltMotionType,
		layer);

	body_settings.mIsSensor = (isSensor != 0);
	body_settings.mFriction = friction;
	body_settings.mRestitution = restitution;
	body_settings.mAllowSleeping = (allowSleeping != 0);

	if (massKg > 0 && joltMotionType == EMotionType::Dynamic)
	{
		body_settings.mOverrideMassProperties = EOverrideMassProperties::MassAndInertiaProvided;
		body_settings.mMassPropertiesOverride.mMass = massKg;
		// Inertia from shape volume scaled to target mass
		const Shape *shapePtr = s;
		MassProperties mp = shapePtr->GetMassProperties();
		if (mp.mMass > 0)
		{
			float scale = massKg / mp.mMass;
			body_settings.mMassPropertiesOverride.mInertia = mp.mInertia * scale;
		}
	}

	Body *body = bi->CreateBody(body_settings);
	if (!body)
	{
		return nullptr;
	}

	bi->AddBody(body->GetID(), EActivation::DontActivate);

	auto bodyIDPtr = std::make_unique<BodyID>(body->GetID());
	return static_cast<JoltBodyID>(bodyIDPtr.release());
}

void JoltSetBodySensor(JoltBodyInterface bodyInterface,
					   JoltBodyID bodyID,
					   int isSensor)
{
	BodyInterface *bi = static_cast<BodyInterface *>(bodyInterface);
	const BodyID *bid = static_cast<const BodyID *>(bodyID);
	bi->SetIsSensor(*bid, isSensor != 0);
}

int JoltIsBodyActive(JoltBodyInterface bodyInterface, const JoltBodyID bodyID)
{
	const BodyInterface *bi = static_cast<const BodyInterface *>(bodyInterface);
	const BodyID *bid = static_cast<const BodyID *>(bodyID);
	return bi->IsActive(*bid) ? 1 : 0;
}

void JoltApplyImpulse(JoltBodyInterface bodyInterface,
					  JoltBodyID bodyID,
					  float px, float py, float pz,
					  float ix, float iy, float iz)
{
	BodyInterface *bi = static_cast<BodyInterface *>(bodyInterface);
	const BodyID *bid = static_cast<const BodyID *>(bodyID);
	bi->AddImpulse(*bid, Vec3(ix, iy, iz));
}

void JoltRemoveAndDestroyBody(JoltBodyInterface bodyInterface, JoltBodyID bodyID)
{
	BodyInterface *bi = static_cast<BodyInterface *>(bodyInterface);
	const BodyID *bid = static_cast<const BodyID *>(bodyID);
	bi->RemoveBody(*bid);
	bi->DestroyBody(*bid);
	delete bid;
}
