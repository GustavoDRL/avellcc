package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/hugo-andrade/avellcc/internal/keyboard"
)

// Calibration control keys. Plain key presses are recorded as the key under the
// lit LED, so every command needs a Ctrl chord to stay unambiguous — there is no
// single key that could not also be the one currently lit.
const (
	calSkip   = "ctrl+s"
	calBack   = "ctrl+b"
	calName   = "ctrl+n"
	calClear  = "ctrl+d"
	calGoto   = "ctrl+g"
	calNext   = "ctrl+f"
	calFinish = "ctrl+q"
)

// gridLabels shortens key names so a 21-column grid stays readable. ASCII only,
// because cells are padded by byte width.
var gridLabels = map[string]string{
	"backspace":  "bksp",
	"backslash":  "\\",
	"apostrophe": "'",
	"semicolon":  ";",
	"period":     ".",
	"comma":      ",",
	"equal":      "=",
	"minus":      "-",
	"slash":      "/",
	"asterisk":   "*",
	"plus":       "+",
	"lbracket":   "[",
	"rbracket":   "]",
	"grave":      "`",
	"enter":      "entr",
	"space":      "spc",
	"capslock":   "caps",
	"delete":     "del",
	"insert":     "ins",
	"pageup":     "pgup",
	"pagedown":   "pgdn",
	"up":         "up",
	"down":       "dn",
	"left":       "lf",
	"right":      "rt",
	"lshift":     "lsft",
	"rshift":     "rsft",
	"lctrl":      "lctl",
	"rctrl":      "rctl",
	"lmeta":      "win",
}

// gridLabel renders one key name inside a cell, marking keypad keys with '#' so
// the numeric keypad is distinguishable from the number row at a glance.
func gridLabel(name string, width int) string {
	if label, ok := gridLabels[name]; ok {
		name = label
	} else if rest, ok := strings.CutPrefix(name, "num_"); ok {
		if label, ok := gridLabels[rest]; ok {
			rest = label
		}
		name = "#" + rest
	}
	if len(name) > width {
		name = name[:width]
	}
	return name
}

// CalibratePanel walks the LED grid one position at a time and records which
// physical key sits under each LED.
//
// The LED grid is wired by the laptop vendor rather than the controller, so no
// built-in map can be correct for every machine; this is how the real one gets
// made.
type CalibratePanel struct {
	kb         keyboard.Controller
	rows, cols int

	step    int               // visit every step-th column; 1 sweeps everything
	idx     int               // current position, row-major
	names   map[int]string    // position index -> key name
	skipped map[int]bool      // positions explicitly marked as having no key
	origin  map[string][2]int // map loaded at start, for reporting changes

	input    string // active text prompt: "", calName or calGoto
	inputBuf string

	enhanced bool // terminal reports keypad and modifier keys separately

	width, height int
	err           error
	status        string
	finished      bool
}

// NewCalibratePanel creates the calibration TUI. step > 1 visits only every
// step-th column, which trades a full sweep for anchors that a later pass
// interpolates between.
func NewCalibratePanel(kb keyboard.Controller, step int) *CalibratePanel {
	if step < 1 {
		step = 1
	}
	existing := keyboard.LoadKeymapFor(kb)
	m := &CalibratePanel{
		kb:      kb,
		step:    step,
		rows:    kb.Rows(),
		cols:    kb.Cols(),
		names:   map[int]string{},
		skipped: map[int]bool{},
		origin:  existing,
	}
	for name, pos := range existing {
		if pos[0] >= 0 && pos[0] < m.rows && pos[1] >= 0 && pos[1] < m.cols {
			m.names[pos[0]*m.cols+pos[1]] = name
		}
	}
	m.idx = m.firstUnmapped()
	return m
}

// isAnchor reports whether a position is part of the sweep. The last column is
// always included so each row's right edge is pinned down.
func (m *CalibratePanel) isAnchor(idx int) bool {
	if m.step <= 1 {
		return true
	}
	col := idx % m.cols
	return col%m.step == 0 || col == m.cols-1
}

// anchorCount is how many positions this sweep will actually visit.
func (m *CalibratePanel) anchorCount() int {
	n := 0
	for i := 0; i < m.total(); i++ {
		if m.isAnchor(i) {
			n++
		}
	}
	return n
}

