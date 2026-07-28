package npc

import (
	"container/heap"
	"math"

	"raytracer/internal/character"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

const (
	navCellSize            = 0.4
	navMaxGridCells        = 128 // cap grid build cost on large outdoor scenes
	navMaxStepDown         = 2.00 // max downward step between grid cells (m)
	navPathArriveDist            = 0.35
	navDefaultTargetRadius       = 0.75 // patrol/goal considered reached within this horizontal distance
	navArrivalSpeedScale         = 0.30 // arrival radius >= walkSpeed * this
	navArrivalClearanceScale     = 0.65 // arrival radius >= clearance * this
	navArrivalOvershootScale     = 2.5  // accept pass-through within this * arrival radius
	navHeadingTurn         = 120.0 // deg/s toward desired heading
	navBoundsMargin        = 2.0
	navGroundProbeY        = 10.0
	navDefaultWalkSpd      = 1.0
	navReplanInterval      = 0.35 // seconds between pathfinding retries
	navLevelTolerance      = 0.60 // max allowed step-up above hinted floor level
)

type navPathResult struct {
	id   uint64
	from vec.V
	to   vec.V
	path []vec.V
}

// Navigator steers an agent along a coarse grid path toward patrol waypoints or a goal.
type Navigator struct {
	patrol       []vec.V
	goal         *vec.V
	wpIdx        int
	path         []vec.V
	pathIdx      int
	replanCD     float64
	walkSpeed    float64
	targetRadius float64
	active       bool
	pathCache    map[int][]vec.V
	pathReqID    uint64
	pathBusy     bool
	pathReady    chan navPathResult
}

// NewNavigator returns a navigator for spawn data, or nil when no patrol/goal is set.
func NewNavigator(sp scene.NPCSpawn) *Navigator {
	targetRadius := sp.TargetRadius
	if targetRadius <= 0 {
		targetRadius = navDefaultTargetRadius
	}
	if len(sp.Patrol) > 0 {
		return &Navigator{
			patrol:       append([]vec.V(nil), sp.Patrol...),
			wpIdx:        0,
			walkSpeed:    patrolWalkSpeed(sp.Speed),
			targetRadius: targetRadius,
			active:       true,
		}
	}
	if sp.Goal != nil {
		g := *sp.Goal
		return &Navigator{
			goal:         &g,
			walkSpeed:    patrolWalkSpeed(sp.Speed),
			targetRadius: targetRadius,
			active:       true,
		}
	}
	return nil
}

func patrolWalkSpeed(speed float64) float64 {
	if speed >= 0.05 {
		return speed
	}
	return navDefaultWalkSpd
}

// InitialHeading returns the heading (degrees) toward the first navigation target.
func (n *Navigator) InitialHeading(from vec.V) float64 {
	if n == nil {
		return 0
	}
	if n.goal != nil {
		dx, dz := n.goal.X-from.X, n.goal.Z-from.Z
		if math.Hypot(dx, dz) < 1e-4 {
			return 0
		}
		return navHeadingFromDelta(dx, dz)
	}
	if len(n.patrol) == 0 {
		return 0
	}
	wp := n.patrol[n.wpIdx]
	dx, dz := wp.X-from.X, wp.Z-from.Z
	if math.Hypot(dx, dz) < 1e-4 {
		return 0
	}
	return navHeadingFromDelta(dx, dz)
}

// Tick updates locomotor heading and speed from the current path segment.
func (n *Navigator) Tick(a *Agent, sc *scene.Scene, peers []*Agent, dt float64) {
	if n == nil || !n.active || a == nil || sc == nil || a.Driver == nil {
		return
	}

	n.pollPath()

	hip := a.Driver.HipPos()
	wp := n.currentWaypoint()
	nav := a.Rig.Navigation
	if n.atTarget(hip, a.Driver.Heading(), nav) {
		if n.goal != nil {
			a.Driver.SetSpeed(0)
			n.active = false
			return
		}
		n.wpIdx = (n.wpIdx + 1) % len(n.patrol)
		n.path = nil
		n.pathIdx = 0
		n.replanCD = 0
		wp = n.currentWaypoint()
	}

	if n.replanCD > 0 {
		n.replanCD = math.Max(0, n.replanCD-dt)
	}
	if len(n.path) == 0 || n.pathIdx >= len(n.path) {
		if n.replanCD <= 0 && !n.pathBusy {
			n.startReplan(sc, hip, wp, a.Driver.GroundY(), a.Rig.Navigation)
			n.replanCD = navReplanInterval
		}
	}
	if len(n.path) == 0 {
		n.steerToward(a, hip, wp, wp, dt, nav, peers)
		return
	}

	arrival := n.arrivalRadius(nav)
	steer := wp
	if n.pathIdx < len(n.path) {
		steer = n.path[n.pathIdx]
		if horizDist(hip, steer) < navPathArriveDist {
			n.pathIdx++
			if n.pathIdx < len(n.path) {
				steer = n.path[n.pathIdx]
			} else {
				steer = wp
			}
		}
	}
	// Close to the waypoint: steer straight in and ease speed to avoid orbiting.
	if horizDist(hip, wp) < arrival*2.5 {
		steer = wp
	}

	n.steerToward(a, hip, steer, wp, dt, nav, peers)
}

func (n *Navigator) steerToward(a *Agent, hip, target, goal vec.V, dt float64, nav character.NavigationParams, peers []*Agent) {
	dx, dz := target.X-hip.X, target.Z-hip.Z
	if math.Hypot(dx, dz) > 1e-4 {
		desired := navHeadingFromDelta(dx, dz)
		desired = separationHeading(hip, desired, a, peers)
		a.Driver.SetHeading(lerpAngleDegrees(a.Driver.Heading(), desired, navHeadingTurn*dt))
	}
	speed := n.walkSpeed
	if arrival := n.arrivalRadius(nav); horizDist(hip, goal) < arrival*3 {
		dist := horizDist(hip, goal)
		scale := dist / (arrival * 3)
		if scale < 0.35 {
			scale = 0.35
		}
		speed = n.walkSpeed * scale
	}
	speed *= separationSpeedScale(hip, a, peers)
	a.Driver.SetSpeed(speed)
}

func (n *Navigator) pollPath() {
	if n.pathReady == nil {
		return
	}
	select {
	case res := <-n.pathReady:
		n.pathBusy = false
		if res.id != n.pathReqID {
			return
		}
		n.applyPath(res.path, res.from, res.to)
	default:
	}
}

func (n *Navigator) applyPath(path []vec.V, from, to vec.V) {
	path = trimPathAhead(from, to, path)
	n.path = path
	n.pathIdx = 0
	if len(n.path) > 0 && from != (vec.V{}) && horizDist(from, n.path[0]) < navPathArriveDist {
		n.pathIdx = 1
	}
}

// trimPathAhead drops grid corners that lie behind the travel direction from start.
func trimPathAhead(from, to vec.V, path []vec.V) []vec.V {
	if len(path) == 0 {
		return path
	}
	fwdX, fwdZ := to.X-from.X, to.Z-from.Z
	if math.Hypot(fwdX, fwdZ) < 1e-6 {
		return path
	}
	i := 0
	for i < len(path) && pathDotAhead(from, path[i], fwdX, fwdZ) < -0.05 {
		i++
	}
	if i >= len(path) {
		return path[len(path)-1:]
	}
	return path[i:]
}

func pathDotAhead(from, pt vec.V, fwdX, fwdZ float64) float64 {
	return (pt.X-from.X)*fwdX + (pt.Z-from.Z)*fwdZ
}

func (n *Navigator) ensurePathReady() {
	if n.pathReady == nil {
		n.pathReady = make(chan navPathResult, 1)
	}
}

func (n *Navigator) startReplan(sc *scene.Scene, from, to vec.V, fromGroundY float64, nav character.NavigationParams) {
	n.ensurePathReady()
	select {
	case <-n.pathReady:
	default:
	}
	n.pathReqID++
	id := n.pathReqID
	n.pathBusy = true
	if n.pathCache == nil {
		n.pathCache = make(map[int][]vec.V)
	}
	usePatrolLeg := len(n.patrol) > 0 && horizDist(from, n.patrol[n.wpIdx]) < n.targetRadius*2
	patrolFrom, patrolTo := n.wpIdx, 0
	if len(n.patrol) > 0 {
		patrolTo = (n.wpIdx + 1) % len(n.patrol)
	}
	cache := n.pathCache
	fromPos := from
	go func() {
		var path []vec.V
		if usePatrolLeg {
			path = findPatrolPath(sc, n.patrol, patrolFrom, patrolTo, fromGroundY, nav, cache)
		} else {
			path = FindPath(sc, fromPos, to, fromGroundY, nav)
		}
		n.pathReady <- navPathResult{id: id, from: fromPos, to: to, path: path}
	}()
}

func findPatrolPath(sc *scene.Scene, patrol []vec.V, fromIdx, toIdx int, fromGroundY float64, nav character.NavigationParams, cache map[int][]vec.V) []vec.V {
	key := fromIdx*1000 + toIdx
	if cache != nil {
		if p, ok := cache[key]; ok {
			return append([]vec.V(nil), p...)
		}
	}
	from := patrol[fromIdx]
	to := patrol[toIdx]
	gy := fromGroundY
	if gy <= 1e-6 {
		gy = from.Y
	}
	p := FindPath(sc, from, to, gy, nav)
	if cache != nil {
		cache[key] = append([]vec.V(nil), p...)
	}
	return p
}

func (n *Navigator) currentWaypoint() vec.V {
	if n.goal != nil {
		return *n.goal
	}
	return n.patrol[n.wpIdx]
}

func (n *Navigator) arrivalRadius(nav character.NavigationParams) float64 {
	if n == nil {
		return navDefaultTargetRadius
	}
	r := n.targetRadius
	if nav.ClearanceRadius > 0 {
		r = math.Max(r, nav.ClearanceRadius*navArrivalClearanceScale)
	} else if nav.Radius > 0 {
		r = math.Max(r, nav.Radius*0.85)
	}
	if n.walkSpeed > 1 {
		r = math.Max(r, n.walkSpeed*navArrivalSpeedScale)
	}
	return r
}

func (n *Navigator) atTarget(hip vec.V, heading float64, nav character.NavigationParams) bool {
	if n == nil {
		return false
	}
	wp := n.currentWaypoint()
	d := horizDist(hip, wp)
	arrival := n.arrivalRadius(nav)
	if d < arrival {
		return true
	}
	// Overshoot forgiveness: passed the waypoint while still nearby.
	if d < arrival*navArrivalOvershootScale {
		toWP := vec.V{X: wp.X - hip.X, Z: wp.Z - hip.Z}
		if toWP.LenSq() > 1e-12 {
			fwd := navYawForward(heading)
			toWP = toWP.Normalize()
			if toWP.X*fwd.X+toWP.Z*fwd.Z < -0.15 {
				return true
			}
		}
	}
	return false
}

func navYawForward(heading float64) vec.V {
	rad := heading * math.Pi / 180
	return vec.V{X: math.Sin(rad), Z: -math.Cos(rad)}
}

// blendAngleDegrees interpolates from→to by t in [0,1].
func blendAngleDegrees(from, to, t float64) float64 {
	if t <= 0 {
		return from
	}
	if t >= 1 {
		return to
	}
	delta := math.Mod(to-from+540, 360) - 180
	return from + delta*t
}

func (n *Navigator) replan(sc *scene.Scene, from, to vec.V, fromGroundY float64, nav character.NavigationParams) {
	n.applyPath(FindPath(sc, from, to, fromGroundY, nav), from, to)
}

// FindPath returns a coarse XZ polyline from start to goal over static geometry.
// fromGroundY and goal.Y are optional feet-height hints (0 = auto) that cap the
// ground probe so multi-level scenes pick the intended floor.
func FindPath(sc *scene.Scene, start, goal vec.V, fromGroundY float64, nav character.NavigationParams) []vec.V {
	if nav.Height <= 0 {
		nav = character.DefaultNavigationParams()
	}
	if sc == nil {
		return nil
	}
	grid := buildNavGrid(sc, start, goal, fromGroundY, nav)
	if grid.cols == 0 || grid.rows == 0 {
		return nil
	}
	fromCol, fromRow, okFrom := grid.cell(start.X, start.Z)
	toCol, toRow, okTo := grid.cell(goal.X, goal.Z)
	if !okFrom || !okTo {
		return nil
	}
	fromCol, fromRow, okFrom = grid.nearestWalkable(fromCol, fromRow, fromGroundY)
	toCol, toRow, okTo = grid.nearestWalkable(toCol, toRow, goal.Y)
	if !okFrom || !okTo {
		return nil
	}
	raw := grid.astar(fromCol, fromRow, toCol, toRow)
	if len(raw) == 0 {
		return nil
	}
	out := make([]vec.V, len(raw))
	for i, c := range raw {
		out[i] = grid.worldCenter(c.col, c.row)
	}
	return out
}

type navGrid struct {
	originX, originZ float64
	cols, rows       int
	cellSize         float64
	walkable         []bool
	heights          []float64
	sc               *scene.Scene
	nav              character.NavigationParams
}

func buildNavGrid(sc *scene.Scene, from, to vec.V, fromGroundY float64, nav character.NavigationParams) navGrid {
	minX, minZ, maxX, maxZ := navBounds(from, to, navBoundsMargin, fromGroundY, to.Y)
	cell := navCellSize
	cols := int(math.Ceil((maxX-minX)/cell)) + 1
	rows := int(math.Ceil((maxZ-minZ)/cell)) + 1
	if cols*rows > navMaxGridCells {
		cell = navCellSizeForBounds(minX, maxX, minZ, maxZ)
		cols = int(math.Ceil((maxX-minX)/cell)) + 1
		rows = int(math.Ceil((maxZ-minZ)/cell)) + 1
	}
	if cols < 1 || rows < 1 {
		return navGrid{}
	}
	probeHeadY := navProbeHeadY(fromGroundY, to.Y, nav)
	levelHintY := to.Y
	if levelHintY <= 1e-6 {
		levelHintY = fromGroundY
	}
	g := navGrid{
		originX:  minX,
		originZ:  minZ,
		cols:     cols,
		rows:     rows,
		cellSize: cell,
		walkable: make([]bool, cols*rows),
		heights:  make([]float64, cols*rows),
		sc:       sc,
		nav:      nav,
	}
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			x, z := g.worldCenter(col, row).X, g.worldCenter(col, row).Z
			ok, feetY := navCellWalkable(sc, x, z, probeHeadY, levelHintY, nav)
			idx := g.index(col, row)
			g.walkable[idx] = ok
			g.heights[idx] = feetY
		}
	}
	return g
}

