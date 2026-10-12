package main

import (
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

var space = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}

// testGame is a game with a brain that has no model, but stats to show.
func testGame(width, height int) *game {
	b := &Brain{Model: "Jev-Style-0.8B-Decision-v3-Q8_0", Processor: "vulkan"}
	g := newGame(NewWorld(3), b, DefaultSpeed)
	g.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return g
}

// TestViewFits plays both lanes for a while at several terminal sizes, with
// colors on, and checks every frame: no line wider than the terminal, no more
// lines than it has rows.
func TestViewFits(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	down := tea.KeyMsg{Type: tea.KeyDown}
	for _, size := range [][2]int{{121, 67}, {120, 33}, {80, 30}, {minWidth, frameHeight}, {minWidth - 1, 40}, {120, frameHeight - 1}, {20, 3}} {
		w, h := size[0], size[1]
		g := testGame(w, h)
		for i := range 600 {
			switch {
			case i%23 == 0:
				g.Update(space)
			case i%31 == 0:
				g.Update(down)
			case i%17 == 0:
				g.Update(decisionMsg{Action: Jump, about: g.bot.next})
			}
			if i == 300 {
				// A model that has answered: the longest model header.
				g.brain.calls, g.brain.total = 1234, 1234*45*time.Millisecond
				g.brain.last = Decision{Action: Crouch, P: 0.8312, Latency: 1234 * time.Millisecond}
			}
			g.step()

			lines := strings.Split(g.View(), "\n")
			if len(lines) > h {
				t.Fatalf("%dx%d, step %d: %d lines", w, h, i, len(lines))
			}
			for n, line := range lines {
				if lw := ansi.StringWidth(line); lw > w {
					t.Fatalf("%dx%d, step %d: line %d is %d wide: %q", w, h, i, n, lw, ansi.Strip(line))
				}
			}
		}
	}
}

// TestViewContent checks what a full frame shows.
func TestViewContent(t *testing.T) {
	g := testGame(80, 30)
	g.Update(space)
	for range 200 {
		g.step()
	}
	view := g.View()
	for _, want := range []string{" YOU ", " MODEL ", "speed 20 cells/s", "Jev-Style-0.8B-Decision-v3-Q8_0", "waiting for its first answer", footer} {
		if !strings.Contains(view, want) {
			t.Errorf("the frame lacks %q", want)
		}
	}

	g.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'+'}})
	g.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'+'}})
	if g.speed != DefaultSpeed+2*SpeedStep {
		t.Errorf("speed %d after ++, want %d", g.speed, DefaultSpeed+2*SpeedStep)
	}
	for range 20 {
		g.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'-'}})
	}
	if g.speed != MinSpeed {
		t.Errorf("speed %d after many -, want %d", g.speed, MinSpeed)
	}
}

// TestModelHeader checks that the model's header keeps its width from one
// answer to the next, so it does not jitter.
func TestModelHeader(t *testing.T) {
	g := testGame(120, 30)
	width := func() int {
		h := ansi.Strip(g.botHeader())
		return len([]rune(h)) - len([]rune(strings.TrimLeft(h[strings.Index(h, "Q8_0")+4:], " ")))
	}
	want := width() // waiting for its first answer
	g.brain.calls, g.brain.total = 1, 80*time.Millisecond
	for _, d := range []Decision{
		{Action: Run, P: 0.5, Latency: 8 * time.Millisecond},
		{Action: Crouch, P: 1, Latency: 123 * time.Millisecond},
		{Action: Jump, P: 0.05, Latency: 80 * time.Millisecond},
	} {
		g.brain.last = d
		if got := width(); got != want {
			t.Errorf("after %+v the stats start at column %d, want %d", d, got, want)
		}
	}
}

