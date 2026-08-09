package main

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// force truecolor on the default renderer so View tests can assert exact RGB
// escapes; the CLI never calls this and keeps environment-detected colors.
func init() {
	lipgloss.SetColorProfile(termenv.TrueColor)
}

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestConfirmPrompt(t *testing.T) {
	cases := []struct {
		key   tea.KeyMsg
		def   bool
		want  bool
		done  bool
		abort bool
		quits bool
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

// pickerSnap is the shared fixture: 16 cores/16384 MiB/100 GiB capacity,
// 6/6144/32 used => 10 cores, 10240 MiB, 68 GiB free.
func pickerSnap() resourceSnapshot {
	return resourceSnapshot{coresTotal: 16, coresUsed: 6, memoryTotalMiB: 16384, memoryUsedMiB: 6144, diskTotalGiB: 100, diskUsedGiB: 32}
}

func keyPress(m pickerPrompt, k tea.KeyMsg) pickerPrompt {
	final, _ := m.Update(k)
	return final.(pickerPrompt)
}

func TestPickerAmount(t *testing.T) {
	cases := []struct {
		r    pickerResource
		pct  int
		want int64
	}{
		{resCores, 0, 0}, {resCores, 1, 1}, {resCores, 50, 5}, {resCores, 100, 10},
		{resMemory, 0, 0}, {resMemory, 1, 103}, {resMemory, 50, 5120}, {resMemory, 100, 10240},
		{resDisk, 0, 0}, {resDisk, 1, 1}, {resDisk, 50, 34}, {resDisk, 100, 68},
	}
	for _, c := range cases {
		m := newPickerPrompt(pickerSnap())
		m.pct[c.r] = c.pct
		if got := m.amount(c.r); got != c.want {
			t.Errorf("resource %d pct %d: amount = %d, want %d", c.r, c.pct, got, c.want)
		}
	}

	// A fully allocated resource has no free capacity: 0 at every pct.
	full := resourceSnapshot{coresTotal: 8, coresUsed: 8}
	for _, pct := range []int{0, 1, 50, 100} {
		m := newPickerPrompt(full)
		m.pct[resCores] = pct
		if got := m.amount(resCores); got != 0 {
			t.Errorf("full resource pct %d: amount = %d, want 0", pct, got)
		}
	}

	// Zero total => amount 0.
	for _, r := range []pickerResource{resCores, resMemory, resDisk} {
		m := newPickerPrompt(resourceSnapshot{})
		m.pct[r] = 100
		if got := m.amount(r); got != 0 {
			t.Errorf("zero total resource %d: amount = %d, want 0", r, got)
		}
	}
}

func TestPickerKeys(t *testing.T) {
	m := newPickerPrompt(pickerSnap())
	if m.focus != resCores {
		t.Errorf("initial focus = %d, want resCores", m.focus)
	}
	for _, r := range []pickerResource{resCores, resMemory, resDisk} {
		if m.pct[r] != 50 {
			t.Errorf("initial pct[%d] = %d, want 50", r, m.pct[r])
		}
	}

	// Right moves the focused bar up 1% per press.
	for i := 0; i < 5; i++ {
		m = keyPress(m, tea.KeyMsg{Type: tea.KeyRight})
	}
	if m.pct[resCores] != 55 {
		t.Errorf("right x5: pct[cores] = %d, want 55", m.pct[resCores])
	}

	// Left goes down to 0 and clamps there (200 presses).
	for i := 0; i < 200; i++ {
		m = keyPress(m, tea.KeyMsg{Type: tea.KeyLeft})
	}
	if m.pct[resCores] != 0 {
		t.Errorf("left x200: pct[cores] = %d, want 0", m.pct[resCores])
	}
	if m.focus != resCores {
		t.Errorf("arrows moved focus to %d, want resCores", m.focus)
	}

	// Down/up cycle focus.
	m = keyPress(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.focus != resMemory {
		t.Errorf("down: focus = %d, want resMemory", m.focus)
	}
	m = keyPress(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.focus != resDisk {
		t.Errorf("down x2: focus = %d, want resDisk", m.focus)
	}
	m = keyPress(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.focus != resCores {
		t.Errorf("down x3: focus = %d, want resCores", m.focus)
	}
	m = keyPress(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.focus != resDisk {
		t.Errorf("up: focus = %d, want resDisk", m.focus)
	}

	// A fully allocated resource ignores arrows.
	full := newPickerPrompt(resourceSnapshot{coresTotal: 8, coresUsed: 8, memoryTotalMiB: 2048, memoryUsedMiB: 2048})
	full = keyPress(full, tea.KeyMsg{Type: tea.KeyRight})
	full = keyPress(full, tea.KeyMsg{Type: tea.KeyLeft})
	if full.pct[resCores] != 50 || full.pct[resMemory] != 50 {
		t.Errorf("full resource arrows moved pct: %v", full.pct)
	}

	// Esc and q do nothing: no quit, no done, no abort.
	for _, k := range []tea.KeyMsg{{Type: tea.KeyEsc}, runeKey('q')} {
		final, cmd := m.Update(k)
		got := final.(pickerPrompt)
		if cmd != nil {
			t.Errorf("key %q: got quit cmd, want none", k)
		}
		if got.done || got.aborted {
			t.Errorf("key %q: done=%v aborted=%v, want both false", k, got.done, got.aborted)
		}
	}

	// Enter confirms with the current selection (defaults: 50% of free).
	m = newPickerPrompt(pickerSnap())
	final, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := final.(pickerPrompt)
	if cmd == nil {
		t.Errorf("enter: want quit cmd, got nil")
	}
	if !got.done {
		t.Errorf("enter: not done")
	}
	want := resourceSelection{cores: 5, memoryMiB: 5120, diskGiB: 34}
	if sel := got.selection(); sel != want {
		t.Errorf("enter: selection = %+v, want %+v", sel, want)
	}

	// Ctrl+C aborts.
	final, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	got = final.(pickerPrompt)
	if cmd == nil {
		t.Errorf("ctrl+c: want quit cmd, got nil")
	}
	if !got.aborted {
		t.Errorf("ctrl+c: not aborted")
	}
}

func TestPickerView(t *testing.T) {
	view := newPickerPrompt(pickerSnap()).View()
	for _, want := range []string{
		"Selection: 5 vcores · 5120 MiB RAM · 34816 MiB disk",
		"6/16 used · 10 free · 50%",
		"6144/16384 MiB used",
		"32/100 GiB used",
		"▸",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q in:\n%s", want, view)
		}
	}

	// Every bar row renders exactly 40 segments.
	plain := ansiRegex.ReplaceAllString(view, "")
	for _, line := range strings.Split(plain, "\n") {
		seg := strings.Count(line, "█") + strings.Count(line, "░")
		if seg == 0 {
			continue
		}
		if seg != 40 {
			t.Errorf("bar row has %d segments, want 40: %q", seg, line)
		}
	}

	// Both colors are applied: light purple backdrop + vivid purple selection.
	for _, want := range []string{"38;2;179;157;219", "38;2;124;77;255"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing ANSI color %q in:\n%s", want, view)
		}
	}

	// Overcommit: no free resources, no panic, explicit "0 free".
	over := newPickerPrompt(resourceSnapshot{coresTotal: 4, coresUsed: 8, memoryTotalMiB: 1024, memoryUsedMiB: 4096, diskTotalGiB: 10, diskUsedGiB: 50}).View()
	if !strings.Contains(over, "0 free") {
		t.Errorf("overcommit view missing \"0 free\": %q", over)
	}

	// Zero snapshot: no panic, all-zero selection line.
	zero := newPickerPrompt(resourceSnapshot{}).View()
	if !strings.Contains(zero, "Selection: 0 vcores · 0 MiB RAM · 0 MiB disk") {
		t.Errorf("zero snapshot view = %q", zero)
	}
}

func TestSelectionDiskSize(t *testing.T) {
	if got := (resourceSelection{diskGiB: 34}).diskSize(); got != "34G" {
		t.Errorf("diskSize = %q, want \"34G\"", got)
	}
	if got := (resourceSelection{diskGiB: 1}).diskSize(); got != "1G" {
		t.Errorf("diskSize = %q, want \"1G\"", got)
	}
}