func navCellSizeForBounds(minX, maxX, minZ, maxZ float64) float64 {
	spanX := maxX - minX
	spanZ := maxZ - minZ
	if spanX < navCellSize {
		spanX = navCellSize
	}
	if spanZ < navCellSize {
		spanZ = navCellSize
	}
	area := spanX * spanZ
	if area <= 0 {
		return navCellSize
	}
	coarse := math.Sqrt(area / navMaxGridCells)
	if coarse < navCellSize {
		return navCellSize
	}
	return coarse
}

func navProbeHeadY(fromGroundY, toYHint float64, nav character.NavigationParams) float64 {
	headY := navGroundProbeY
	for _, feetY := range []float64{fromGroundY, toYHint} {
		if feetY > 1e-6 {
			hy := feetY + nav.Height + 0.5
			if hy < headY {
				headY = hy
			}
		}
	}
	return headY
}

func navBounds(from, to vec.V, margin, fromY, toY float64) (minX, minZ, maxX, maxZ float64) {
	m := margin
	if fromY > 1e-6 || toY > 1e-6 {
		span := math.Hypot(to.X-from.X, to.Z-from.Z)
		m += math.Min(6.0, span*0.35)
		if m < 3.0 {
			m = 3.0
		}
	}
	minX = math.Min(from.X, to.X) - m
	maxX = math.Max(from.X, to.X) + m
	minZ = math.Min(from.Z, to.Z) - m
	maxZ = math.Max(from.Z, to.Z) + m
	return
}

