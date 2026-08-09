package main

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// errInterrupted is returned by the ask* helpers when the user aborts the
// prompt with Ctrl+C.
var errInterrupted = errors.New("interrupted")

// runPrompt runs one interactive prompt as a Bubble Tea program. Input comes
// from a TTY: bubbletea reads stdin when it is a terminal and falls back to
// /dev/tty otherwise, so prompts never consume piped stdin. On a graceful
// quit the final frame stays on screen, which is what shows the question and
// its answer in the terminal transcript.
func runPrompt(m tea.Model) (tea.Model, error) {
	return tea.NewProgram(m).Run()
}

// askText renders a one-line input and returns the entered value, trimmed.
func askText(label string) (string, error) {
	final, err := runPrompt(textPrompt{label: label, input: newTextInput(false)})
	if err != nil {
		return "", err
	}
	m := final.(textPrompt)
	if m.aborted {
		return "", errInterrupted
	}
	return strings.TrimSpace(m.input.Value()), nil
}

// askConfirm asks a yes/no question. y/yes is true, n/no is false, Enter or
// Esc yields the default. The answered prompt (e.g. "…? [y/N]: yes") stays
// in the terminal transcript.
func askConfirm(label string, def bool) (bool, error) {
	final, err := runPrompt(confirmPrompt{label: label, def: def})
	if err != nil {
		return false, err
	}
	m := final.(confirmPrompt)
	if m.aborted {
		return false, errInterrupted
	}
	return m.answer, nil
}

// askChoice renders a selectable list and returns the index of the item the
// user picks with Enter. The cursor starts at def.
func askChoice(label string, items []string, def int) (int, error) {
	if def < 0 || def >= len(items) {
		def = 0
	}
	final, err := runPrompt(choicePrompt{list: newChoiceList(label, items, def)})
	if err != nil {
		return 0, err
	}
	m := final.(choicePrompt)
	if m.aborted {
		return 0, errInterrupted
	}
	return m.list.Index(), nil
}

// askPassword renders a masked input and returns the entered value. The
// value itself is never echoed to the terminal.
func askPassword(label string) (string, error) {
	final, err := runPrompt(textPrompt{label: label, input: newTextInput(true)})
	if err != nil {
		return "", err
	}
	m := final.(textPrompt)
	if m.aborted {
		return "", errInterrupted
	}
	return m.input.Value(), nil
}

// newTextInput builds a focused one-line input. masked hides the value
// (used for passwords).
func newTextInput(masked bool) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Width = 60
	if masked {
		ti.EchoMode = textinput.EchoPassword
	}
	ti.Focus()
	return ti
}

// textPrompt is the model behind askText and askPassword: a label followed
// by a one-line text input.
type textPrompt struct {
	label   string
	input   textinput.Model
	aborted bool
}

func (m textPrompt) Init() tea.Cmd { return textinput.Blink }

func (m textPrompt) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			return m, tea.Quit
		case tea.KeyCtrlC:
			m.aborted = true
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		// Keep label + input on one line, shrinking the input when the
		// terminal is narrow.
		w := msg.Width - utf8.RuneCountInString(m.label) - 2
		if w < 10 {
			w = 10
		}
		m.input.Width = w
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m textPrompt) View() string {
	return m.label + m.input.View()
}

// confirmPrompt is the model behind askConfirm: a yes/no question with a
// [y/N] (or [Y/n]) hint. The answer is appended to the final frame.
type confirmPrompt struct {
	label   string
	def     bool
	answer  bool
	done    bool
	aborted bool
}

func (m confirmPrompt) Init() tea.Cmd { return nil }

func (m confirmPrompt) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.aborted = true
			return m, tea.Quit
		case "y", "Y":
			m.answer, m.done = true, true
			return m, tea.Quit
		case "n", "N":
			m.done = true
			return m, tea.Quit
		case "enter", "esc":
			m.answer, m.done = m.def, true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m confirmPrompt) View() string {
	suffix := " [y/N]: "
	if m.def {
		suffix = " [Y/n]: "
	}
	view := m.label + suffix
	if m.done {
		if m.answer {
			view += "yes"
		} else {
			view += "no"
		}
	}
	return view
}

// choiceItem adapts a plain string to list.Item.
type choiceItem struct{ title string }

