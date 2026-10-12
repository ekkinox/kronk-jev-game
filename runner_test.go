package main

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

// window returns the distances, 0 to 20, at which action a given now (with
// nothing after it) clears o.
func window(o Obstacle, a Action) []int {
	var ds []int
	for d := 0; d <= 20; d++ {
		if clears(o, d, 0, a) {
			ds = append(ds, d)
		}
	}
	return ds
}

func span(lo, hi int) []int {
	var ds []int
	for d := lo; d <= hi; d++ {
		ds = append(ds, d)
	}
	return ds
}

// TestJumpWindows checks the engine against the jump arc worked out by hand.
// Asked at distance d, a jump applies on the next step, so the obstacle's
// cells meet the arc at steps d-1 to d+w-2. The feet must be at 1 or more over
// a hole or a rock 1 high (steps 0 to 7): d from 1 to 9-w. They must be at 2
// over a rock 2 high (steps 1 to 6): d from 2 to 8-w.
func TestJumpWindows(t *testing.T) {
	tests := []struct {
		o    Obstacle
		want []int
	}{
		{Obstacle{Kind: Hole, Width: 2}, span(1, 7)},
		{Obstacle{Kind: Hole, Width: 3}, span(1, 6)},
		{Obstacle{Kind: Hole, Width: 4}, span(1, 5)},
		{Obstacle{Kind: Rock, Width: 1, Height: 1}, span(1, 8)},
		{Obstacle{Kind: Rock, Width: 2, Height: 1}, span(1, 7)},
		{Obstacle{Kind: Rock, Width: 1, Height: 2}, span(2, 7)},
		{Obstacle{Kind: Rock, Width: 2, Height: 2}, span(2, 6)},
	}
	for _, tt := range tests {
		if got := window(tt.o, Jump); !slices.Equal(got, tt.want) {
			t.Errorf("%s: jump clears at %v, want %v", tt.o, got, tt.want)
		}
		if got := window(tt.o, Crouch); len(got) > 0 {
			t.Errorf("%s: crouch clears at %v, want never", tt.o, got)
		}
	}
}

// TestBirdNeedsCrouch checks that a jump never clears a bird, whenever it
// starts, that a crouch clears it from d=1 to 6 (the 8 crouched steps cover
// its 3 cells), and that standing never does.
func TestBirdNeedsCrouch(t *testing.T) {
	bird := Obstacle{Kind: Bird, Width: BirdWidth}
	for d := 0; d <= 20; d++ {
		for wait := 0; wait <= 20; wait++ {
			if clears(bird, d, wait, Jump) {
				t.Errorf("a jump at distance %d, after %d steps, clears a bird", d, wait)
			}
		}
		if clears(bird, d, 0, Run) {
			t.Errorf("standing at distance %d clears a bird", d)
		}
	}
	if got, want := window(bird, Crouch), span(1, 6); !slices.Equal(got, want) {
		t.Errorf("crouch clears a bird at %v, want %v", got, want)
	}
}

// TestRight checks the right actions -table compares the model with.
func TestRight(t *testing.T) {
	hole := Obstacle{Kind: Hole, Width: 2}
	rock := Obstacle{Kind: Rock, Width: 2, Height: 2}
	bird := Obstacle{Kind: Bird, Width: BirdWidth}
	tests := []struct {
		o    Obstacle
		d    int
		want []Action
	}{
		{hole, 0, nil}, // already in it
		{hole, 1, []Action{Jump}},
		{hole, 2, []Action{Run, Jump}},
		{hole, 7, []Action{Run, Jump}},
		{hole, 8, []Action{Run}}, // a jump now would land in it
		{rock, 1, nil},           // too late: the jump is not high enough yet
		{rock, 2, []Action{Jump}},
		{rock, 6, []Action{Run, Jump}},
		{rock, 7, []Action{Run}},
		{bird, 1, []Action{Crouch}},
		{bird, 6, []Action{Run, Crouch}},
		{bird, 7, []Action{Run}},
		{bird, 20, []Action{Run}},
	}
	for _, tt := range tests {
		if got := Right(tt.o, tt.d); !slices.Equal(got, tt.want) {
			t.Errorf("Right(%s, %d) = %v, want %v", tt.o, tt.d, got, tt.want)
		}
	}
}