// nextUnmapped finds the next anchor with no key name, wrapping around, so gaps
// left by an earlier session can be filled without walking the whole grid.
func (m *CalibratePanel) nextUnmapped() (int, bool) {
	for i := 1; i <= m.total(); i++ {
		idx := (m.idx + i) % m.total()
		if _, ok := m.names[idx]; !ok && m.isAnchor(idx) {
			return idx, true
		}
	}
	return 0, false
}

// firstUnmapped picks up where a previous session stopped.
func (m *CalibratePanel) firstUnmapped() int {
	for i := 0; i < m.total(); i++ {
		if _, ok := m.names[i]; !ok && m.isAnchor(i) {
			return i
		}
	}
	return 0
}

func (m *CalibratePanel) Init() tea.Cmd {
	_ = m.kb.SetBrightness(10)
	_ = m.kb.SetAllKeys(0, 0, 0)
	m.repaint()
	return tea.RequestWindowSize
}

func (m *CalibratePanel) total() int { return m.rows * m.cols }

func (m *CalibratePanel) rowCol(idx int) (int, int) { return idx / m.cols, idx % m.cols }

// repaint lights the whole grid row dimly with the position under examination
// at full brightness. Grid rows do not follow the keyboard's physical row order,
// so showing the band being swept is what makes it possible to tell that a
// position genuinely has no LED, rather than guessing at the next key in a
// layout that does not apply.
func (m *CalibratePanel) repaint() {
	row, col := m.rowCol(m.idx)
	colorMap := make(map[[2]int][3]byte, m.total())
	for r := 0; r < m.rows; r++ {
		for c := 0; c < m.cols; c++ {
			var rgb [3]byte
			switch {
			case m.finished:
			case r == row && c == col:
				rgb = [3]byte{255, 255, 255}
			case r == row:
				rgb = [3]byte{16, 16, 48}
			}
			colorMap[[2]int{r, c}] = rgb
		}
	}
	if err := m.kb.SetKeyMap(colorMap); err != nil {
		m.err = err
	}
}

func (m *CalibratePanel) move(to int) { m.moveDir(to, 1) }

// moveDir lands on the next anchor in the given direction, so a sparse sweep
// never stops on a position it is not meant to visit.
func (m *CalibratePanel) moveDir(to, dir int) {
	for to >= 0 && to < m.total() && !m.isAnchor(to) {
		to += dir
	}
	if to < 0 {
		to = 0
	}
	if to >= m.total() {
		m.finished = true
		m.idx = m.total() - 1
		m.repaint()
		return
	}
	m.finished = false
	m.idx = to
	m.repaint()
}

func (m *CalibratePanel) positionOf(name string) (int, bool) {
	for idx, existing := range m.names {
		if existing == name {
			return idx, true
		}
	}
	return 0, false
}

// resolveCollision stops two physical keys that report the same name from
// overwriting each other. A numeric keypad reports plain "7" for its 7 unless
// the terminal negotiates the extended keyboard protocol, and the keymap is
// name to position, so the second one needs a name of its own.
func (m *CalibratePanel) resolveCollision(name string) (string, string) {
	other, taken := m.positionOf(name)
	if !taken || other == m.idx {
		return name, ""
	}
	r, c := m.rowCol(other)
	if !strings.HasPrefix(name, "num_") {
		if candidate := "num_" + name; !m.nameTaken(candidate) {
			return candidate, fmt.Sprintf(" (%s already at row %d col %d — recorded as keypad)", name, r, c)
		}
	}
	for i := 2; i < 10; i++ {
		candidate := fmt.Sprintf("%s_%d", name, i)
		if !m.nameTaken(candidate) {
			return candidate, fmt.Sprintf(" (%s already at row %d col %d)", name, r, c)
		}
	}
	return name, ""
}

func (m *CalibratePanel) nameTaken(name string) bool {
	_, taken := m.positionOf(name)
	return taken
}