func (c choiceItem) Title() string       { return c.title }
func (c choiceItem) Description() string { return "" }
func (c choiceItem) FilterValue() string { return c.title }

func choiceItems(items []string) []list.Item {
	out := make([]list.Item, len(items))
	for i, it := range items {
		out[i] = choiceItem{title: it}
	}
	return out
}

// newChoiceList builds the single-select list behind askChoice. Quit
// keybindings are disabled so q/Esc cannot dismiss the prompt silently.
func newChoiceList(label string, items []string, def int) list.Model {
	lis := list.New(choiceItems(items), list.NewDefaultDelegate(), 80, len(items)+1)
	lis.Title = label
	lis.SetShowStatusBar(false)
	lis.SetShowPagination(false)
	lis.SetShowHelp(false)
	lis.SetFilteringEnabled(false)
	lis.DisableQuitKeybindings()
	lis.Select(def)
	return lis
}

// choicePrompt is the model behind askChoice: a single-select list. Enter
// picks the highlighted item, Ctrl+C aborts.
type choicePrompt struct {
	list    list.Model
	aborted bool
}

func (m choicePrompt) Init() tea.Cmd { return nil }

func (m choicePrompt) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.aborted = true
			return m, tea.Quit
		case "enter":
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		// Keep the list compact: title + one line per item.
		h := len(m.list.Items()) + 1
		if msg.Height < h {
			h = msg.Height
		}
		m.list.SetWidth(msg.Width)
		m.list.SetHeight(h)
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m choicePrompt) View() string { return m.list.View() }

// pickerResource identifies one bar; index into pickerPrompt.pct.
type pickerResource int

const (
	resCores pickerResource = iota
	resMemory
	resDisk
)

// resourceSnapshot is a node's capacity and current allocation. The future
// provisioning flow builds it from the node (pvesh node status + per-VM qm
// configs over the goph client); the picker is data-agnostic.
type resourceSnapshot struct {
	coresTotal, coresUsed         int64
	memoryTotalMiB, memoryUsedMiB int64
	diskTotalGiB, diskUsedGiB     int64
}

// resourceSelection is the confirmed pick in Proxmox units:
// cores as total vcores (integration sets sockets=1 so cores==total),
// memory in MiB, disk in GiB.
type resourceSelection struct {
	cores     int64
	memoryMiB int64
	diskGiB   int64
}

// diskSize returns the disk size in qm config/volume form, e.g. "32G".
func (s resourceSelection) diskSize() string { return fmt.Sprintf("%dG", s.diskGiB) }

// pickerPrompt is the model behind runResourcePicker: three bars, one per
// resource, selecting a % of the FREE resources (default 50% each).
type pickerPrompt struct {
	snap    resourceSnapshot
	focus   pickerResource // starts resCores
	pct     [3]int         // % of free, 0..100, all start 50
	done    bool           // Enter pressed
	aborted bool           // Ctrl+C pressed
}

func newPickerPrompt(snap resourceSnapshot) pickerPrompt {
	return pickerPrompt{snap: snap, focus: resCores, pct: [3]int{50, 50, 50}}
}

// totals returns the resource's capacity and used amounts.
func (m pickerPrompt) totals(r pickerResource) (total, used int64) {
	switch r {
	case resCores:
		total, used = m.snap.coresTotal, m.snap.coresUsed
	case resMemory:
		total, used = m.snap.memoryTotalMiB, m.snap.memoryUsedMiB
	case resDisk:
		total, used = m.snap.diskTotalGiB, m.snap.diskUsedGiB
	}
	return total, used
}

// bar returns the resource's total capacity, used amount and selected amount.
func (m pickerPrompt) bar(r pickerResource) (total, used, amount int64) {
	total, used = m.totals(r)
	return total, used, m.amount(r)
}

// free = max(0, total-used). Overcommit (used>total) yields 0.
func (m pickerPrompt) free(r pickerResource) int64 {
	total, used := m.totals(r)
	return max(0, total-used)
}

// amount = ceil(free*pct/100): every pct>=1 with free>=1 yields >=1,
// monotone in pct, pct 100 exactly equals free, pct 0 yields 0. Never
// exceeds free.
func (m pickerPrompt) amount(r pickerResource) int64 {
	return int64(math.Ceil(float64(m.free(r)) * float64(m.pct[r]) / 100))
}

