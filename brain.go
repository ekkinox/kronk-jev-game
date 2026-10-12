package main

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/ardanlabs/kronk/sdk/kronk"
	"github.com/ardanlabs/kronk/sdk/kronk/model"
)

// How the model plays, in one place: what it is told (State), what it is asked
// (actionQuestion), and the loop that asks it (Brain.Play).

// State is what the model is told about its lane, the input of one call.
type State struct {
	Runner   Pose   // running or crouching: the model is not asked in the air or in a hole
	Next     string // the next obstacle's kind, never its size: hole | rock | bird | none
	Distance int    // cells from the runner to the next obstacle's first cell, 0 when over it
	obstacle int    // the next obstacle's index in the world: an answer about it is stale once the lane has resolved it
}

// StateAt describes a lane as it will be lag cells further, when the answer
// to a call made now arrives: the runner's pose now, and the next obstacle and
// its distance from cell X+lag. The next obstacle is the first one the lane
// has not resolved and that the runner has not fully passed at X+lag: a failed
// obstacle is behind it, even if the runner is still passing through it.
func StateAt(l *Lane, w *World, lag int) State {
	s := State{Runner: l.Pose(), Next: "none"}
	x := l.X + lag
	for s.obstacle = l.next; ; s.obstacle++ {
		o, ok := w.Obstacle(s.obstacle)
		if !ok {
			return s
		}
		if o.End() > x {
			s.Next, s.Distance = o.Kind.String(), max(o.Start-x, 0)
			return s
		}
	}
}

func (s State) decisionState() model.DecisionStateValue {
	return model.DecisionState(
		model.DecisionStateData("runner", s.Runner.String()),
		model.DecisionStateData("next_obstacle", s.Next),
		model.DecisionStateData("distance", s.Distance),
	)
}

// actionQuestion is the one question, one option per Action. Its wording is
// the best of four compared with make table (see the README's Results): Jev
// ignores "5 cells away or closer" and picks the action by obstacle kind
// alone; spelled out numbers make it look at the distance. Asked at distance
// d, a jump clears any hole or rock from d=2 to d=5 (a wider window for
// smaller ones), a crouch clears a bird from d=1 to d=6.
var actionQuestion = model.DecisionQuestionChoice("action", "What should the runner do now?",
	model.DecisionQuestionOpt(Run.String(), "the distance is 6, 7, 8, 9, 10 or more"),
	model.DecisionQuestionOpt(Jump.String(), "the next obstacle is a hole or a rock and the distance is 1, 2, 3, 4 or 5"),
	model.DecisionQuestionOpt(Crouch.String(), "the next obstacle is a bird and the distance is 1, 2, 3, 4 or 5"),
)

// Jev answers jump to a hole 11 to 20 cells away, but less sure than to one
// within reach: in make table, a wrong jump scores at most 0.83 and a right
// one at least 0.89; a wrong crouch 0.60, a right one at least 0.72. A move
// below its threshold is taken as run, so the runner stops jumping for
// nothing.
const (
	jumpSure   = 0.85
	crouchSure = 0.70
)

// A Decision is the answer of one model call.
type Decision struct {
	Action  Action
	P       float64 // the probability of Action
	Latency time.Duration
	about   int // the obstacle the state was about, see Brain.Deliver
}

// latencyWeight is the weight of the last call in the moving average of the
// latency that projects the state.
const latencyWeight = 0.2

// Brain lets a decision model play a lane.
type Brain struct {
	krn       *kronk.Kronk
	Model     string // model id
	Processor string // cpu, cuda, vulkan, metal

	// ask makes one model call. load sets it to askModel, tests to a fake.
	ask func(ctx context.Context, s State) (Decision, error)

	mu        sync.Mutex
	state     State         // the lane's latest state, projected, set by the game on every step
	seq       int           // the number of states observed so far
	paused    bool          // the game is paused: don't ask
	wake      chan struct{} // tells Play that state or paused changed
	recent    time.Duration // moving average of the call latencies, seeded by the warm-up call
	calls     int
	dropped   int // stale answers, see Deliver
	total     time.Duration
	latencies []time.Duration
	actions   [numActions]int
	last      Decision
}

// Observe records the lane after a step: the next call reads it. The state is
// projected by the cells the map moves during a call at speed, so it is
// about the lane when the answer arrives.
func (b *Brain) Observe(l *Lane, w *World, speed int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = StateAt(l, w, b.lag(speed))
	b.seq++
	b.signal()
}

// Pause stops the model from asking until it is called with false.
func (b *Brain) Pause(paused bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.paused != paused {
		b.paused = paused
		b.signal()
	}
}

// lag is how many cells the map moves during a call at speed, at the recent
// latency. b.mu must be held.
func (b *Brain) lag(speed int) int {
	return int(math.Round(b.recent.Seconds() * float64(speed)))
}

// signal wakes Play up, if it waits. b.mu must be held.
func (b *Brain) signal() {
	select {
	case b.wakeChan() <- struct{}{}:
	default: // a wake-up is pending already
	}
}