// assign records a key name at the current position and advances.
func (m *CalibratePanel) assign(raw string) {
	name := keyboard.CanonicalKeyName(raw)
	if name == "" {
		return
	}
	final, note := m.resolveCollision(name)
	m.names[m.idx] = final
	delete(m.skipped, m.idx)
	r, c := m.rowCol(m.idx)
	m.status = fmt.Sprintf("row %d col %d = %s%s", r, c, final, note)
	m.move(m.idx + 1)
}

// jumpTo parses "row,col" or a plain position number.
func (m *CalibratePanel) jumpTo(spec string) {
	spec = strings.TrimSpace(spec)
	if row, col, ok := strings.Cut(spec, ","); ok {
		r, err1 := strconv.Atoi(strings.TrimSpace(row))
		c, err2 := strconv.Atoi(strings.TrimSpace(col))
		if err1 != nil || err2 != nil || r < 0 || r >= m.rows || c < 0 || c >= m.cols {
			m.status = "invalid position"
			return
		}
		m.move(r*m.cols + c)
		return
	}
	n, err := strconv.Atoi(spec)
	if err != nil || n < 1 || n > m.total() {
		m.status = "invalid position"
		return
	}
	m.move(n - 1)
}

func (m *CalibratePanel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyboardEnhancementsMsg:
		m.enhanced = msg.SupportsKeyDisambiguation()
		return m, nil

	case tea.KeyPressMsg:
		key := msg.String()

		if m.input != "" {
			switch key {
			case "enter":
				mode, buf := m.input, m.inputBuf
				m.input, m.inputBuf = "", ""
				if buf == "" {
					return m, nil
				}
				if mode == calGoto {
					m.jumpTo(buf)
				} else {
					m.assign(buf)
				}
			case KeyEsc:
				m.input, m.inputBuf = "", ""
				m.status = "cancelled"
			case "backspace":
				if m.inputBuf != "" {
					m.inputBuf = m.inputBuf[:len(m.inputBuf)-1]
				}
			default:
				if key == "space" {
					key = " "
				}
				if len(key) == 1 {
					m.inputBuf += key
				}
			}
			return m, nil
		}

		switch key {
		case calFinish, KeyCtrlC:
			m.finished = true
			m.repaint()
			return m, tea.Quit

		case calSkip:
			m.skipped[m.idx] = true
			delete(m.names, m.idx)
			m.status = "no key here"
			m.move(m.idx + 1)

		case calBack:
			m.moveDir(m.idx-1, -1)
			m.status = "went back"

		case calNext:
			if next, ok := m.nextUnmapped(); ok {
				m.move(next)
				m.status = "jumped to next unmapped position"
			} else {
				m.status = "every position has a key name"
			}

		case calName:
			m.input = calName
			m.inputBuf = ""

		case calGoto:
			m.input = calGoto
			m.inputBuf = ""

		case calClear:
			delete(m.names, m.idx)
			delete(m.skipped, m.idx)
			m.status = "cleared"

		default:
			// Anything else is the key sitting under the lit LED. Unhandled
			// chords are ignored so a stray combination is never recorded.
			if strings.Contains(key, "+") {
				m.status = "unrecognised command — use ctrl+n to type a name"
				return m, nil
			}
			m.assign(key)
		}
		return m, nil
	}
	return m, nil
}

