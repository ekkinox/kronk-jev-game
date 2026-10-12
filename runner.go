package main

// Action is what a runner is told to do, by a key or by the model.
type Action int

const (
	Run    Action = iota // keep running: do nothing
	Jump                 // jump, from the ground only, not on the step it lands
	Crouch               // crouch for CrouchSteps, or restart the crouch
	numActions
)

var actionNames = [numActions]string{"run", "jump", "crouch"}

func (a Action) String() string { return actionNames[a] }

// Pose is what the runner is doing on a step.
type Pose int

const (
	Running Pose = iota
	Jumping
	Crouching
	Sunk // fell into a hole: can't act until there is ground under it
)

var poseNames = [...]string{"running", "jumping", "crouching", "in a hole"}

func (p Pose) String() string { return poseNames[p] }

// An Outcome is an obstacle resolved for a lane: passed (+1) or failed (-1).
type Outcome struct {
	Obstacle Obstacle
	Passed   bool
}

// A Lane is one runner on the shared world, and its score. The zero Lane is a
// runner standing at world cell 0, ready to go. See rules.go for the step.
type Lane struct {
	X             int    // the world cell under the runner
	air           int    // step of the jump, 1-based into JumpArc; 0 on the ground
	crouch        int    // crouch steps left
	sunk          bool   // in a hole
	flash         int    // flash steps left
	popup         int    // popup steps left
	gain          int    // the last score change: +1 or -1
	pending       Action // the last input since the previous step
	pendingCrouch int    // how many steps the pending crouch lasts
	applied       Action // the action applied on the last step
	next          int    // index of the first obstacle this lane has not resolved

	Score          int
	Passed, Failed [numKinds]int
}

// Press records an input. It takes effect on the next step, if the runner can
// do it then. A runner in a hole ignores inputs, and Run is no input at all.
func (l *Lane) Press(a Action) { l.press(a, CrouchSteps) }

// PressCrouch records a crouch that lasts steps, at least CrouchSteps: the
// game's crouch key, which lasts KeyCrouchSteps.
func (l *Lane) PressCrouch(steps int) { l.press(Crouch, max(steps, CrouchSteps)) }

func (l *Lane) press(a Action, crouch int) {
	if a != Run && !l.sunk {
		l.pending, l.pendingCrouch = a, crouch
	}
}

// Step advances the lane by one step. It reports the obstacle it resolved, if
// any: at most one, since there are empty cells between two obstacles.
func (l *Lane) Step(w *World) (Outcome, bool) {
	grounded := l.air == 0 // before this step: a runner landing on it can't jump yet
	l.X++
	l.advance(w)
	l.apply(grounded)
	if out, ok := l.collide(w); ok {
		return out, true
	}
	return l.resolve(w)
}

func (l *Lane) advance(w *World) {
	if l.air > 0 {
		l.air++
		if l.air > len(JumpArc) {
			l.air = 0 // landed
		}
	}
	l.crouch = max(l.crouch-1, 0)
	l.flash = max(l.flash-1, 0)
	l.popup = max(l.popup-1, 0)
	if l.sunk && !w.HoleAt(l.X) {
		l.sunk = false
	}
}

func (l *Lane) apply(grounded bool) {
	a := l.pending
	l.pending, l.applied = Run, Run
	if l.sunk || l.air > 0 || (a == Jump && !grounded) {
		return
	}
	switch a {
	case Jump:
		l.crouch, l.air = 0, 1 // a crouched runner stands up and jumps
	case Crouch:
		l.crouch = l.pendingCrouch
	default:
		return
	}
	l.applied = a
}

// collide fails the next obstacle if the runner is over it and falls in or
// hits it this step. A failed obstacle is resolved: the runner passes through
// what is left of it.
func (l *Lane) collide(w *World) (Outcome, bool) {
	o, ok := w.Obstacle(l.next)
	if !ok || l.X < o.Start || l.X >= o.End() {
		return Outcome{}, false
	}
	switch o.Kind {
	case Hole:
		if l.Feet() > 0 {
			return Outcome{}, false
		}
		l.sunk, l.crouch = true, 0
	default:
		lo, hi := l.Rows()
		hit := false
		for h := lo; h <= hi; h++ {
			hit = hit || o.Blocks(h)
		}
		if !hit {
			return Outcome{}, false
		}
	}
	l.next++
	l.Score--
	l.Failed[o.Kind]++
	l.flash = FlashSteps
	l.popup, l.gain = PopupSteps, -1
	return Outcome{Obstacle: o}, true
}

