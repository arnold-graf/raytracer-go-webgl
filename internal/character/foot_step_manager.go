package character

import (
	"math"
	"sort"

	"raytracer/internal/vec"
)

// legStepMode selects gait coordination strategy (Unity IKStepManager).
type legStepMode int

const (
	legStepTetrapod legStepMode = iota
	legStepQueueWait
	legStepQueueNoWait
)

// legStepManager coordinates when legs may step.
type legStepManager struct {
	mode         legStepMode
	gaitGroupA   []int
	gaitGroupB   []int
	groupIsA     bool
	nextSwitch   float64
	stepTime     float64
	dynamicTime  bool
	time         float64
}

func newLegStepManager(legs []LegDef, mode legStepMode) legStepManager {
	n := len(legs)
	groupA := make([]int, 0, n/2)
	groupB := make([]int, 0, n/2)
	for i := range legs {
		if i%2 == 0 {
			groupA = append(groupA, i)
		} else {
			groupB = append(groupB, i)
		}
	}
	return legStepManager{
		mode:        mode,
		gaitGroupA:  groupA,
		gaitGroupB:   groupB,
		groupIsA:     true,
		stepTime:     0.32,
		dynamicTime:  true,
	}
}

func legStepModeFromConfig(s string) legStepMode {
	switch s {
	case "tetrapod":
		return legStepTetrapod
	case "queue_wait":
		return legStepQueueWait
	default:
		return legStepQueueNoWait
	}
}

func (m *legStepManager) tick(dt float64, spider *SpiderLocomotor, rig *Rig, world FootWorld, chains []legChain, steppers []legStepper, maxSwing int) {
	if spider.Speed < 0.05 {
		for i := range steppers {
			steppers[i].advanceStep(dt, spider, world)
		}
		return
	}
	m.time += dt
	if m.mode == legStepTetrapod {
		m.tetrapod(dt, spider, rig, world, chains, steppers)
	} else {
		m.queue(dt, spider, rig, world, chains, steppers, maxSwing)
	}
	for i := range steppers {
		steppers[i].advanceStep(dt, spider, world)
		steppers[i].timeSinceStep += dt
	}
}

func (m *legStepManager) tetrapod(dt float64, spider *SpiderLocomotor, rig *Rig, world FootWorld, chains []legChain, steppers []legStepper) {
	if m.time < m.nextSwitch {
		return
	}
	if m.groupIsA {
		m.groupIsA = false
	} else {
		m.groupIsA = true
	}
	group := m.gaitGroupB
	if m.groupIsA {
		group = m.gaitGroupA
	}
	stepTime := m.averageStepTime(chains, steppers, group, spider, rig)
	m.nextSwitch = m.time + stepTime
	moving := spider.Speed >= 0.05
	com := spiderBodyCOM(spider.Body.Pos)
	for _, idx := range group {
		if idx >= len(steppers) {
			continue
		}
		st := &steppers[idx]
		if !st.stepCheck(moving, spider, rig, idx) {
			continue
		}
		remaining := 0
		for j, f := range spider.Feet {
			if j == idx || !f.Initialized || f.Phase == FootSwing {
				continue
			}
			remaining++
		}
		if remaining >= 4 && !spiderCanLift(spider.Feet, idx, com, spiderSupportMargin) {
			if st.chain.Error < st.chain.Tolerance*1.5 {
				continue
			}
		}
		st.beginStep(stepTime, spider, rig, world, idx)
	}
}

func (m *legStepManager) queue(dt float64, spider *SpiderLocomotor, rig *Rig, world FootWorld, chains []legChain, steppers []legStepper, maxSwing int) {
	moving := spider.Speed >= 0.05
	swinging := 0
	for i := range steppers {
		if steppers[i].isStepping {
			swinging++
		}
	}
	if maxSwing <= 0 {
		maxSwing = m.maxSwing()
	}
	type stepCand struct {
		idx  int
		dist float64
	}
	var urgent []stepCand
	for i := range steppers {
		st := &steppers[i]
		if !st.stepCheck(moving, spider, rig, i) {
			continue
		}
		hip := st.chain.Hinges[0].Point
		plant := st.plantPos(spider, i)
		d := horizDist(vec.V{X: hip.X, Z: hip.Z}, vec.V{X: plant.X, Z: plant.Z})
		urgent = append(urgent, stepCand{idx: i, dist: d})
	}
	sort.Slice(urgent, func(a, b int) bool { return urgent[a].dist > urgent[b].dist })
	for _, c := range urgent {
		if swinging >= maxSwing {
			break
		}
		i := c.idx
		st := &steppers[i]
		if st.isStepping {
			continue
		}
		hip := st.chain.Hinges[0].Point
		plant := st.plantPos(spider, i)
		hipPlant := horizDist(vec.V{X: hip.X, Z: hip.Z}, vec.V{X: plant.X, Z: plant.Z})
		if !st.allowedToStep(steppers) {
			if hipPlant < spiderMaxHipPlantHoriz*0.70 {
				continue
			}
		}
		remaining := 0
		for j, f := range spider.Feet {
			if j == i || !f.Initialized || f.Phase == FootSwing {
				continue
			}
			remaining++
		}
		if remaining >= 4 && !spiderCanLift(spider.Feet, i, spiderBodyCOM(spider.Body.Pos), spiderSupportMargin) {
			if st.chain.Error < st.chain.Tolerance*1.5 {
				continue
			}
		}
		stepTime := m.legStepTime(&chains[i], st, spider, rig)
		chains[i].Paused = false
		st.beginStep(stepTime, spider, rig, world, i)
		swinging++
	}
}

func (m *legStepManager) averageStepTime(chains []legChain, steppers []legStepper, group []int, spider *SpiderLocomotor, rig *Rig) float64 {
	if !m.dynamicTime {
		return m.stepTime
	}
	sum := 0.0
	n := 0
	for _, idx := range group {
		if idx >= len(chains) {
			continue
		}
		sum += m.legStepTime(&chains[idx], &steppers[idx], spider, rig)
		n++
	}
	if n == 0 {
		return m.stepTime
	}
	return sum / float64(n)
}

func (m *legStepManager) legStepTime(chain *legChain, st *legStepper, spider *SpiderLocomotor, rig *Rig) float64 {
	if !m.dynamicTime {
		return m.stepTime
	}
	const floor = 0.06
	ceiling := m.stepTime
	if spider != nil && rig != nil {
		pace := spider.paceScale(rig)
		if pace < 0.25 {
			pace = 0.25
		}
		ceiling = spiderStepDuration / math.Sqrt(pace)
		if ceiling > m.stepTime {
			ceiling = m.stepTime
		}
		if ceiling < floor {
			ceiling = floor
		}
	}
	vel := chain.TipVelocity.Len()
	if vel < 1e-6 {
		return ceiling
	}
	t := 0.50 * spiderScale / vel
	if t > ceiling {
		t = ceiling
	}
	if t < floor {
		t = floor
	}
	_ = st
	return t
}

func (m *legStepManager) maxSwing() int {
	pace := 4
	if m.dynamicTime {
		pace = 6
	}
	return pace
}

func easeInOutStep(t float64) float64 {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return t * t * (3 - 2*t)
}

// unused but kept for step arc variety
var _ = math.Pi