// TestStartAndPause checks that the game waits for space to start, that this
// first space does not jump, that p pauses and resumes, and that nothing
// steps while the terminal is too small.
// TestDifficultyKey checks that d cycles easy, medium, hard and back, and
// that the score line and the ground show it.
func TestDifficultyKey(t *testing.T) {
	g := testGame(121, 67)
	d := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}}
	for _, want := range []Difficulty{Easy, Medium, Hard, Easy} {
		if g.world.Difficulty != want {
			t.Fatalf("difficulty %s, want %s", g.world.Difficulty, want)
		}
		view := g.View()
		if !strings.Contains(view, "· "+want.String()) {
			t.Errorf("%s: the score line does not show it", want)
		}
		if grass := g.paint(groundInks[want].grass, "▀"); !strings.Contains(view, grass) {
			t.Errorf("%s: the ground is not in its color", want)
		}
		g.Update(d)
	}
}

func TestStartAndPause(t *testing.T) {
	g := testGame(80, 30)
	tick := func() { g.Update(tickMsg{}) }
	tick()
	if g.steps != 0 || !strings.Contains(g.View(), "press space to start") {
		t.Fatalf("before space: %d steps, want 0 and \"press space to start\"", g.steps)
	}
	if !g.brain.paused {
		t.Error("the model may ask before the game starts")
	}
	g.Update(space)
	if g.human.pending != Run {
		t.Error("the space that starts the game jumps")
	}
	tick()
	if g.steps != 1 || g.brain.paused {
		t.Fatalf("after space: %d steps, model paused %v; want 1, false", g.steps, g.brain.paused)
	}

	p := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}}
	g.Update(p)
	tick()
	if g.steps != 1 || !strings.Contains(g.View(), "paused") || !g.brain.paused {
		t.Fatalf("paused: %d steps, want 1 and \"paused\"", g.steps)
	}
	g.Update(p)
	tick()
	if g.steps != 2 {
		t.Fatalf("resumed: %d steps, want 2", g.steps)
	}

	g.Update(tea.WindowSizeMsg{Width: minWidth - 1, Height: 40})
	tick()
	if g.steps != 2 || !strings.Contains(g.View(), "paused") || !g.brain.paused {
		t.Fatalf("too small: %d steps, want 2 and \"paused\"", g.steps)
	}
	g.Update(space)
	g.Update(tea.KeyMsg{Type: tea.KeyDown})
	if g.human.pending != Run {
		t.Errorf("a key pressed while too small waits to act blind: %s", g.human.pending)
	}
	g.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	tick()
	if g.steps != 3 {
		t.Fatalf("enlarged: %d steps, want 3", g.steps)
	}
}

// TestCrouchKey checks that the crouch key crouches for KeyCrouchSteps, the
// model's answer for CrouchSteps.
func TestCrouchKey(t *testing.T) {
	g := testGame(80, 30)
	g.Update(space)
	g.Update(tea.KeyMsg{Type: tea.KeyDown})
	g.bot.Press(Crouch)
	for i := range KeyCrouchSteps(g.speed) {
		g.step()
		if g.human.Pose() != Crouching {
			t.Fatalf("step %d of %d: you are %s, want crouching", i, KeyCrouchSteps(g.speed), g.human.Pose())
		}
		if want := i < CrouchSteps; (g.bot.Pose() == Crouching) != want {
			t.Fatalf("step %d: the model is %s, crouching %v", i, g.bot.Pose(), want)
		}
	}
	g.step()
	if g.human.Pose() != Running {
		t.Errorf("you are %s after the crouch, want running", g.human.Pose())
	}
}

// TestProgram runs the game as a bubbletea program without a terminal: after
// space, it must step on its own, and quit on q.
func TestProgram(t *testing.T) {
	g := testGame(80, 30)
	p := tea.NewProgram(g, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutSignalHandler())
	go func() {
		p.Send(space)
		time.Sleep(300 * time.Millisecond)
		p.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	}()
	done := make(chan error, 1)
	go func() {
		_, err := p.Run()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		p.Kill()
		t.Fatal("the program did not quit on q")
	}
	if g.steps < 3 {
		t.Errorf("%d steps in 300 ms at %d cells/s, want about 6", g.steps, DefaultSpeed)
	}
}
