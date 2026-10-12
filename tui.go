package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The screen, top to bottom: the human's band, the score line, the model's
// band, a blank line and the footer, centered vertically. A band is a header
// line, then map rows from the top of the sky (h = topRow) down to the bottom
// under the ground (h = -1 - underRows).
const (
	skyRows     = 4 // h = 4..7: clouds, and room for the popup
	playRows    = 4 // h = 0..3: a jump peaks with the feet at 2, the head at 3
	underRows   = 2 // h = -2..-3: where a runner falls into a hole
	topRow      = playRows + skyRows - 1
	bandHeight  = 1 + skyRows + playRows + 1 + underRows
	frameHeight = 2*bandHeight + 3
	minWidth    = 64

	cloudParallax = 3  // clouds move one cell for every 3 cells of ground
	cloudSlot     = 24 // at most one cloud per slot of 24 sky cells
)

const footer = "space/↑ jump · c/↓ crouch · +/- speed · d difficulty · p pause · q quit"

// An ink is the look of a cell. Colors are lipgloss colors, "" for the
// terminal's own. lipgloss drops colors when NO_COLOR is set, so every thing
// on the map also has its own character.
type ink struct {
	fg, bg lipgloss.Color
	bold   bool
}

var (
	inkCloud = ink{fg: "#8a9bb0"}
	inkHole  = ink{bg: "#121212"}
	inkRock  = ink{fg: "#a0a0a0"}
	inkBird  = ink{fg: "#d75fd7", bold: true}
	inkBeak  = ink{fg: "#ffd75f", bold: true}
	inkYou   = ink{fg: "#5fd7ff", bold: true}
	inkBot   = ink{fg: "#ff8700", bold: true}
	inkFlash = ink{fg: "#ff3b3b", bold: true}
	inkPlus  = ink{fg: "#5fff87", bold: true}
	inkDim   = ink{fg: "#808080"}
)

// groundInks are the ground's layers for each difficulty, so the ground shows
// it: greenish on easy, yellowish on medium, reddish on hard. The grass line,
// the dirt under it, the deep dirt.
var groundInks = [numDifficulties]struct{ grass, dirt, deep ink }{
	Easy:   {ink{fg: "#6fd04a", bg: "#3d6b28"}, ink{fg: "#57913a", bg: "#3d6b28"}, ink{fg: "#3d6b28", bg: "#284a1a"}},
	Medium: {ink{fg: "#f0d050", bg: "#8a6a1c"}, ink{fg: "#b8902c", bg: "#8a6a1c"}, ink{fg: "#8a6a1c", bg: "#5c4612"}},
	Hard:   {ink{fg: "#ff6a50", bg: "#7d2a20"}, ink{fg: "#a8463a", bg: "#7d2a20"}, ink{fg: "#7d2a20", bg: "#521a14"}},
}