// resolve scores the next obstacle once its last cell is behind the runner.
func (l *Lane) resolve(w *World) (Outcome, bool) {
	o, ok := w.Obstacle(l.next)
	if !ok || l.X < o.End() {
		return Outcome{}, false
	}
	l.next++
	l.Score++
	l.Passed[o.Kind]++
	l.popup, l.gain = PopupSteps, 1
	return Outcome{Obstacle: o, Passed: true}, true
}

// Feet is the height of the runner's feet: above 0 only in a jump.
func (l *Lane) Feet() int {
	if l.air > 0 {
		return JumpArc[l.air-1]
	}
	return 0
}

func (l *Lane) Pose() Pose {
	switch {
	case l.sunk:
		return Sunk
	case l.air > 0:
		return Jumping
	case l.crouch > 0:
		return Crouching
	}
	return Running
}

// Rows returns the lowest and highest rows the runner occupies. A runner in a
// hole is below the ground: it occupies no play row (lo > hi).
func (l *Lane) Rows() (lo, hi int) {
	switch l.Pose() {
	case Sunk:
		return 0, -1
	case Jumping:
		return l.Feet(), l.Feet() + 1
	case Crouching:
		return 0, 0
	}
	return 0, 1
}

// Next returns the first obstacle the lane has not resolved yet, and the
// distance from the runner to its first cell, 0 once the runner is over it.
func (l *Lane) Next(w *World) (Obstacle, int, bool) {
	o, ok := w.Obstacle(l.next)
	if !ok {
		return o, 0, false
	}
	return o, max(o.Start-l.X, 0), true
}

// Resolved reports whether the lane has passed or failed the i-th obstacle.
func (l *Lane) Resolved(i int) bool { return i < l.next }

// Flashing reports whether the runner shows its failure flash.
func (l *Lane) Flashing() bool { return l.flash > 0 }

// Popup returns the score change to show above the runner, and for how many
// steps it has shown.
func (l *Lane) Popup() (gain, age int, ok bool) {
	return l.gain, PopupSteps - l.popup, l.popup > 0
}

// Applied returns the action applied on the last step, Run if none.
func (l *Lane) Applied() Action { return l.applied }

// Right returns the right actions for a runner running on the ground at
// distance d of obstacle o, by playing them out on the engine:
//
//   - jump or crouch is right when that one action, given now and nothing
//     after it, clears o;
//   - run is right while waiting is safe: a jump or a crouch given on a later
//     step would still clear o.
//
// It returns nothing when o can't be cleared any more. At distance 0 the
// runner is already over o, so it has fallen in or hit it.
func Right(o Obstacle, d int) []Action {
	var right []Action
	for wait := 1; wait <= d; wait++ {
		if clears(o, d, wait, Jump) || clears(o, d, wait, Crouch) {
			right = append(right, Run)
			break
		}
	}
	for _, a := range []Action{Jump, Crouch} {
		if clears(o, d, 0, a) {
			right = append(right, a)
		}
	}
	return right
}

// clears plays a lane on a world holding only o: it runs to distance d of o,
// waits wait more steps, presses a, then does nothing. It reports whether o
// was passed. An input is pressed between two steps, so it applies on the
// step after the one it is given at, like a key in the game.
func clears(o Obstacle, d, wait int, a Action) bool {
	o.Start = d + 1 // the zero Lane is at cell 0: one step brings it to distance d
	w := FixedWorld(o)
	var l Lane
	for range 1 + wait {
		if out, ok := l.Step(w); ok {
			return out.Passed
		}
	}
	l.Press(a)
	for {
		if out, ok := l.Step(w); ok {
			return out.Passed
		}
	}
}