func (m *CalibratePanel) View() tea.View {
	if m.width == 0 {
		return tea.NewView("Loading...")
	}

	var sb strings.Builder
	RenderHeader(&sb, "calibrate", m.kb.Name(), m.width)

	row, col := m.rowCol(m.idx)
	muted := lipgloss.NewStyle().Foreground(ColorMuted)
	bold := lipgloss.NewStyle().Bold(true)

	if m.finished {
		fmt.Fprintf(&sb, "  %s  %s\n\n",
			bold.Render("Swept every position."),
			muted.Render(fmt.Sprintf("%d keys mapped", len(m.names))))
	} else {
		scope := fmt.Sprintf("%d", m.total())
		if m.step > 1 {
			scope = fmt.Sprintf("%d (anchors, every %d columns)", m.anchorCount(), m.step)
		}
		fmt.Fprintf(&sb, "  Position %s of %s   %s\n",
			bold.Render(fmt.Sprintf("%d", m.idx+1)), scope,
			muted.Render(fmt.Sprintf("row %d, col %d — %d mapped", row, col, len(m.names))))
		fmt.Fprintf(&sb, "  %s\n", "Press the bright white key. The dim blue band is this grid row.")
		fmt.Fprintf(&sb, "  %s\n\n", muted.Render("If no key lit up, this position has no LED — press ctrl+s."))
	}

	if !m.enhanced {
		fmt.Fprintf(&sb, "  %s\n\n", muted.Render(
			"Terminal does not report keypad or modifier keys separately; "+
				"duplicates are auto-named num_*."))
	}

	switch m.input {
	case calName:
		fmt.Fprintf(&sb, "  %s %s\n\n", bold.Render("Key name:"), m.inputBuf+"_")
	case calGoto:
		fmt.Fprintf(&sb, "  %s %s   %s\n\n", bold.Render("Go to:"), m.inputBuf+"_",
			muted.Render("row,col  or  position number"))
	}

	m.renderGrid(&sb)

	if m.status != "" {
		fmt.Fprintf(&sb, "\n  %s\n", muted.Render(m.status))
	}
	if m.err != nil {
		fmt.Fprintf(&sb, "\n  %s\n", lipgloss.NewStyle().Foreground(lipgloss.Red).Render(m.err.Error()))
	}

	fmt.Fprintf(&sb, "\n%s\n", RenderHelp(
		"press lit key: record",
		"ctrl+s: no key",
		"ctrl+n: type name",
		"ctrl+f: next gap",
		"ctrl+g: go to",
		"ctrl+b: back",
		"ctrl+d: clear",
		"ctrl+q: save & quit",
	))

	v := tea.NewView(sb.String())
	// Asking for enhancements is what makes a capable terminal report keypad
	// and modifier keys distinctly, which removes the duplicate-name problem.
	v.KeyboardEnhancements.ReportEventTypes = true
	return v
}

// renderGrid draws the LED grid so progress and the current position are
// visible at a glance.
func (m *CalibratePanel) renderGrid(sb *strings.Builder) {
	cell := 5
	for cell > 2 && m.width < m.cols*cell+6 {
		cell--
	}
	if m.width < m.cols*cell+6 {
		fmt.Fprintf(sb, "  %s\n", lipgloss.NewStyle().Foreground(ColorMuted).
			Render("(terminal too narrow to draw the grid)"))
		return
	}

	current := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.BrightWhite)
	done := lipgloss.NewStyle().Foreground(ColorMuted)
	empty := lipgloss.NewStyle().Foreground(ColorMuted).Faint(true)

	for r := 0; r < m.rows; r++ {
		fmt.Fprintf(sb, "%2d ", r)
		for c := 0; c < m.cols; c++ {
			idx := r*m.cols + c
			text := "."
			style := empty
			if !m.isAnchor(idx) {
				text = " "
			}
			if name, ok := m.names[idx]; ok {
				text = gridLabel(name, cell-1)
				style = done
			} else if m.skipped[idx] {
				text = "-"
			}
			if idx == m.idx && !m.finished {
				style = current
				if text == "." {
					text = "[]"
				}
			}
			sb.WriteString(style.Render(fmt.Sprintf("%-*s", cell, text)))
		}
		sb.WriteString("\n")
	}
}

// Result returns the calibrated map, ready to be saved.
func (m *CalibratePanel) Result() map[string][2]int {
	out := make(map[string][2]int, len(m.names))
	for idx, name := range m.names {
		out[name] = [2]int{idx / m.cols, idx % m.cols}
	}
	return out
}

// Summary describes what changed relative to the map loaded at start.
func (m *CalibratePanel) Summary() string {
	result := m.Result()
	var added, moved []string
	for name, pos := range result {
		old, existed := m.origin[name]
		switch {
		case !existed:
			added = append(added, name)
		case old != pos:
			moved = append(moved, name)
		}
	}
	sort.Strings(added)
	sort.Strings(moved)

	parts := []string{fmt.Sprintf("%d keys mapped", len(result))}
	if len(added) > 0 {
		parts = append(parts, fmt.Sprintf("%d new", len(added)))
	}
	if len(moved) > 0 {
		parts = append(parts, fmt.Sprintf("%d moved", len(moved)))
	}
	return strings.Join(parts, ", ")
}
