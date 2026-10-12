package main

import (
	"testing"
	"time"
)

func TestKeyCrouchSteps(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ speed, want int }{
		{MinSpeed, CrouchSteps}, // 700 ms is 3.5 steps: the model's crouch is longer
		{DefaultSpeed, 14},      // exactly 700 ms
		{25, 18},                // 17.5 steps, rounded up
		{MaxSpeed, 42},
	} {
		if got := KeyCrouchSteps(tc.speed); got != tc.want {
			t.Errorf("KeyCrouchSteps(%d) = %d, want %d", tc.speed, got, tc.want)
		}
	}
	for speed := MinSpeed; speed <= MaxSpeed; speed++ {
		held := time.Duration(KeyCrouchSteps(speed)) * time.Second / time.Duration(speed)
		if held < CrouchHold {
			t.Errorf("at %d cells/s, a key crouch lasts %s, want at least %s", speed, held, CrouchHold)
		}
	}
}

func TestDifficulty(t *testing.T) {
	t.Parallel()
	for d := range numDifficulties {
		got, ok := parseDifficulty(d.String())
		if !ok || got != d {
			t.Errorf("parseDifficulty(%q) = %v, %v, want %v", d.String(), got, ok, d)
		}
		if lo, hi := d.Gaps(); lo > hi {
			t.Errorf("%s: min gap %d above max gap %d", d, lo, hi)
		}
	}
	if _, ok := parseDifficulty("extreme"); ok {
		t.Error(`parseDifficulty("extreme") is ok, want an error`)
	}
	if got := Easy.Next().Next().Next(); got != Easy {
		t.Errorf("three presses of d from easy give %s, want easy", got)
	}
}

func TestObstacleMix(t *testing.T) {
	t.Parallel()
	if sum := HolePercent + RockPercent + BirdPercent; sum != 100 {
		t.Errorf("the obstacle mix sums to %d%%, want 100%%", sum)
	}
}

// TestSmallestGap checks the promise in rules.go: on every difficulty, the
// smallest gap leaves room to clear the biggest obstacle, land, and clear the
// next one. The runner acts as early as its action clears the obstacle.
func TestSmallestGap(t *testing.T) {
	t.Parallel()
	biggest := []Obstacle{
		{Kind: Hole, Width: HoleMaxWidth},
		{Kind: Rock, Width: RockMaxWidth, Height: RockMaxHeight},
		{Kind: Bird, Width: BirdWidth},
	}
	for d := range numDifficulties {
		gap, _ := d.Gaps()
		for _, first := range biggest {
			for _, second := range biggest {
				first.Start = 10
				second.Start = first.End() + gap
				w := FixedWorld(first, second)
				var l Lane
				for range second.End() {
					if o, dist, ok := l.Next(w); ok {
						for _, a := range Right(o, dist) {
							if a != Run {
								l.Press(a)
							}
						}
					}
					if out, ok := l.Step(w); ok && !out.Passed {
						t.Errorf("%s, gap %d: failed %s after %s", d, gap, out.Obstacle, first)
					}
				}
			}
		}
	}
}