func navCellWalkable(sc *scene.Scene, x, z, probeHeadY, levelHintY float64, nav character.NavigationParams) (bool, float64) {
	feetY := sc.GroundHeightStatic(x, z, probeHeadY)
	if levelHintY > 1e-6 && feetY > levelHintY+navLevelTolerance {
		return false, feetY
	}
	clearRadius := nav.Radius
	if levelHintY > 1e-6 {
		clearRadius = nav.ClearanceRadius
		clearHeadY := levelHintY + nav.Height
		if sc.BlockedStatic(x, z, levelHintY, clearHeadY, clearRadius, nav.MaxStepUp) {
			return false, feetY
		}
	}
	headY := feetY + nav.Height
	if sc.BlockedStatic(x, z, feetY, headY, clearRadius, nav.MaxStepUp) {
		return false, feetY
	}
	return true, feetY
}

func (g navGrid) index(col, row int) int { return row*g.cols + col }

func (g navGrid) inBounds(col, row int) bool {
	return col >= 0 && col < g.cols && row >= 0 && row < g.rows
}

func (g navGrid) cell(x, z float64) (col, row int, ok bool) {
	col = int(math.Floor((x - g.originX) / g.cellSize))
	row = int(math.Floor((z - g.originZ) / g.cellSize))
	if !g.inBounds(col, row) {
		return 0, 0, false
	}
	return col, row, true
}