// TestJumpArc checks the arc, that inputs in the air are dropped, and that a
// jump can't start on the landing step.
func TestJumpArc(t *testing.T) {
	w := FixedWorld()
	var l Lane
	l.Press(Jump)
	var feet []int
	for i := range len(JumpArc) + 1 {
		l.Step(w)
		feet = append(feet, l.Feet())
		switch {
		case i < len(JumpArc)-2:
			l.Press(Crouch) // applies on a step in the air: dropped
		case i == len(JumpArc)-1:
			l.Press(Jump) // applies on the landing step: dropped
		}
	}
	if want := append(JumpArc[:], 0); !slices.Equal(feet, want) {
		t.Errorf("feet = %v, want %v", feet, want)
	}
	if l.Pose() != Running {
		t.Errorf("after landing the pose is %s, want running: inputs applied in the air or on landing must be dropped", l.Pose())
	}
	l.Press(Jump)
	l.Step(w)
	if l.Feet() != JumpArc[0] {
		t.Errorf("feet %d a step after landing, want %d: it can jump again", l.Feet(), JumpArc[0])
	}
}

// TestHeldJump presses jump before every step, like a held key or a model
// that always jumps: the runner must touch the ground for one step between
// two jumps, so it can't stay in the air over a hole.
func TestHeldJump(t *testing.T) {
	var want []int
	for range 3 {
		want = append(append(want, JumpArc[:]...), 0)
	}
	w := FixedWorld()
	var l Lane
	var feet []int
	for range want {
		l.Press(Jump)
		l.Step(w)
		feet = append(feet, l.Feet())
	}
	if !slices.Equal(feet, want) {
		t.Errorf("feet = %v, want %v", feet, want)
	}

	// The runner lands every 9 steps, so it falls into a hole 9 wide.
	w = FixedWorld(Obstacle{Kind: Hole, Start: 5, Width: len(JumpArc) + 1})
	l = Lane{}
	for l.X < 20 {
		l.Press(Jump)
		l.Step(w)
	}
	if l.Failed[Hole] != 1 {
		t.Errorf("holding jump over a hole %d wide: failed %d, want 1", len(JumpArc)+1, l.Failed[Hole])
	}
}

// TestHeldCrouchKey holds the crouch key the way a keyboard repeats it: one
// press, then a repeat after the repeat delay, then one every 40 ms. The
// runner must stay crouched until the key is released, at every speed.
func TestHeldCrouchKey(t *testing.T) {
	const held = 2 * time.Second
	for _, delay := range []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, 600 * time.Millisecond, 660 * time.Millisecond} {
		for speed := MinSpeed; speed <= MaxSpeed; speed += SpeedStep {
			period := time.Second / time.Duration(speed)
			w := FixedWorld()
			var l Lane
			press := time.Duration(0) // the next key event
			for now := time.Duration(0); now < held; now += period {
				for ; press <= now; press = max(press+40*time.Millisecond, delay) {
					l.PressCrouch(KeyCrouchSteps(speed))
				}
				l.Step(w)
				if l.Pose() != Crouching {
					t.Errorf("repeat delay %s, %d cells/s: standing at %s while the key is held", delay, speed, now+period)
					break
				}
			}
		}
	}
	if got := KeyCrouchSteps(MinSpeed); got != CrouchSteps {
		t.Errorf("KeyCrouchSteps(%d) = %d, want %d: never shorter than the model's crouch", MinSpeed, got, CrouchSteps)
	}
}

// TestCrouch checks that a crouch lasts CrouchSteps, that another crouch
// restarts it, and that a jump stands the runner up.
func TestCrouch(t *testing.T) {
	w := FixedWorld()
	var l Lane
	l.Press(Crouch)
	for i := range CrouchSteps {
		l.Step(w)
		if l.Pose() != Crouching {
			t.Fatalf("step %d: pose %s, want crouching", i, l.Pose())
		}
		if i == 3 {
			l.Press(Crouch)
		}
	}
	for range 4 {
		l.Step(w) // the second crouch started on step 4, so it lasts until step 11
	}
	if l.Pose() != Crouching {
		t.Fatalf("pose %s, want crouching: the second crouch must restart the count", l.Pose())
	}
	l.Step(w)
	if l.Pose() != Running {
		t.Fatalf("pose %s, want running", l.Pose())
	}

	l.Press(Crouch)
	l.Step(w)
	l.Press(Jump)
	l.Step(w)
	if l.Pose() != Jumping {
		t.Errorf("pose %s, want jumping: a jump stands a crouched runner up", l.Pose())
	}
}

