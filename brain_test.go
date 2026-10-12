package main

import (
	"context"
	"testing"
	"time"
)

// TestQuestion guards the question: one option per action, in Action order,
// each with a description (kronk rejects duplicate names).
func TestQuestion(t *testing.T) {
	if len(actionQuestion.Options) != int(numActions) {
		t.Fatalf("%d options, want %d", len(actionQuestion.Options), numActions)
	}
	for i, opt := range actionQuestion.Options {
		a, ok := parseAction(opt.Name)
		if !ok || a != Action(i) {
			t.Errorf("option %d is %q, want %q", i, opt.Name, Action(i))
		}
		if d, _ := opt.Description.(string); d == "" {
			t.Errorf("option %q has no description", opt.Name)
		}
	}
	if _, ok := parseAction("fly"); ok {
		t.Error(`parseAction("fly") succeeded`)
	}
}

// TestStateAt checks the state, projected lag cells ahead.
// TestPick checks the thresholds against make table answers: a sure jump or
// crouch is kept, an unsure one far from the obstacle becomes run.
func TestPick(t *testing.T) {
	for _, c := range []struct {
		choice string
		probs  map[string]float64
		want   Action
		p      float64
	}{
		{"jump", map[string]float64{"run": 0.04, "jump": 0.92, "crouch": 0.04}, Jump, 0.92},     // hole 2 at 3
		{"jump", map[string]float64{"run": 0.12, "jump": 0.83, "crouch": 0.05}, Run, 0.12},      // hole 2 at 20
		{"crouch", map[string]float64{"run": 0.20, "jump": 0.08, "crouch": 0.72}, Crouch, 0.72}, // bird at 3
		{"crouch", map[string]float64{"run": 0.30, "jump": 0.10, "crouch": 0.60}, Run, 0.30},    // bird at 20
		{"run", map[string]float64{"run": 0.52, "jump": 0.40, "crouch": 0.08}, Run, 0.52},
	} {
		a, p, ok := pick(c.choice, c.probs)
		if !ok || a != c.want || p != c.p {
			t.Errorf("pick(%q, %v) = %v %.2f %v, want %v %.2f", c.choice, c.probs, a, p, ok, c.want, c.p)
		}
	}
	if _, _, ok := pick("fly", nil); ok {
		t.Error("pick accepted an unknown action")
	}
}

func TestStateAt(t *testing.T) {
	w := FixedWorld(
		Obstacle{Kind: Rock, Start: 10, Width: 2, Height: 1},
		Obstacle{Kind: Bird, Start: 29, Width: BirdWidth},
	)
	var l Lane
	for _, tt := range []struct {
		lag  int
		want State
	}{
		{0, State{Running, "rock", 10, 0}},
		{3, State{Running, "rock", 7, 0}},
		{11, State{Running, "rock", 0, 0}},  // over its last cell
		{12, State{Running, "bird", 17, 1}}, // the rock is passed by then
		{40, State{Running, "none", 0, 2}},
	} {
		if got := StateAt(&l, w, tt.lag); got != tt.want {
			t.Errorf("lag %d: %+v, want %+v", tt.lag, got, tt.want)
		}
	}

	for l.X < 10 {
		l.Step(w) // runs into the rock: it fails, so the bird is next
	}
	if got, want := StateAt(&l, w, 0), (State{Running, "bird", 19, 1}); got != want {
		t.Errorf("after hitting the rock: %+v, want %+v", got, want)
	}

	for l.X < 27 {
		l.Step(w)
	}
	l.Press(Crouch)
	l.Step(w)
	l.Step(w)
	if got, want := StateAt(&l, w, 0), (State{Crouching, "bird", 0, 1}); got != want {
		t.Errorf("under the bird: %+v, want %+v", got, want)
	}
}

// fakeBrain is a Brain whose model calls go to the test: each call sends its
// state on asked and returns the decision the test sends on reply.
func fakeBrain(asked chan<- State, reply <-chan Decision) *Brain {
	b := &Brain{}
	b.ask = func(ctx context.Context, s State) (Decision, error) {
		select {
		case asked <- s:
		case <-ctx.Done():
			return Decision{}, ctx.Err()
		}
		select {
		case d := <-reply:
			return d, nil
		case <-ctx.Done():
			return Decision{}, ctx.Err()
		}
	}
	return b
}