func (g navGrid) nearestWalkable(col, row int, yHint float64) (int, int, bool) {
	if !g.inBounds(col, row) {
		return 0, 0, false
	}
	if g.walkable[g.index(col, row)] {
		return col, row, true
	}
	maxR := g.cols
	if g.rows > maxR {
		maxR = g.rows
	}
	bestCol, bestRow := 0, 0
	bestScore := math.Inf(1)
	found := false
	for r := 1; r <= maxR; r++ {
		for rr := row - r; rr <= row+r; rr++ {
			for cc := col - r; cc <= col+r; cc++ {
				if !g.inBounds(cc, rr) || !g.walkable[g.index(cc, rr)] {
					continue
				}
				dr := float64(rr - row)
				dc := float64(cc - col)
				score := dc*dc + dr*dr
				if yHint > 1e-6 {
					dh := math.Abs(g.heights[g.index(cc, rr)] - yHint)
					score += dh * 4
				}
				if score < bestScore {
					bestScore = score
					bestCol, bestRow = cc, rr
					found = true
				}
			}
		}
		if found {
			return bestCol, bestRow, true
		}
	}
	return 0, 0, false
}

func (g navGrid) worldCenter(col, row int) vec.V {
	return vec.V{
		X: g.originX + (float64(col)+0.5)*g.cellSize,
		Z: g.originZ + (float64(row)+0.5)*g.cellSize,
	}
}