// skyInk is the sky's background at height h, from a dark night blue at the
// top to a lighter blue at the horizon.
func skyInk(h int) ink {
	top, horizon := [3]int{0x0e, 0x18, 0x2b}, [3]int{0x1f, 0x36, 0x58}
	var c [3]int
	for i := range c {
		c[i] = top[i] + (horizon[i]-top[i])*(topRow-h)/topRow
	}
	return ink{bg: lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", c[0], c[1], c[2]))}
}

type cell struct {
	r   rune
	ink ink
}

type tickMsg struct{}

type decisionMsg Decision

type brainErrMsg struct{ err error }

// game is the bubbletea model: one world, two lanes, one speed.
type game struct {
	world         *World
	human, bot    *Lane
	brain         *Brain
	speed         int
	steps         int
	paused        bool      // it starts paused, until you press space
	next          time.Time // when the next tick is due
	width, height int
	styles        map[ink]lipgloss.Style
	err           error // the model failed
}

func newGame(w *World, b *Brain, speed int) *game {
	g := &game{world: w, human: &Lane{}, bot: &Lane{}, brain: b, speed: speed, paused: true, styles: map[ink]lipgloss.Style{}}
	b.Pause(true)
	b.Observe(g.bot, w, speed)
	return g
}

func (g *game) Init() tea.Cmd {
	g.next = time.Now()
	return g.tick()
}

// tick schedules the next tick on an absolute deadline, one step after the
// last one, so late timers don't add up and the map moves at the speed shown.
// After a stall of more than a step, it starts again from now instead of
// catching up.
func (g *game) tick() tea.Cmd {
	period := time.Second / time.Duration(g.speed)
	g.next = g.next.Add(period)
	if time.Until(g.next) < -period {
		g.next = time.Now()
	}
	return tea.Tick(time.Until(g.next), func(time.Time) tea.Msg { return tickMsg{} })
}

// playing reports whether the lanes step: not paused, and the frame fits the
// terminal, so you can see your lane.
func (g *game) playing() bool { return !g.paused && g.fits() }

func (g *game) fits() bool { return g.width >= minWidth && g.height >= frameHeight }

func (g *game) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		g.width, g.height = msg.Width, msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return g, tea.Quit
		case "p":
			g.paused = !g.paused
		case " ", "up":
			switch {
			case g.paused:
				g.paused = false // starts or resumes, without jumping
			case g.playing():
				g.human.Press(Jump)
			}
		case "c", "down":
			// Not while the lanes don't step: the input would wait, and act
			// blind when they step again.
			if g.playing() {
				g.human.PressCrouch(KeyCrouchSteps(g.speed))
			}
		case "+", "=":
			g.speed = min(g.speed+SpeedStep, MaxSpeed)
		case "-", "_":
			g.speed = max(g.speed-SpeedStep, MinSpeed)
		case "d":
			g.world.Difficulty = g.world.Difficulty.Next()
		}

	case decisionMsg:
		g.brain.Deliver(g.bot, Decision(msg))

	case brainErrMsg:
		g.err = msg.err
		return g, tea.Quit

	case tickMsg:
		// Ticks go on while paused, the steps don't. The model does not ask
		// while paused either. Step first: on resume, the model's first call
		// reads the state of this step.
		if g.playing() {
			g.step()
		}
		g.brain.Pause(!g.playing())
		return g, g.tick()
	}
	return g, nil
}

// step advances the shared world by one step: both lanes, then the model
// lane's state for the model's next call.
func (g *game) step() {
	g.steps++
	g.human.Step(g.world)
	g.bot.Step(g.world)
	g.brain.Observe(g.bot, g.world, g.speed)
}

func (g *game) View() string {
	if g.width == 0 {
		return "" // no size yet
	}
	if !g.fits() {
		return g.tooSmall()
	}
	lines := make([]string, (g.height-frameHeight)/2, g.height)
	lines = append(lines, g.band(g.human, g.youHeader(), inkYou)...)
	lines = append(lines, g.scoreLine())
	lines = append(lines, g.band(g.bot, g.botHeader(), inkBot)...)
	lines = append(lines, "", center(g.paint(inkDim, footer), g.width))
	return strings.Join(lines, "\n")
}

func (g *game) tooSmall() string {
	lines := []string{
		"The terminal is too small for the game.",
		fmt.Sprintf("It is %dx%d, the game needs %dx%d.", g.width, g.height, minWidth, frameHeight),
		"The game is paused. Enlarge it, or press q to quit.",
	}
	lines = lines[:min(len(lines), g.height)]
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, g.width, "…")
	}
	return strings.Join(lines, "\n")
}

// band draws a lane: its header, then the sky, the play rows, the ground and
// what is under it, scrolled so the runner sits at RunnerColumn.
func (g *game) band(l *Lane, header string, runnerInk ink) []string {
	rows := make([][]cell, topRow+2+underRows)
	for i := range rows {
		rows[i] = make([]cell, g.width)
		var sky ink
		if h := topRow - i; h >= 0 {
			sky = skyInk(h)
		}
		for j := range rows[i] {
			rows[i][j] = cell{' ', sky}
		}
	}
	// set draws r at screen column col and height h, over what is there: it
	// keeps the background when r has none, so a runner in a hole stays dark.
	set := func(col, h int, r rune, k ink) {
		i := topRow - h
		if col < 0 || col >= g.width || i < 0 || i >= len(rows) {
			return
		}
		if k.bg == "" {
			k.bg = rows[i][col].ink.bg
		}
		rows[i][col] = cell{r, k}
	}

	x0 := l.X - RunnerColumn // the world cell at column 0
	g.drawClouds(set, l.X)
	g.drawGround(set, x0)
	for _, o := range g.world.Span(x0, x0+g.width) {
		drawObstacle(set, o, o.Start-x0, l.X)
	}
	drawRunner(set, l, runnerInk)

	lines := make([]string, 0, bandHeight)
	lines = append(lines, header)
	for _, r := range rows {
		lines = append(lines, g.paintCells(r))
	}
	return lines
}

