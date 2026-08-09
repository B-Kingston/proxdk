package main

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
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
