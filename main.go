// This program is an endless runner in the terminal, like the Chrome dinosaur
// game, played by you and by a local *decision model* run by Kronk, at the same
// time, on the same map. It shows whether a small model reacts well and fast
// enough to play in real time.
//
// The screen is split in two: you play the top half, the model plays the same
// map in the bottom half, and the score line sits between them.
//
// A decision model does not generate text: one forward pass returns a
// probability for each answer option (run, jump, crouch). That is one call.
//
// The first time you run this program it downloads the model and llama.cpp
// into ~/.kronk-game.
//
// Run it like this:
// $ go run .              # play against the model
// $ go run . -sim 60s     # the model plays alone for a minute, then a recap
// $ go run . -table       # the model's answer for each obstacle and distance
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

func main() {
	if err := run(); err != nil {
		fmt.Printf("ERROR: %s\n", err)
		os.Exit(1)
	}
}

func run() error {
	speed := flag.Int("speed", DefaultSpeed, fmt.Sprintf("map scrolling speed in `N` cells per second, %d to %d", MinSpeed, MaxSpeed))
	seed := flag.Uint64("seed", 0, "map seed `N`, the same map for the same seed (default random)")
	sim := flag.Duration("sim", 0, "let the model play alone, without a screen, for `DURATION` (e.g. 60s), then print a recap")
	table := flag.Bool("table", false, "ask the model about each obstacle at each distance, then print its accuracy")
	level := flag.String("difficulty", Easy.String(), "`LEVEL` at the start: easy, medium or hard (d changes it in the game)")
	download := flag.Bool("download", false, "download llama.cpp and the model, then exit")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: kronk-jev-game [-speed N] [-seed N] [-difficulty LEVEL]\n       kronk-jev-game -sim DURATION [-speed N] [-seed N] [-difficulty LEVEL]\n       kronk-jev-game -table\n       kronk-jev-game -download\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if !isSet("seed") {
		*seed = rand.Uint64()
	}

	if flag.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flag.Arg(0))
	}
	if *speed < MinSpeed || *speed > MaxSpeed {
		return fmt.Errorf("-speed %d: want %d to %d cells per second", *speed, MinSpeed, MaxSpeed)
	}
	if *sim < 0 {
		return errors.New("-sim: want a positive duration")
	}
	difficulty, ok := parseDifficulty(*level)
	if !ok {
		return fmt.Errorf("-difficulty %q: want easy, medium or hard", *level)
	}
	if *download {
		if _, _, err := install(context.Background()); err != nil {
			return err
		}
		fmt.Printf("llama.cpp and %s are downloaded\n", modelSource)
		return nil
	}

	interactive := !*table && *sim == 0
	if interactive && !(term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))) {
		return errors.New("the game needs a terminal; use -sim or -table to run without one")
	}

	b, err := load()
	if err != nil {
		return err
	}
	defer b.Close()

	switch {
	case *table:
		return runTable(b)
	case *sim > 0:
		return runSim(b, *speed, *seed, difficulty, *sim)
	}
	return play(b, *speed, *seed, difficulty)
}

func isSet(name string) bool {
	set := false
	flag.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

// play runs the game on the terminal, the model playing in the background,
// then prints a recap.
func play(b *Brain, speed int, seed uint64, difficulty Difficulty) error {
	w := NewWorld(seed)
	w.Difficulty = difficulty
	g := newGame(w, b, speed)
	// 120 fps: above the top speed of 60 cells/s, so a frame never shows two
	// steps at once.
	p := tea.NewProgram(g, tea.WithAltScreen(), tea.WithFPS(120))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := b.Play(ctx, func(d Decision) { p.Send(decisionMsg(d)) })
		if err != nil {
			p.Send(brainErrMsg{err})
		}
	}()

	start := time.Now()
	_, err := p.Run()
	cancel()
	<-done
	if err != nil {
		return err
	}

	fmt.Printf("seed %d · %s · %d steps · speed %d cells/s, %s at the end\n\n",
		seed, time.Since(start).Round(time.Second), g.steps, g.speed, g.world.Difficulty)
	fmt.Printf("          score  holes  rocks  birds  (passed/failed)\n")
	printLane("YOU", g.human)
	printLane("MODEL", g.bot)
	fmt.Println()
	printModel(b, g.speed)
	return g.err
}

func printLane(name string, l *Lane) {
	fmt.Printf("  %-6s  %5d", name, l.Score)
	for k := range numKinds {
		fmt.Printf("  %5s", fmt.Sprintf("%d/%d", l.Passed[k], l.Failed[k]))
	}
	fmt.Println()
}

// printModel prints what the model's calls cost, and what it answered.
func printModel(b *Brain, speed int) {
	s := b.Stats(true)
	fmt.Printf("%s on %s\n", b.Model, b.Processor)
	fmt.Printf("  calls      %d\n", s.Calls)
	if s.Calls == 0 {
		return
	}
	fmt.Printf("  latency    avg %d ms, p50 %d ms, p95 %d ms: the map moves %.1f cells during a call at %d cells/s\n",
		s.Avg.Milliseconds(), s.P50.Milliseconds(), s.P95.Milliseconds(), s.Avg.Seconds()*float64(speed), speed)
	fmt.Printf("  dropped    %d stale answers, about an obstacle passed or failed during the call\n", s.Dropped)
	var answers []string
	for a := range numActions {
		answers = append(answers, fmt.Sprintf("%s %d (%.0f%%)", a, s.Actions[a], 100*float64(s.Actions[a])/float64(s.Calls)))
	}
	fmt.Printf("  decisions  %s\n", strings.Join(answers, ", "))
}
