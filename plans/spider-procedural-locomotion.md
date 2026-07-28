# Procedural Spider Locomotion with Rigid Body Physics

## Overview

This document specifies an architecture for realistic procedural spider (or
generally multi-legged) locomotion. The system is physically grounded rather
than purely kinematic/animated.

The key design principle: split the problem into three layers that update
every physics tick.

1. **Body** — a single rigid body (the torso), driven by physics forces/torques.
2. **Legs** — IK chains (kinematic, not individually simulated as rigid bodies)
   that solve to foot targets.
3. **Gait controller** — decides where and when each foot should plant, based
   on body movement and terrain.

### Why legs are IK, not individually rigid-bodied

If each leg segment is its own physics-simulated rigid body (e.g. ragdoll-style
chains with joint motors), the system fights itself — the legs jitter, gaits
become unstable, and tuning becomes very painful. Instead:

- The **body** is the only thing physics simulates directly (forces/torques).
- **Legs** are kinematic IK solvers whose foot targets are computed by the gait
  controller.
- Foot **contact** feeds forces back into the body (via spring/constraint
  forces), which is what makes the whole thing feel physical despite the legs
  themselves being kinematic.

This mirrors how most production procedural-animation rigs handle
quadrupeds/hexapods/octopods in games and robotics.

---

## 1. Body Rigid Body

- One `RigidBody` for the torso: mass, linear drag, angular drag.
- Movement input (or AI steering) applies **force** and **torque** directly to
  this body. Never teleport or directly set its position/rotation.
- Add a raycast-based "hover" spring: cast downward from the body center, and
  apply a spring-damper force to keep the body at a target height above the
  average ground plane defined by the current foot contacts. This produces
  suspension-like behavior over bumpy terrain.

```
targetHeight = averagePlaneHeight(currentFootContacts) + restHeight
error = targetHeight - body.position.y
force.y += error * springK - body.velocity.y * springDamp
```

- Body orientation (pitch/roll) should align to the plane fitted through the
  current foot contact points (see Section 4), applied as a **corrective
  torque** — not a hard-set rotation. This keeps the body's motion physically
  continuous rather than snapping.

---

## 2. Per-Leg IK

For each leg, define:

- **Root**: the hip attachment point on the body, fixed in the body's local
  space.
- **Chain**: an ordered list of segments (e.g. coxa, femur, tibia — or however
  many joints the model has).
- **Foot target**: a world-space position that the gait controller updates.

### Two-segment legs: analytic (two-bone) IK

If each leg has only 2 significant segments (e.g. femur + tibia), solve
analytically using the law of cosines — cheap and perfectly stable (same
technique used for arms/legs in most character rigs):

```
solveLegIK(hipPos, targetPos, segmentLengths):
    dist = clamp(length(targetPos - hipPos), 0, sum(segmentLengths) - epsilon)
    # law of cosines to get joint angles for a 2-bone chain
    angle1 = lawOfCosines(segmentLengths[0], segmentLengths[1], dist)
    angle2 = lawOfCosines(dist, segmentLengths[0], segmentLengths[1])
    return [angle1, angle2]
```

### Longer chains: FABRIK

If legs have 3+ significant segments, use FABRIK (Forward And Backward
Reaching Inverse Kinematics) — general-purpose and still cheap:

```
FABRIK(chain, target, iterations=10):
    for i in range(iterations):
        # backward pass: from foot to root
        chain[-1].pos = target
        for i from len-2 down to 0:
            reposition chain[i] along direction to chain[i+1], preserving segment length

        # forward pass: from root to foot
        chain[0].pos = fixedRootPos
        for i from 1 to len-1:
            reposition chain[i] along direction from chain[i-1], preserving segment length

        if error < threshold: break
```

---

## 3. Foot Stepping Trigger

Each leg has an **anchor point**: where its foot *should* be relative to the
body if the body weren't moving (a fixed offset in body-local space,
projected onto the ground below). Each tick:

```
desiredFootPos = raycastToGround(body.position + body.rotation * legLocalOffset)

if distance(currentFootTarget, desiredFootPos) > stepThreshold:
    triggerStep(leg, desiredFootPos)
```

`triggerStep` does **not** snap the foot to the new position. Instead it kicks
off a short step animation:

- The foot lifts (add a vertical arc — half-sine or bezier — layered on top of
  the horizontal path).
- The foot swings toward `desiredFootPos`, with a bit of **lookahead** in the
  direction of travel so it lands slightly ahead of where it's needed rather
  than behind (this avoids the foot immediately being "late" again next tick).
- The foot plants at the end of the swing.

While a leg is mid-step, it is **excluded** from the support polygon
(Section 4).