func (g navGrid) canStep(fromCol, fromRow, toCol, toRow int) bool {
	if !g.inBounds(toCol, toRow) || !g.walkable[g.index(toCol, toRow)] {
		return false
	}
	if !g.walkable[g.index(fromCol, fromRow)] {
		return false
	}
	dh := g.heights[g.index(toCol, toRow)] - g.heights[g.index(fromCol, fromRow)]
	if dh > g.nav.MaxStepUp+1e-6 {
		return false
	}
	if dh < -navMaxStepDown-1e-6 {
		return false
	}
	return true
}

type navNode struct {
	col, row int
	g, f     float64
	parent   *navNode
	index    int
}

type navOpen []*navNode

func (h navOpen) Len() int           { return len(h) }
func (h navOpen) Less(i, j int) bool { return h[i].f < h[j].f }
func (h navOpen) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *navOpen) Push(x any) {
	n := x.(*navNode)
	n.index = len(*h)
	*h = append(*h, n)
}
func (h *navOpen) Pop() any {
	old := *h
	n := len(old)
	node := old[n-1]
	old[n-1] = nil
	node.index = -1
	*h = old[:n-1]
	return node
}

var navNeighbors = [8][2]int{
	{1, 0}, {-1, 0}, {0, 1}, {0, -1},
	{1, 1}, {1, -1}, {-1, 1}, {-1, -1},
}

