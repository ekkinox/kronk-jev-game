package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"golang.org/x/term"
)

// runSim lets the model play alone, in real time, at speed, for dur. It runs
// the same world, lane and model loop as the game, without a screen and
// without you, and prints each obstacle as it is resolved, then a recap.
func runSim(b *Brain, speed int, seed uint64, difficulty Difficulty, dur time.Duration) error {
	w, l := NewWorld(seed), &Lane{}
	w.Difficulty = difficulty
	b.Observe(l, w, speed)

	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()
	answers := make(chan Decision)
	errc := make(chan error, 1)
	go func() {
		errc <- b.Play(ctx, func(d Decision) {
			select {
			case answers <- d:
			case <-ctx.Done():
			}
		})
	}()

	fmt.Printf("The model plays alone for %s at %d cells/s on %s, seed %d.\n", dur, speed, difficulty, seed)
	fmt.Println(paint("jump@3: a jump given 3 cells before the obstacle, as in make table: it applies on the next step. crouch@9..2: a crouch given at 9, given again down to 2.", dim))
	tick := time.NewTicker(time.Second / time.Duration(speed))
	defer tick.Stop()
	start, steps := time.Now(), 0
	var applied []move // the actions applied toward the next obstacle
	var err error
loop:
	for {
		select {
		case <-ctx.Done():
			err = <-errc
			break loop

		case err = <-errc: // Play returns before ctx is done only on an error
			break loop

		case d := <-answers:
			b.Deliver(l, d)

		case <-tick.C:
			steps++
			next, _, _ := l.Next(w)
			x, was := l.X, l.Pose()
			out, resolved := l.Step(w)
			// The distance when the action was given, the step before it
			// applies: the scale of the state and of make table.
			switch a, at := l.Applied(), next.Start-x; {
			case a == Crouch && was == Crouching && len(applied) > 0:
				applied[len(applied)-1].to = at // a crouch held on
			case a != Run:
				applied = append(applied, move{a, at, at})
			}
			if resolved {
				printOutcome(time.Since(start), out, l.Score, applied)
				applied = nil
			}
			b.Observe(l, w, speed)
		}
	}
	if err != nil {
		return err
	}

	fmt.Printf("\n%s, %d steps at %d cells/s on %s, seed %d: score %d\n", dur, steps, speed, difficulty, seed, l.Score)
	for k := range numKinds {
		fmt.Printf("  %-9s  %d passed, %d failed\n", k.String()+"s", l.Passed[k], l.Failed[k])
	}
	printModel(b, speed)
	return nil
}

// A move is an action applied toward an obstacle, at the distance from it
// when the action was given. A crouch held on is one move, from the distance
// it was given at to the last one it was given again at.
type move struct {
	a        Action
	from, to int
}

func (m move) String() string {
	if m.from == m.to {
		return fmt.Sprintf("%s@%d", m.a, m.from)
	}
	return fmt.Sprintf("%s@%d..%d", m.a, m.from, m.to)
}

func printOutcome(at time.Duration, out Outcome, score int, applied []move) {
	result := paint("passed +1", green)
	if !out.Passed {
		result = paint("failed -1", red)
	}
	moves := make([]string, len(applied))
	for i, m := range applied {
		moves[i] = m.String()
	}
	actions := strings.Join(moves, " ")
	if actions == "" {
		actions = "no action"
	}
	fmt.Printf("  %5.1fs  %-8s  %s  score %3d  %s\n", at.Seconds(), out.Obstacle, result, score, paint(actions, dim))
}

// tableCases are the obstacles -table asks about, at every distance up to
// tableDistance.
var tableCases = []Obstacle{
	{Kind: Hole, Width: 2},
	{Kind: Hole, Width: 4},
	{Kind: Rock, Width: 1, Height: 1},
	{Kind: Rock, Width: 2, Height: 2},
	{Kind: Bird, Width: BirdWidth},
}

const tableDistance = 20

// runTable asks the model once per obstacle and distance, with the runner
// running on the ground, and checks its answer against the right actions,
// played out on the engine (see Right). Far from an obstacle only run is
// right. A row where no action can clear the obstacle any more is too late,
// and left out of the accuracy. The distance is not projected: each row is
// "if applied right now". The state holds the obstacle's kind only, so the two
// holes, and the two rocks, are the same question with different right
// answers.
func runTable(b *Brain) error {
	fmt.Println("The runner runs on the ground toward one obstacle. Right: jump or crouch when that action, given now,")
	fmt.Println("clears the obstacle; run while acting later still would. Too late rows are not counted.")
	fmt.Println()
	fmt.Printf("%-9s  %4s  %-12s  %-12s\n", "obstacle", "dist", "model", "right")

	var right, counted, late int
	var perCase []string
	for _, o := range tableCases {
		caseRight, caseCounted := 0, 0
		for d := 0; d <= tableDistance; d++ {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			dec, err := b.Decide(ctx, State{Runner: Running, Next: o.Kind.String(), Distance: d})
			cancel()
			if err != nil {
				return fmt.Errorf("%s at %d: %w", o, d, err)
			}

			want := Right(o, d)
			names := make([]string, len(want))
			for i, a := range want {
				names[i] = a.String()
			}
			mark := paint("too late", dim)
			switch {
			case len(want) == 0:
				late++
			case slices.Contains(want, dec.Action):
				mark = paint("✓", green)
				caseRight++
				caseCounted++
			default:
				mark = paint("✗", red)
				caseCounted++
			}
			if len(names) == 0 {
				names = []string{"-"}
			}
			fmt.Printf("%-9s  %4d  %-6s %.2f   %-12s  %s\n", o, d, dec.Action, dec.P, strings.Join(names, " "), mark)
		}
		right += caseRight
		counted += caseCounted
		perCase = append(perCase, fmt.Sprintf("%s %d/%d", o, caseRight, caseCounted))
	}

	s := b.Stats(true)
	fmt.Printf("\nby obstacle  %s\n", strings.Join(perCase, ", "))
	fmt.Printf("latency      avg %d ms, p50 %d ms, p95 %d ms over %d calls\n", s.Avg.Milliseconds(), s.P50.Milliseconds(), s.P95.Milliseconds(), s.Calls)
	fmt.Printf("accuracy     %d/%d (%.1f%%), %d too late rows not counted\n", right, counted, 100*float64(right)/float64(counted), late)
	return nil
}

// ANSI colors for -sim and -table, only when stdout is a terminal and
// NO_COLOR is unset. The game itself colors with lipgloss.
const (
	dim   = "2"
	red   = "31"
	green = "32"
)

var useColor = term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("NO_COLOR") == ""

func paint(s, code string) string {
	if !useColor {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}
