package main

import (
	"math"
	"slices"
	"testing"
)

// TestWorldSeed checks that a seed always gives the same map, however far it
// is generated at a time, and that two seeds give two maps.
func TestWorldSeed(t *testing.T) {
	a, b, c := NewWorld(42), NewWorld(42), NewWorld(43)
	b.Span(0, 5000) // generate b ahead, a one at a time
	same := true
	for i := range 200 {
		oa, _ := a.Obstacle(i)
		ob, _ := b.Obstacle(i)
		oc, _ := c.Obstacle(i)
		if oa != ob {
			t.Fatalf("obstacle %d: %+v and %+v from the same seed", i, oa, ob)
		}
		same = same && oa == oc
	}
	if same {
		t.Error("seeds 42 and 43 give the same map")
	}
}

// TestWorldLayout checks the start, the gaps, the sizes and the mix, on each
// difficulty.
func TestWorldLayout(t *testing.T) {
	for d := range numDifficulties {
		t.Run(d.String(), func(t *testing.T) { testLayout(t, d) })
	}
}

func testLayout(t *testing.T, d Difficulty) {
	const n = 5000
	w := NewWorld(1)
	w.Difficulty = d
	lo, hi := d.Gaps()
	var count [numKinds]int
	prev, _ := w.Obstacle(0)
	if prev.Start != StartGap {
		t.Errorf("first obstacle at %d, want %d", prev.Start, StartGap)
	}
	for i := range n {
		o, _ := w.Obstacle(i)
		count[o.Kind]++
		if i > 0 {
			if gap := o.Start - prev.End(); gap < lo || gap > hi {
				t.Fatalf("obstacle %d: gap %d, want %d to %d", i, gap, lo, hi)
			}
		}
		prev = o
		var ok bool
		switch o.Kind {
		case Hole:
			ok = o.Width >= HoleMinWidth && o.Width <= HoleMaxWidth
		case Rock:
			ok = o.Width >= RockMinWidth && o.Width <= RockMaxWidth && o.Height >= RockMinHeight && o.Height <= RockMaxHeight
		case Bird:
			ok = o.Width == BirdWidth
		}
		if !ok {
			t.Fatalf("obstacle %d: bad size %+v", i, o)
		}
	}
	for k, want := range [numKinds]int{HolePercent, RockPercent, BirdPercent} {
		if got := 100 * float64(count[k]) / n; math.Abs(got-float64(want)) > 3 {
			t.Errorf("%s: %.1f%% of obstacles, want about %d%%", Kind(k), got, want)
		}
	}
}

// TestDifficultyChange checks that a harder difficulty brings obstacles closer
// together, from the next obstacle generated: the ones already there stay.
func TestDifficultyChange(t *testing.T) {
	w := NewWorld(1)
	before := w.Span(0, 200)
	w.Difficulty = Hard
	if again := w.Span(0, 200); !slices.Equal(before, again) {
		t.Fatalf("the map already generated changed: %v, then %v", before, again)
	}
	last := len(w.obstacles) - 1
	lo, hi := Hard.Gaps()
	for i := last + 1; i < last+200; i++ {
		o, _ := w.Obstacle(i)
		prev, _ := w.Obstacle(i - 1)
		if gap := o.Start - prev.End(); gap < lo || gap > hi {
			t.Fatalf("obstacle %d after the change: gap %d, want %d to %d", i, gap, lo, hi)
		}
	}
	for d := Easy; d < Hard; d++ {
		lo, hi := d.Gaps()
		if nlo, nhi := d.Next().Gaps(); nlo+nhi >= lo+hi {
			t.Errorf("%s does not bring obstacles closer than %s", d.Next(), d)
		}
	}
}

func TestSpanAndHoles(t *testing.T) {
	w := FixedWorld(
		Obstacle{Kind: Rock, Start: 10, Width: 2, Height: 1},
		Obstacle{Kind: Hole, Start: 30, Width: 3},
	)
	if got := w.Span(0, 10); len(got) != 0 {
		t.Errorf("Span(0, 10) = %v, want none", got)
	}
	if got := w.Span(11, 31); len(got) != 2 {
		t.Errorf("Span(11, 31) = %v, want both", got)
	}
	for x, want := range map[int]bool{29: false, 30: true, 32: true, 33: false, 10: false} {
		if got := w.HoleAt(x); got != want {
			t.Errorf("HoleAt(%d) = %v, want %v", x, got, want)
		}
	}
}