type setFunc func(col, h int, r rune, k ink)

// drawClouds draws the clouds, which drift slower than the ground. Each slot
// of cloudSlot sky cells may hold one cloud, its shape drawn from a hash of the
// slot, so a cloud keeps its shape while it drifts.
func (g *game) drawClouds(set setFunc, x int) {
	shift := floorDiv(x, cloudParallax) // the sky cell at column 0
	for k := floorDiv(shift, cloudSlot); k*cloudSlot < shift+g.width; k++ {
		h := mix(uint64(k))
		if h%3 == 0 {
			continue // no cloud in this slot
		}
		width := 5 + int((h>>8)%5)
		col := k*cloudSlot + int((h>>16)%uint64(cloudSlot-width)) - shift
		top := topRow - int((h>>24)%2)
		for i := 2; i < width-2; i++ {
			set(col+i, top, '▄', inkCloud)
		}
		set(col, top-1, '▄', inkCloud)
		for i := 1; i < width-1; i++ {
			set(col+i, top-1, '█', inkCloud)
		}
		set(col+width-1, top-1, '▄', inkCloud)
	}
}

// drawGround draws the ground line and the dirt under it, with gaps for the
// holes. The dirt is speckled by world cell, so it scrolls with the map.
func (g *game) drawGround(set setFunc, x0 int) {
	ground := groundInks[g.world.Difficulty]
	for col := range g.width {
		if g.world.HoleAt(x0 + col) {
			for h := -1; h >= -1-underRows; h-- {
				set(col, h, ' ', inkHole)
			}
			continue
		}
		set(col, -1, '▀', ground.grass)
		for h := -2; h >= -1-underRows; h-- {
			r, k := ' ', ground.dirt
			if h < -2 {
				k = ground.deep
			}
			if mix(uint64(x0+col)*31+uint64(-h))%6 == 0 {
				r = '.'
			}
			set(col, h, r, k)
		}
	}
}

// drawObstacle draws a rock or a bird whose first cell is at screen column col.
// A bird's body stays on h=1, its wings flap on h=2 every 4 steps.
func drawObstacle(set setFunc, o Obstacle, col, x int) {
	switch o.Kind {
	case Rock:
		for i := range o.Width {
			for h := range o.Height {
				r := '█'
				if h == o.Height-1 {
					r = '▆'
				}
				set(col+i, h, r, inkRock)
			}
		}
	case Bird:
		sprite := [2]string{`\ /`, `<●=`} // wings up: h=2, h=1
		if x/4%2 == 1 {
			sprite[0] = `─ ─` // wings level
		}
		for row, line := range sprite {
			for i, r := range []rune(line) {
				k := inkBird
				if r == '<' {
					k = inkBeak
				}
				if r != ' ' {
					set(col+i, BirdHigh-row, r, k)
				}
			}
		}
	}
}

// drawRunner draws the runner at RunnerColumn, and the popup of its last
// score change rising above it.
func drawRunner(set setFunc, l *Lane, k ink) {
	if l.flash%4 >= 2 {
		k = inkFlash
	}
	col := RunnerColumn
	legs := 'Λ'
	if l.X/2%2 == 1 {
		legs = 'λ'
	}
	switch l.Pose() {
	case Running:
		set(col, 1, 'o', k)
		set(col, 0, legs, k)
	case Crouching:
		set(col, 0, '@', k)
	case Jumping:
		set(col, l.Feet()+1, 'o', k)
		set(col, l.Feet(), 'Y', k)
	case Sunk:
		set(col, -1, 'o', k)
		set(col, -2, 'Y', k)
	}

	gain, age, ok := l.Popup()
	if !ok {
		return
	}
	_, hi := l.Rows()
	h := min(hi+1+age/4, topRow)
	text, pk := "+1", inkPlus
	if gain < 0 {
		text, pk = "-1", inkFlash
	}
	for i, r := range text {
		set(col+i, h, r, pk)
	}
}

// paintCells renders a row of cells, one lipgloss render per run of cells
// with the same ink.
func (g *game) paintCells(cells []cell) string {
	var b strings.Builder
	run := make([]rune, 0, len(cells))
	for i := 0; i < len(cells); {
		run = run[:0]
		j := i
		for ; j < len(cells) && cells[j].ink == cells[i].ink; j++ {
			run = append(run, cells[j].r)
		}
		b.WriteString(g.paint(cells[i].ink, string(run)))
		i = j
	}
	return b.String()
}