func (g navGrid) astar(fromCol, fromRow, toCol, toRow int) []navCell {
	if !g.walkable[g.index(fromCol, fromRow)] || !g.walkable[g.index(toCol, toRow)] {
		return nil
	}
	start := &navNode{col: fromCol, row: fromRow, g: 0, f: g.heuristic(fromCol, fromRow, toCol, toRow)}
	open := navOpen{start}
	heap.Init(&open)
	closed := make(map[[2]int]bool)
	best := make(map[[2]int]*navNode)
	best[[2]int{fromCol, fromRow}] = start

	for open.Len() > 0 {
		cur := heap.Pop(&open).(*navNode)
		key := [2]int{cur.col, cur.row}
		if closed[key] {
			continue
		}
		closed[key] = true
		if cur.col == toCol && cur.row == toRow {
			return reconstructNavPath(cur)
		}
		for _, d := range navNeighbors {
			nc, nr := cur.col+d[0], cur.row+d[1]
			if !g.canStep(cur.col, cur.row, nc, nr) {
				continue
			}
			stepCost := 1.0
			if d[0] != 0 && d[1] != 0 {
				stepCost = math.Sqrt2
			}
			ng := cur.g + stepCost
			nkey := [2]int{nc, nr}
			if prev, ok := best[nkey]; ok && ng >= prev.g {
				continue
			}
			node := &navNode{
				col: nc, row: nr, g: ng,
				f: ng + g.heuristic(nc, nr, toCol, toRow),
				parent: cur,
			}
			best[nkey] = node
			heap.Push(&open, node)
		}
	}
	return nil
}

func (g navGrid) heuristic(col, row, toCol, toRow int) float64 {
	dx := float64(toCol - col)
	dz := float64(toRow - row)
	return math.Hypot(dx, dz)
}

type navCell struct {
	col, row int
}

func reconstructNavPath(end *navNode) []navCell {
	var cells []navCell
	for n := end; n != nil; n = n.parent {
		cells = append(cells, navCell{col: n.col, row: n.row})
	}
	for i, j := 0, len(cells)-1; i < j; i, j = i+1, j-1 {
		cells[i], cells[j] = cells[j], cells[i]
	}
	return simplifyNavPath(cells)
}

func simplifyNavPath(cells []navCell) []navCell {
	if len(cells) <= 2 {
		return cells
	}
	out := []navCell{cells[0]}
	for i := 1; i < len(cells)-1; i++ {
		a, b, c := cells[i-1], cells[i], cells[i+1]
		d1x, d1z := float64(b.col-a.col), float64(b.row-a.row)
		d2x, d2z := float64(c.col-b.col), float64(c.row-b.row)
		if d1x*d2z-d1z*d2x == 0 {
			continue
		}
		out = append(out, b)
	}
	out = append(out, cells[len(cells)-1])
	return out
}

func navHeadingFromDelta(dx, dz float64) float64 {
	return math.Atan2(-dx, -dz) * 180 / math.Pi
}

func lerpAngleDegrees(from, to, maxDelta float64) float64 {
	delta := math.Mod(to-from+540, 360) - 180
	if delta > maxDelta {
		delta = maxDelta
	} else if delta < -maxDelta {
		delta = -maxDelta
	}
	return from + delta
}

func horizDist(a, b vec.V) float64 {
	dx, dz := a.X-b.X, a.Z-b.Z
	return math.Hypot(dx, dz)
}
