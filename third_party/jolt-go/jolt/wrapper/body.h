/*
 * Jolt Physics C Wrapper - Body Operations
 *
 * Handles rigid body creation and manipulation.
 */

#ifndef JOLT_WRAPPER_BODY_H
#define JOLT_WRAPPER_BODY_H

#include "physics.h"

#ifdef __cplusplus
extern "C" {
#endif

// Opaque pointer types
typedef void* JoltBodyInterface;
typedef void* JoltBodyID;
typedef void* JoltShape;

// Motion type enum (matches Jolt's EMotionType)
typedef enum {
    JoltMotionTypeStatic = 0,    // Immovable, zero velocity
    JoltMotionTypeKinematic = 1, // Movable by user, zero velocity response to forces
    JoltMotionTypeDynamic = 2    // Affected by forces
} JoltMotionType;

// Get the body interface for creating/manipulating bodies
JoltBodyInterface JoltPhysicsSystemGetBodyInterface(JoltPhysicsSystem system);

// Get the position of a body
void JoltGetBodyPosition(const JoltBodyInterface bodyInterface,
                        const JoltBodyID bodyID,
                        float* x, float* y, float* z);

// Set the position of a body
void JoltSetBodyPosition(JoltBodyInterface bodyInterface,
                        JoltBodyID bodyID,
                        float x, float y, float z);

// Create a body with specific motion type and sensor flag
JoltBodyID JoltCreateBody(JoltBodyInterface bodyInterface,
                          JoltShape shape,
                          float x, float y, float z,
                          JoltMotionType motionType,
                          int isSensor);

// Activate a body (makes it participate in simulation)
void JoltActivateBody(JoltBodyInterface bodyInterface, JoltBodyID bodyID);

// Deactivate a body (removes from active simulation)
void JoltDeactivateBody(JoltBodyInterface bodyInterface, JoltBodyID bodyID);

// Set the shape of a body
void JoltSetBodyShape(JoltBodyInterface bodyInterface,
                     JoltBodyID bodyID,
                     JoltShape shape,
                     int updateMassProperties);

// Destroy a body ID
void JoltDestroyBodyID(JoltBodyID bodyID);

// Get body rotation as quaternion (x, y, z, w)
void JoltGetBodyRotation(const JoltBodyInterface bodyInterface,
                        const JoltBodyID bodyID,
                        float* qx, float* qy, float* qz, float* qw);

// Set position and rotation; activate controls EActivation
void JoltSetBodyPositionAndRotation(JoltBodyInterface bodyInterface,
                                    JoltBodyID bodyID,
                                    float x, float y, float z,
                                    float qx, float qy, float qz, float qw,
                                    int activate);

// Create body with orientation, mass (kg, 0 = auto), friction, restitution
JoltBodyID JoltCreateBodyEx(JoltBodyInterface bodyInterface,
                            JoltShape shape,
                            float x, float y, float z,
                            float qx, float qy, float qz, float qw,
                            JoltMotionType motionType,
                            int isSensor,
                            float massKg,
                            float friction,
                            float restitution,
                            int allowSleeping);

// Sensor flag without recreating the body
void JoltSetBodySensor(JoltBodyInterface bodyInterface,
                       JoltBodyID bodyID,
                       int isSensor);

// Reports whether the body is active (awake)
int JoltIsBodyActive(JoltBodyInterface bodyInterface, const JoltBodyID bodyID);

// Apply impulse at world-space point
void JoltApplyImpulse(JoltBodyInterface bodyInterface,
                      JoltBodyID bodyID,
                      float px, float py, float pz,
                      float ix, float iy, float iz);

// Remove body from simulation and destroy it
void JoltRemoveAndDestroyBody(JoltBodyInterface bodyInterface, JoltBodyID bodyID);

#ifdef __cplusplus
}
#endif

#endif // JOLT_WRAPPER_BODY_H
