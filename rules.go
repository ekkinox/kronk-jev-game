package main

import "time"

// The rules of the game. Every number that shapes play lives in this file, so
// tuning the game means editing one place.
//
// The world advances in discrete steps. Each step scrolls the map one cell to
// the left: the runner moves one cell to the right in the world. Both lanes
// share the world and step together. One step of a lane, in this order:
//
//  1. Scroll. The runner moves to the next world cell.
//  2. Advance timers. The jump moves to its next height (or lands), the crouch
//     counts down, the flash and the popup fade, and a runner in a hole stands
//     up if there is ground under it again.
//  3. Apply the pending action: the last one received since the previous step.
//     Crouch only if the runner is on the ground and not in a hole. Jump only
//     if it is, and was on the ground before step 2 too: a runner landing on
//     this step runs one step on the ground before it can jump again.
//     Otherwise the action is dropped. So an input takes effect on the next
//     step, and that step is the first one of the jump or the crouch.
//  4. Collide. If the runner is over the next obstacle, it falls into it or
//     hits it, depending on its rows that step. The obstacle fails: -1.
//  5. Resolve. If the next obstacle's last cell is now behind the runner and
//     it did not fail: +1.
//
// Heights are rows above the ground line: h=0 is the row right above it.
// Standing occupies h=0 and h=1, crouched h=0 only, airborne with feet at f
// occupies f and f+1.

// Speed is in steps, so cells, per second. One speed for the shared world.
const (
	DefaultSpeed = 20
	MinSpeed     = 5
	MaxSpeed     = 60
	SpeedStep    = 5
)

// The runner sits at a fixed screen column and is one column wide.
const RunnerColumn = 8

// JumpArc is the height of the runner's feet on each step of a jump, from the
// step the jump is applied. The step after the last one, it has landed (feet
// at 0), and it can't jump on that step. Go has no constant arrays, hence a
// var.
var JumpArc = [...]int{1, 2, 2, 2, 2, 2, 2, 1}

// A crouch lasts CrouchSteps from the last crouch command: terminals do not
// report key releases, so a crouch can't last "while the key is held".
//
// A held key only starts repeating after the keyboard's repeat delay, up to
// 660 ms by default (500 on GNOME, 600 on COSMIC): longer than CrouchSteps at
// 20 cells/s, 400 ms. So a crouch from a key lasts CrouchHold, or CrouchSteps
// if that is longer, and a held key keeps the runner crouched. The model's
// crouch lasts CrouchSteps.
const (
	CrouchSteps = 8
	CrouchHold  = 700 * time.Millisecond
)

// KeyCrouchSteps is how many steps a crouch from a key lasts at speed: at
// least CrouchHold, rounded up to a step.
func KeyCrouchSteps(speed int) int {
	return max(CrouchSteps, int((CrouchHold*time.Duration(speed)+time.Second-1)/time.Second))
}

// After a failure the runner flashes red for FlashSteps. A score change shows
// as a popup above the runner for PopupSteps.
const (
	FlashSteps = 10
	PopupSteps = 12
)

// The map: the first obstacle starts StartGap cells ahead of the runner. The
// difficulty sets the empty cells between two obstacles, so how often they
// come; the mix of kinds stays the same.
const StartGap = 40

// A Difficulty sets the gap between two obstacles. At 20 cells/s, an obstacle
// comes about every 1.5 s on easy, 1 s on medium and 0.8 s on hard. Even the
// smallest gap leaves room to land from one obstacle and jump the next.
type Difficulty int

const (
	Easy Difficulty = iota
	Medium
	Hard
	numDifficulties
)

var difficulties = [numDifficulties]struct {
	name           string
	minGap, maxGap int
}{
	{"easy", 14, 40},
	{"medium", 11, 26},
	{"hard", 8, 18},
}

func (d Difficulty) String() string { return difficulties[d].name }

// Gaps returns the fewest and the most empty cells between two obstacles.
func (d Difficulty) Gaps() (lo, hi int) { return difficulties[d].minGap, difficulties[d].maxGap }

// Next is the difficulty after d: easy, medium, hard, then easy again.
func (d Difficulty) Next() Difficulty { return (d + 1) % numDifficulties }

func parseDifficulty(name string) (Difficulty, bool) {
	for d := range numDifficulties {
		if d.String() == name {
			return d, true
		}
	}
	return 0, false
}

// Obstacles. A hole is missing ground; a runner with its feet at 0 over it
// falls in. A rock occupies the rows h < its height. A bird occupies BirdLow
// to BirdHigh, so jumping always hits it and only crouching passes under.
const (
	HoleMinWidth  = 2
	HoleMaxWidth  = 4
	RockMinWidth  = 1
	RockMaxWidth  = 2
	RockMinHeight = 1
	RockMaxHeight = 2
	BirdWidth     = 3
	BirdLow       = 1
	BirdHigh      = 2
)

// The mix of obstacles, in percent.
const (
	HolePercent = 35
	RockPercent = 40
	BirdPercent = 25
)