// paint renders s with ink k. lipgloss styles are built once per ink.
func (g *game) paint(k ink, s string) string {
	if k == (ink{}) {
		return s
	}
	st, ok := g.styles[k]
	if !ok {
		st = lipgloss.NewStyle().Bold(k.bold)
		if k.fg != "" {
			st = st.Foreground(k.fg)
		}
		if k.bg != "" {
			st = st.Background(k.bg)
		}
		g.styles[k] = st
	}
	return st.Render(s)
}

func (g *game) badge(text string, k ink) string {
	return g.paint(ink{fg: "#000000", bg: k.fg, bold: true}, " "+text+" ")
}

func (g *game) youHeader() string {
	var right string
	switch {
	case g.paused && g.steps == 0:
		right = g.paint(inkYou, "press space to start ")
	case g.paused:
		right = g.paint(inkYou, "paused · press space or p to resume ")
	default:
		right = g.paint(inkDim, fmt.Sprintf("passed %d · failed %d ", sum(g.human.Passed), sum(g.human.Failed)))
	}
	return spread(g.badge("YOU", inkYou), right, g.width)
}

// modelStats is the right side of the model's header: its last answer, the
// answer's probability, the call's latency and the average. The fields have a
// fixed width, so the header holds still from one answer to the next.
const modelStats = "%-6s %3.0f%% · %3d ms · avg %3d ms "

// botHeader shows the model and the processor it runs on, its last decision
// and what its calls cost. The call count is in the recap.
func (g *game) botHeader() string {
	s := g.brain.Stats(false)
	left := g.badge("MODEL", inkBot) + " " + g.brain.Model + " - " + g.brain.Processor
	right := fmt.Sprintf(modelStats, s.Last.Action, 100*s.Last.P, s.Last.Latency.Milliseconds(), s.Avg.Milliseconds())
	if s.Calls == 0 {
		right = fmt.Sprintf("%-*s", ansi.StringWidth(right), "waiting for its first answer")
	}
	return spread(left, g.paint(inkDim, right), g.width)
}

// scoreLine is the middle line: both scores and the speed, the leader
// highlighted.
func (g *game) scoreLine() string {
	youText := fmt.Sprintf("YOU %d", g.human.Score)
	botText := fmt.Sprintf("MODEL %d", g.bot.Score)
	you := g.paint(inkYou, " "+youText+" ")
	bot := g.paint(inkBot, " "+botText+" ")
	switch {
	case g.human.Score > g.bot.Score:
		you = g.badge(youText, inkYou)
	case g.bot.Score > g.human.Score:
		bot = g.badge(botText, inkBot)
	}
	dash := g.paint(inkDim, "──")
	level := g.paint(ink{fg: groundInks[g.world.Difficulty].grass.fg, bold: true}, g.world.Difficulty.String())
	core := you + dash + fmt.Sprintf(" speed %d cells/s · ", g.speed) + level + " " + dash + bot
	core = ansi.Truncate(core, g.width, "")
	pad := g.width - ansi.StringWidth(core)
	return g.paint(inkDim, strings.Repeat("─", pad/2)) + core + g.paint(inkDim, strings.Repeat("─", pad-pad/2))
}

// spread puts left and right at both ends of a line of width w. When they
// don't fit, left is cut first.
func spread(left, right string, w int) string {
	rw := ansi.StringWidth(right)
	if ansi.StringWidth(left)+rw > w {
		left = ansi.Truncate(left, max(w-rw-1, 0), "…")
	}
	lw := ansi.StringWidth(left)
	right = ansi.Truncate(right, w-lw, "")
	return left + strings.Repeat(" ", w-lw-ansi.StringWidth(right)) + right
}

func center(s string, w int) string {
	s = ansi.Truncate(s, w, "")
	return strings.Repeat(" ", (w-ansi.StringWidth(s))/2) + s
}

func sum(counts [numKinds]int) int {
	n := 0
	for _, c := range counts {
		n += c
	}
	return n
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

// mix is splitmix64: a cheap hash that spreads close inputs far apart.
func mix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ x>>30) * 0xbf58476d1ce4e5b9
	x = (x ^ x>>27) * 0x94d049bb133111eb
	return x ^ x>>31
}
