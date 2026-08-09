package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestConfirmPrompt(t *testing.T) {
	cases := []struct {
		key    tea.KeyMsg
		def    bool
		want   bool
		done   bool
		abort  bool
		quits  bool
	}{
		{runeKey('y'), false, true, true, false, true},
		{runeKey('Y'), false, true, true, false, true},
		{runeKey('n'), true, false, true, false, true},
		{runeKey('N'), true, false, true, false, true},
		{tea.KeyMsg{Type: tea.KeyEnter}, true, true, true, false, true},
		{tea.KeyMsg{Type: tea.KeyEnter}, false, false, true, false, true},
		{tea.KeyMsg{Type: tea.KeyEsc}, true, true, true, false, true},
		{tea.KeyMsg{Type: tea.KeyEsc}, false, false, true, false, true},
		{tea.KeyMsg{Type: tea.KeyCtrlC}, false, false, false, true, true},
		{runeKey('x'), false, false, false, false, false},
	}
	for _, c := range cases {
		m := confirmPrompt{def: c.def}
		final, cmd := m.Update(c.key)
		got := final.(confirmPrompt)
		if got.answer != c.want {
			t.Errorf("key %q def %v: answer = %v, want %v", c.key, c.def, got.answer, c.want)
		}
		if got.done != c.done {
			t.Errorf("key %q def %v: done = %v, want %v", c.key, c.def, got.done, c.done)
		}
		if got.aborted != c.abort {
			t.Errorf("key %q def %v: aborted = %v, want %v", c.key, c.def, got.aborted, c.abort)
		}
		if (cmd != nil) != c.quits {
			t.Errorf("key %q def %v: quit cmd = %v, want %v", c.key, c.def, cmd != nil, c.quits)
		}
	}
}

func TestConfirmPromptViewShowsAnswer(t *testing.T) {
	m := confirmPrompt{label: "Delete?", def: false}
	if view := m.View(); strings.Contains(view, "yes") || strings.Contains(view, "no") {
		t.Errorf("unanswered view shows an answer: %q", view)
	}
	final, _ := m.Update(runeKey('y'))
	view := final.(confirmPrompt).View()
	if !strings.Contains(view, "[y/N]: yes") {
		t.Errorf("answered view = %q, want it to end in \"[y/N]: yes\"", view)
	}
}

func TestTextPromptEnterQuits(t *testing.T) {
	m := textPrompt{label: "host: ", input: newTextInput(false)}
	m.input.SetValue("root@host")
	final, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := final.(textPrompt)
	if cmd == nil {
		t.Errorf("enter: want quit cmd, got nil")
	}
	if got.aborted {
		t.Errorf("enter: aborted")
	}
	if got.input.Value() != "root@host" {
		t.Errorf("enter: value = %q, want %q", got.input.Value(), "root@host")
	}
}

func TestTextPromptCtrlCAborts(t *testing.T) {
	m := textPrompt{label: "host: ", input: newTextInput(false)}
	final, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := final.(textPrompt)
	if cmd == nil {
		t.Errorf("ctrl+c: want quit cmd, got nil")
	}
	if !got.aborted {
		t.Errorf("ctrl+c: not aborted")
	}
}

func TestTextPromptTypes(t *testing.T) {
	m := textPrompt{label: "host: ", input: newTextInput(false)}
	final, _ := m.Update(runeKey('p'))
	final, _ = final.(textPrompt).Update(runeKey('v'))
	got := final.(textPrompt)
	if got.input.Value() != "pv" {
		t.Errorf("typing: value = %q, want %q", got.input.Value(), "pv")
	}
	if got.aborted {
		t.Errorf("typing: aborted")
	}
}

func TestPasswordPromptMasksValue(t *testing.T) {
	m := textPrompt{label: "pw: ", input: newTextInput(true)}
	m.input.SetValue("secret")
	view := m.View()
	if strings.Contains(view, "secret") {
		t.Errorf("masked view leaks the value: %q", view)
	}
	if !strings.Contains(view, "*") {
		t.Errorf("masked view shows no mask: %q", view)
	}
	if m.input.Value() != "secret" {
		t.Errorf("masked value = %q, want %q", m.input.Value(), "secret")
	}
}

func TestChoicePromptKeys(t *testing.T) {
	items := []string{"Upload an ISO", "Delete an ISO", "List ISOs"}
	m := choicePrompt{list: newChoiceList("What do you want to do?", items, 0)}

	if idx := m.list.Index(); idx != 0 {
		t.Fatalf("initial cursor = %d, want 0", idx)
	}

	// Arrow down moves the cursor.
	final, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = final.(choicePrompt)
	if idx := m.list.Index(); idx != 1 {
		t.Fatalf("after down: cursor = %d, want 1", idx)
	}

	// 'q' must not dismiss the prompt (list quit bindings are disabled).
	final, cmd := m.Update(runeKey('q'))
	m = final.(choicePrompt)
	if cmd != nil {
		t.Fatalf("q: got quit cmd, list quit bindings should be disabled")
	}
	if m.aborted {
		t.Fatalf("q: marked aborted")
	}

	// Enter quits with the highlighted item.
	final, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = final.(choicePrompt)
	if cmd == nil {
		t.Fatalf("enter: want quit cmd, got nil")
	}
	if idx := m.list.Index(); idx != 1 {
		t.Fatalf("enter: cursor = %d, want 1", idx)
	}

	// Ctrl+C aborts.
	final, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = final.(choicePrompt)
	if cmd == nil {
		t.Fatalf("ctrl+c: want quit cmd, got nil")
	}
	if !m.aborted {
		t.Fatalf("ctrl+c: not aborted")
	}
}
