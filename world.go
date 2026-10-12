package main

import (
	"fmt"
	"math/rand/v2"
	"sort"
)

// Kind is the kind of an obstacle.
type Kind int

const (
	Hole Kind = iota
	Rock
	Bird
	numKinds
)

var kindNames = [numKinds]string{"hole", "rock", "bird"}

func (k Kind) String() string { return kindNames[k] }

// An Obstacle spans the world cells [Start, Start+Width).
type Obstacle struct {
	Kind   Kind
	Start  int
	Width  int
	Height int // rocks only: a rock occupies the rows h < Height
}

func (o Obstacle) End() int { return o.Start + o.Width }

// Blocks reports whether the obstacle occupies row h. A hole occupies no row:
// it is ground that is missing.
func (o Obstacle) Blocks(h int) bool {
	switch o.Kind {
	case Rock:
		return h >= 0 && h < o.Height
	case Bird:
		return h >= BirdLow && h <= BirdHigh
	}
	return false
}

// String names the obstacle with its size: "hole 3", "rock 2x1", "bird".
func (o Obstacle) String() string {
	switch o.Kind {
	case Hole:
		return fmt.Sprintf("hole %d", o.Width)
	case Rock:
		return fmt.Sprintf("rock %dx%d", o.Width, o.Height)
	}
	return o.Kind.String()
}

// A World is the one map both lanes run on: obstacles in order of Start. It is
// generated lazily from a seed, as far as the runners get, so it is endless and
// the same seed gives the same map.
type World struct {
	obstacles []Obstacle
	rng       *rand.Rand // nil for a fixed world: nothing after its obstacles

	// Difficulty sets the gaps of the obstacles generated from now on. The
	// map is generated a screen ahead, so a change shows from the next
	// obstacle that comes into view.
	Difficulty Difficulty
}

func NewWorld(seed uint64) *World {
	return &World{rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))}
}

// FixedWorld is a world with only the given obstacles, sorted by Start. Tests
// and the ideal-move computation use it.
func FixedWorld(obstacles ...Obstacle) *World {
	return &World{obstacles: obstacles}
}

// Obstacle returns the i-th obstacle, generating the map up to it.
func (w *World) Obstacle(i int) (Obstacle, bool) {
	for len(w.obstacles) <= i && w.rng != nil {
		w.obstacles = append(w.obstacles, w.generate())
	}
	if i < len(w.obstacles) {
		return w.obstacles[i], true
	}
	return Obstacle{}, false
}

// generate makes the obstacle after the last one. Each obstacle draws its gap,
// kind and size in that order, so the map only depends on the seed and on
// when the difficulty changed.
func (w *World) generate() Obstacle {
	o := Obstacle{Start: StartGap}
	if n := len(w.obstacles); n > 0 {
		o.Start = w.obstacles[n-1].End() + w.between(w.Difficulty.Gaps())
	}
	switch r := w.rng.IntN(HolePercent + RockPercent + BirdPercent); {
	case r < HolePercent:
		o.Kind, o.Width = Hole, w.between(HoleMinWidth, HoleMaxWidth)
	case r < HolePercent+RockPercent:
		o.Kind, o.Width = Rock, w.between(RockMinWidth, RockMaxWidth)
		o.Height = w.between(RockMinHeight, RockMaxHeight)
	default:
		o.Kind, o.Width = Bird, BirdWidth
	}
	return o
}

func (w *World) between(lo, hi int) int {
	return lo + w.rng.IntN(hi-lo+1)
}

// Span returns the obstacles that overlap the cells [from, to).
func (w *World) Span(from, to int) []Obstacle {
	// Generate until an obstacle starts at or after to: then all the ones
	// before it are known.
	for w.rng != nil && (len(w.obstacles) == 0 || w.obstacles[len(w.obstacles)-1].Start < to) {
		w.obstacles = append(w.obstacles, w.generate())
	}
	i := sort.Search(len(w.obstacles), func(i int) bool { return w.obstacles[i].End() > from })
	j := i
	for j < len(w.obstacles) && w.obstacles[j].Start < to {
		j++
	}
	return w.obstacles[i:j]
}

// HoleAt reports whether the ground is missing at cell x.
func (w *World) HoleAt(x int) bool {
	for _, o := range w.Span(x, x+1) {
		if o.Kind == Hole {
			return true
		}
	}
	return false
}