// TestFailure checks that hitting an obstacle gives -1 once, flashes, and lets
// the runner pass through the rest of it.
func TestFailure(t *testing.T) {
	w := FixedWorld(Obstacle{Kind: Rock, Start: 5, Width: 2, Height: 2})
	var l Lane
	var outs []Outcome
	for l.X < 20 {
		if out, ok := l.Step(w); ok {
			outs = append(outs, out)
			if l.X != 5 {
				t.Errorf("hit at cell %d, want 5", l.X)
			}
			if !l.Flashing() {
				t.Error("no flash after a hit")
			}
		}
	}
	if len(outs) != 1 || outs[0].Passed {
		t.Fatalf("outcomes %+v, want one failure", outs)
	}
	if l.Score != -1 || l.Failed[Rock] != 1 || l.Passed[Rock] != 0 {
		t.Errorf("score %d, rocks passed %d failed %d; want -1, 0, 1", l.Score, l.Passed[Rock], l.Failed[Rock])
	}
}

// TestHoleFall checks that a runner in a hole can't act until there is
// ground under it again.
func TestHoleFall(t *testing.T) {
	w := FixedWorld(Obstacle{Kind: Hole, Start: 5, Width: 4})
	var l Lane
	for l.X < 5 {
		l.Step(w)
	}
	if l.Pose() != Sunk || l.Score != -1 {
		t.Fatalf("at the hole: pose %s, score %d; want in a hole, -1", l.Pose(), l.Score)
	}
	for l.X < 8 {
		l.Press(Jump)
		l.Press(Crouch)
		l.Step(w)
		if l.Pose() != Sunk {
			t.Fatalf("cell %d: pose %s, want in a hole", l.X, l.Pose())
		}
	}
	l.Step(w) // cell 9: ground again
	if l.Pose() != Running {
		t.Fatalf("cell %d: pose %s, want running", l.X, l.Pose())
	}
	l.Press(Jump)
	l.Step(w)
	if l.Pose() != Jumping {
		t.Errorf("pose %s after a jump out of the hole, want jumping", l.Pose())
	}
	if l.Score != -1 || l.Failed[Hole] != 1 {
		t.Errorf("score %d, holes failed %d; want -1, 1", l.Score, l.Failed[Hole])
	}
}

// TestScoresOnce plays two lanes on one random map with random inputs: each
// obstacle must resolve exactly once per lane, in order, and the score must be
// passed minus failed.
func TestScoresOnce(t *testing.T) {
	w := NewWorld(7)
	rng := rand.New(rand.NewPCG(1, 2))
	lanes := []*Lane{{}, {}}
	resolved := make([][]int, len(lanes))
	for range 20_000 {
		for i, l := range lanes {
			if rng.IntN(6) == 0 {
				l.Press(Action(rng.IntN(int(numActions))))
			}
			if out, ok := l.Step(w); ok {
				resolved[i] = append(resolved[i], out.Obstacle.Start)
			}
		}
	}
	for i, l := range lanes {
		var behind []int // the obstacles whose last cell is behind the runner
		for j := 0; ; j++ {
			o, _ := w.Obstacle(j)
			if o.End() > l.X {
				break
			}
			behind = append(behind, o.Start)
		}
		// The obstacle under the runner may already have failed.
		if got := resolved[i]; !slices.Equal(got[:min(len(got), len(behind))], behind) || len(got) > len(behind)+1 {
			t.Errorf("lane %d resolved %d obstacles, want each of the %d behind it once, in order", i, len(got), len(behind))
		}
		if l.Score != sum(l.Passed)-sum(l.Failed) || sum(l.Passed)+sum(l.Failed) != len(resolved[i]) {
			t.Errorf("lane %d: score %d, passed %v, failed %v, %d resolved", i, l.Score, l.Passed, l.Failed, len(resolved[i]))
		}
	}
	if sum(lanes[0].Passed) == 0 || sum(lanes[0].Failed) == 0 {
		t.Errorf("random play passed %v and failed %v: want some of both", lanes[0].Passed, lanes[0].Failed)
	}
}