```
stepFoot(leg, newTarget, duration):
    start = leg.footTarget
    for t in 0..duration:
        alpha = t / duration
        horizontal = lerp(start, newTarget, easeInOut(alpha))
        vertical = liftHeight * sin(pi * alpha)
        leg.footTarget = horizontal + up * vertical
    leg.footTarget = newTarget  # ensure exact landing, no drift
```

---

## 4. Gait Coordination

With many legs (e.g. 8 for a spider), you cannot let them all step
simultaneously — the body would lose support and topple under physics. Two
approaches, in increasing order of realism/complexity:

### A. Grouped/alternating gait (simpler, robust)

Split legs into groups (e.g. two groups of four, alternating like a tetrapod
trot, or a proper tripod/wave pattern for hexapod/octopod creatures). Only one
group may be "in flight" (stepping) at a time; the rest stay planted.

```
if leg.wantsToStep and no other leg in leg.group is currently stepping:
    triggerStep(leg, desiredFootPos)
```

This is easy to implement and tune, and is a good first milestone.

### B. Reactive / free gait (more realistic, terrain-adaptive)

Don't predefine groups. Instead, only allow a leg to lift if doing so keeps
the body's projected center of mass inside the convex hull (support polygon)
of the *remaining* planted feet:

```
canLift(leg):
    remainingFeet = allPlantedFeet - {leg}
    hull = convexHull2D(remainingFeet.positions.xz)
    return pointInPolygon(bodyCOM.xz, hull, margin=safetyMargin)
```

Run this per leg each tick. Any leg that both *wants* to step (past the
distance threshold) and is *safe* to lift gets triggered; if multiple legs
qualify simultaneously, prioritize whichever has the largest positional error.

This is what produces genuinely organic, terrain-adaptive footfall patterns
(comparable to how modern legged robots and high-end game creature rigs plan
steps), rather than a fixed, robotic-looking cycle.

**Recommendation:** implement A first to validate the rest of the pipeline,
then upgrade to B once the base system is stable.

### Desynchronization

Regardless of approach, add small random phase offsets / per-leg timing
variance so legs don't fall into a perfectly repeating, robotic-looking
pattern. Real arthropod gaits have slight asymmetry.

---

## 5. Feeding Foot Contact Back Into Body Physics

The body should not be moved kinematically based on "average foot position" —
that reads as floaty and disconnected. Instead:

- **Planted feet act as constraint anchors.** Apply a corrective force at each
  planted foot's body-attachment point that opposes relative slip (essentially
  a friction/anchor constraint). This is what actually propels the body
  forward as legs "row" backward relative to the body during their stance
  phase.
- **Body height/pitch/roll** are driven by springs against the plane fit
  through currently planted feet (Section 1) — never by direct assignment.
- **Gravity and collision** are left entirely to the physics engine. The
  IK/gait layer only ever decides *foot targets*; it must never override how
  the physics engine resolves the body's actual position, falls, slopes, or
  collisions with obstacles.

---

## 6. Full Per-Tick Loop (Summary)

```
every physics step:
    for leg in legs:
        desired = raycastGround(bodyLocalAnchor(leg))
        if shouldStep(leg, desired):      # distance threshold + gait/support rules
            beginStep(leg, desired)
        updateStepAnimation(leg)          # advances lift/swing/plant
        ikResult = solveLegIK(leg.hip, leg.footTarget, leg.segmentLengths)
        applyJointAngles(leg, ikResult)

    plane = fitPlane(plantedFeetPositions)
    applyHoverSpringForce(body, plane)
    applyOrientationTorque(body, plane.normal)
    applyPlantedFootConstraintForces(legs)
```

---

## Implementation Notes / Suggested Milestones

1. **Body + single static stance**: get the hover spring and orientation
   torque working with all feet permanently "planted" at fixed local offsets
   (no stepping yet). Validates the physics feedback loop.
2. **Add per-leg IK**: two-bone analytic IK first if legs are 2-segment;
   FABRIK if longer. Validate against static targets.
3. **Add stepping with grouped gait (4a)**: distance-threshold-triggered
   steps, alternating leg groups, lift/swing/plant animation with lookahead.
4. **Add planted-foot constraint forces**: this is what makes the body
   actually get propelled by leg motion instead of just following a hover
   spring passively.
5. **Upgrade to reactive/free gait (4b)**: convex hull + center-of-mass
   support check, replacing the fixed groups.
6. **Tuning pass**: spring constants, step thresholds, lift height, step
   duration, desync/phase jitter, lookahead distance — these all need
   iteration against the actual body mass/size/leg count to look right.

### Key parameters to expose for tuning

- `restHeight`, `springK`, `springDamp` (body hover)
- `stepThreshold` (distance before a foot re-plants)
- `liftHeight`, `stepDuration` (swing animation)
- `lookaheadFactor` (how far ahead of current need the foot plants)
- `safetyMargin` (support-polygon slack for free gait)
- Per-leg phase offset / timing jitter range