// TestObserveProjects checks that the state is projected by speed x the
// moving average of the latency, seeded by the first call.
func TestObserveProjects(t *testing.T) {
	asked, reply := make(chan State, 1), make(chan Decision, 1)
	b := fakeBrain(asked, reply)
	w := FixedWorld(Obstacle{Kind: Rock, Start: 30, Width: 1, Height: 1})
	var l Lane

	b.Observe(&l, w, 20)
	if got := b.state.Distance; got != 30 {
		t.Errorf("before any call: distance %d, want 30", got)
	}
	for _, tt := range []struct {
		latency time.Duration
		recent  time.Duration
	}{
		{100 * time.Millisecond, 100 * time.Millisecond}, // the first call seeds it
		{200 * time.Millisecond, 120 * time.Millisecond},
		{20 * time.Millisecond, 100 * time.Millisecond},
	} {
		reply <- Decision{Latency: tt.latency}
		if _, err := b.Decide(context.Background(), b.state); err != nil {
			t.Fatal(err)
		}
		<-asked
		if b.recent != tt.recent {
			t.Errorf("after a call of %s: average %s, want %s", tt.latency, b.recent, tt.recent)
		}
	}
	for speed, want := range map[int]int{20: 28, 60: 24, 5: 29} { // lag 2, 6, 0.5 rounded up
		b.Observe(&l, w, speed)
		if got := b.state.Distance; got != want {
			t.Errorf("100 ms at %d cells/s: distance %d, want %d", speed, got, want)
		}
	}
}

// TestPlayWaitsOnGround checks that the model is not asked while the runner
// is in the air or in a hole, nor after a jump until the step that applies
// it, nor while paused, and that it is asked right away once it can be. The
// test steps the lane only while the model is inside a call or waiting, so
// what it reads next is known.
func TestPlayWaitsOnGround(t *testing.T) {
	asked, reply := make(chan State), make(chan Decision)
	b := fakeBrain(asked, reply)
	answers := make(chan Decision)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := FixedWorld(Obstacle{Kind: Hole, Start: 30, Width: 4})
	var l Lane
	b.Observe(&l, w, 20)
	go b.Play(ctx, func(d Decision) { answers <- d })

	call := func(what string) State {
		t.Helper()
		select {
		case s := <-asked:
			return s
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: the model is not asked", what)
		}
		return State{}
	}
	noCall := func(what string) {
		t.Helper()
		select {
		case s := <-asked:
			t.Fatalf("%s: the model is asked about %+v", what, s)
		case <-time.After(20 * time.Millisecond):
		}
	}
	answer := func(a Action) {
		reply <- Decision{Action: a}
		b.Deliver(&l, <-answers)
	}
	step := func() {
		l.Step(w)
		b.Observe(&l, w, 20)
	}

	// It answers jump: no call until the step that applies it, then none in
	// the air.
	call("on the ground")
	answer(Jump)
	noCall("before the jump applies")
	for step(); l.Pose() == Jumping; step() {
		noCall("in the air")
	}
	if s := call("after landing"); s.Runner != Running {
		t.Errorf("asked about a runner %s, want running", s.Runner)
	}

	// It runs into the hole during that call: no call while in it.
	for l.Pose() != Sunk {
		step()
	}
	answer(Run)
	for ; l.Pose() == Sunk; step() {
		noCall("in a hole")
	}
	if s := call("out of the hole"); s.Runner != Running {
		t.Errorf("asked about a runner %s, want running", s.Runner)
	}

	// Paused during a call: no call until it resumes.
	b.Pause(true)
	answer(Run)
	noCall("paused")
	b.Pause(false)
	call("resumed")
	answer(Run)
}

// TestDeliverDropsStale checks that an answer about an obstacle the lane has
// resolved since the call started is dropped and counted.
func TestDeliverDropsStale(t *testing.T) {
	w := FixedWorld(
		Obstacle{Kind: Rock, Start: 5, Width: 1, Height: 1},
		Obstacle{Kind: Bird, Start: 30, Width: BirdWidth},
	)
	var l Lane
	b := &Brain{}
	b.Observe(&l, w, 20)
	stale := Decision{Action: Jump, about: b.state.obstacle}
	for l.X < 5 {
		l.Step(w) // runs into the rock while the model thinks
	}
	if b.Deliver(&l, stale) || l.pending != Run {
		t.Error("a jump about a failed rock was pressed")
	}

	b.Observe(&l, w, 20)
	fresh := Decision{Action: Crouch, about: b.state.obstacle}
	if !b.Deliver(&l, fresh) || l.pending != Crouch {
		t.Error("a crouch about the next bird was dropped")
	}
	if got := b.Stats(false).Dropped; got != 1 {
		t.Errorf("%d answers dropped, want 1", got)
	}
}