func (m pickerPrompt) selection() resourceSelection {
	return resourceSelection{
		cores:     m.amount(resCores),
		memoryMiB: m.amount(resMemory),
		diskGiB:   m.amount(resDisk),
	}
}

func (m pickerPrompt) Init() tea.Cmd { return nil }

// Update handles the picker keys: ↑/↓ switch the focused bar, ←/→ move the
// selection in 1%-of-free steps (no-ops on a fully allocated resource),
// Enter confirms, Ctrl+C aborts. Esc/q deliberately do nothing — only
// Ctrl+C aborts a prompt (same convention as the other prompts).
func (m pickerPrompt) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.aborted = true
			return m, tea.Quit
		case "enter":
			m.done = true
			return m, tea.Quit
		case "up":
			m.focus = (m.focus + 2) % 3
		case "down":
			m.focus = (m.focus + 1) % 3
		case "left":
			if m.free(m.focus) > 0 && m.pct[m.focus] > 0 {
				m.pct[m.focus]--
			}
		case "right":
			if m.free(m.focus) > 0 && m.pct[m.focus] < 100 {
				m.pct[m.focus]++
			}
		}
	}
	return m, nil
}

func (m pickerPrompt) View() string {
	var b strings.Builder
	b.WriteString("Resource allocation — select % of the node's free resources\n")
	b.WriteString("←/→ 1% of free · ↑/↓ switch · Enter confirm · Ctrl+C abort\n\n")
	for r := resCores; r <= resDisk; r++ {
		b.WriteString(m.barRow(r))
		b.WriteByte('\n')
	}
	sel := m.selection()
	fmt.Fprintf(&b, "\nSelection: %d vcores · %d MiB RAM · %d MiB disk\n", sel.cores, sel.memoryMiB, sel.diskGiB*1024)
	return b.String()
}

// barRow renders one resource line: focus marker + padded label + bar + stats.
func (m pickerPrompt) barRow(r pickerResource) string {
	names := [3]string{"CPU", "Memory", "Disk"}
	name := fmt.Sprintf("%-6s", names[r])
	if m.focus == r {
		name = pickerFocusStyle.Render("▸ " + name)
	} else {
		name = "  " + name
	}
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte(' ')
	total, used, amount := m.bar(r)
	allocLen := pickerBarLen(used, total)
	selLen := min(pickerBarLen(amount, total), pickerBarWidth-allocLen)
	b.WriteString(pickerAllocStyle.Render(strings.Repeat("█", allocLen)))
	b.WriteString(pickerSelStyle.Render(strings.Repeat("█", selLen)))
	b.WriteString(pickerRestStyle.Render(strings.Repeat("░", pickerBarWidth-allocLen-selLen)))
	unit := ""
	switch r {
	case resMemory:
		unit = " MiB"
	case resDisk:
		unit = " GiB"
	}
	fmt.Fprintf(&b, "  %d/%d%s used · %d free · %d%%", used, total, unit, m.free(r), m.pct[r])
	return b.String()
}

// runResourcePicker runs the picker as one Bubble Tea program (reuses
// runPrompt — no alt screen, the final frame with the chosen numbers stays
// in the terminal transcript) and returns the confirmed selection. Ctrl+C
// or program exit without Enter => errInterrupted.
func runResourcePicker(snap resourceSnapshot) (resourceSelection, error) {
	final, err := runPrompt(newPickerPrompt(snap))
	if err != nil {
		return resourceSelection{}, err
	}
	m := final.(pickerPrompt)
	if m.aborted || !m.done {
		return resourceSelection{}, errInterrupted
	}
	return m.selection(), nil
}

// pickerBarWidth is the rendered length of one resource bar.
const pickerBarWidth = 40

var (
	pickerAllocStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#B39DDB")) // light purple backdrop = allocated
	pickerSelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C4DFF")) // vivid purple = user's selection
	pickerRestStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#3A3A4A")) // dim = unselected free
	pickerFocusStyle = lipgloss.NewStyle().Bold(true)                            // focused row label
)

// pickerBarLen converts a value to bar segments, rounded to the nearest
// segment and clamped to [0, pickerBarWidth]. total<=0 yields 0.
func pickerBarLen(v, total int64) int {
	if total <= 0 {
		return 0
	}
	l := int(float64(v)/float64(total)*pickerBarWidth + 0.5)
	return min(pickerBarWidth, max(0, l))
}