// wakeChan returns b.wake, made on first use so the zero Brain works. b.mu
// must be held.
func (b *Brain) wakeChan() chan struct{} {
	if b.wake == nil {
		b.wake = make(chan struct{}, 1)
	}
	return b.wake
}

// Play lets the model play until ctx is done: wait until the runner is on the
// ground, ask the model about the latest state, hand its answer to act (see
// Deliver), again. One call is in flight at a time, so the model does its own
// timing; the projected state makes up for the latency.
//
// It does not ask while the runner is in the air or in a hole: the answer
// would be dropped, or arrive at landing about the world as it was in the
// air. After a jump, it waits for the next state: the runner takes off on
// that step.
func (b *Brain) Play(ctx context.Context, act func(Decision)) error {
	after := 0 // ask about a state observed after this one
	for {
		s, ok := b.await(ctx, after)
		if !ok {
			return nil
		}
		d, err := b.Decide(ctx, s)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		act(d)
		if d.Action == Jump {
			b.mu.Lock()
			after = b.seq
			b.mu.Unlock()
		}
	}
}

// await waits until the game is not paused and the latest state, observed
// after the after-th one, has the runner on the ground. It reports false when
// ctx is done first.
func (b *Brain) await(ctx context.Context, after int) (State, bool) {
	for {
		b.mu.Lock()
		s, ready, wake := b.state, !b.paused && b.seq > after, b.wakeChan()
		b.mu.Unlock()
		if ready && (s.Runner == Running || s.Runner == Crouching) {
			return s, true
		}
		select {
		case <-wake:
		case <-ctx.Done():
			return State{}, false
		}
	}
}

// Deliver presses the model's answer on its lane, unless it is stale: the lane
// has passed or failed the obstacle the answer is about since the call
// started. Then it drops it, counts it, and reports false.
func (b *Brain) Deliver(l *Lane, d Decision) bool {
	if l.Resolved(d.about) {
		b.mu.Lock()
		b.dropped++
		b.mu.Unlock()
		return false
	}
	l.Press(d.Action)
	return true
}

// Decide makes one model call about s and counts it in the stats.
func (b *Brain) Decide(ctx context.Context, s State) (Decision, error) {
	d, err := b.ask(ctx, s)
	if err != nil {
		return d, err
	}
	d.about = s.obstacle
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	b.total += d.Latency
	b.latencies = append(b.latencies, d.Latency)
	b.actions[d.Action]++
	b.last = d
	if b.recent == 0 {
		b.recent = d.Latency // no warm-up call
	} else {
		b.recent += time.Duration(latencyWeight * float64(d.Latency-b.recent))
	}
	return d, nil
}

// askModel makes one model call: a single forward pass that scores every
// action.
func (b *Brain) askModel(ctx context.Context, s State) (Decision, error) {
	start := time.Now()
	resp, err := b.krn.Decision(ctx, model.DecisionRequest{
		State:     s.decisionState(),
		Questions: []model.DecisionQuestion{actionQuestion},
	})
	if err != nil {
		return Decision{}, fmt.Errorf("decision: %w", err)
	}
	ans := resp.Answers[actionQuestion.ID]
	a, p, ok := pick(ans.Choice, ans.Probabilities)
	if !ok {
		return Decision{}, fmt.Errorf("decision: unknown action %q", ans.Choice)
	}
	return Decision{Action: a, P: p, Latency: time.Since(start)}, nil
}

// pick turns the model's answer into an action and its probability. A jump
// or a crouch below its threshold is taken as run, with run's probability.
func pick(choice string, probs map[string]float64) (Action, float64, bool) {
	a, ok := parseAction(choice)
	if !ok {
		return 0, 0, false
	}
	if (a == Jump && probs[choice] < jumpSure) || (a == Crouch && probs[choice] < crouchSure) {
		a, choice = Run, Run.String()
	}
	return a, probs[choice], true
}

func parseAction(name string) (Action, bool) {
	i := slices.Index(actionNames[:], name)
	return Action(i), i >= 0
}

// Stats sums up the model calls so far.
type Stats struct {
	Calls    int
	Dropped  int // stale answers, see Brain.Deliver
	Last     Decision
	Avg      time.Duration
	P50, P95 time.Duration // only from Brain.Stats(true)
	Actions  [numActions]int
}

// Stats returns the call counts and latencies. Percentiles sort every latency
// so far: ask for them in a recap, not on every frame.
func (b *Brain) Stats(percentiles bool) Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := Stats{Calls: b.calls, Dropped: b.dropped, Last: b.last, Actions: b.actions}
	if b.calls == 0 {
		return s
	}
	s.Avg = b.total / time.Duration(b.calls)
	if percentiles {
		l := slices.Sorted(slices.Values(b.latencies))
		s.P50, s.P95 = l[len(l)/2], l[len(l)*95/100]
	}
	return s
}
