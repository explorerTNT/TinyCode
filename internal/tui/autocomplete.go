package tui

import (
	"sort"
	"strings"

	"github.com/explorerTNT/TinyCode/internal/agent"
)

// Trigger identifies which autocomplete menu (if any) is active.
type Trigger string

const (
	triggerNone  Trigger = ""
	triggerSlash Trigger = "/"
	triggerAt    Trigger = "@"
)

// ACOption is a single autocomplete candidate.
type ACOption struct {
	Display     string // text shown in the menu
	Value       string // text inserted on completion
	Description string // optional secondary text
	Path        string // for frecency boosting; "" disables the boost
	IsDir       bool
}

// autocomplete drives the @ and / mention/command menus.
type autocomplete struct {
	visible  Trigger
	index    int // rune index of the trigger character
	query    string
	selected int
	options  []ACOption
	fr       *frecency
	build    func(Trigger) []ACOption
}

func (a *autocomplete) active() bool { return a.visible != triggerNone }

func (a *autocomplete) hide() {
	a.visible = triggerNone
	a.index = 0
	a.query = ""
	a.selected = 0
	a.options = nil
}

func (a *autocomplete) open(t Trigger, index int) {
	a.visible = t
	a.index = index
	a.selected = 0
}

func (a *autocomplete) move(delta int) {
	if len(a.options) == 0 {
		return
	}
	a.selected = (a.selected + delta) % len(a.options)
	if a.selected < 0 {
		a.selected += len(a.options)
	}
}

func (a *autocomplete) selectedOption() (ACOption, bool) {
	if a.selected < 0 || a.selected >= len(a.options) {
		return ACOption{}, false
	}
	return a.options[a.selected], true
}

// update recomputes the menu state from the current input value and cursor
// position (both measured in runes).
func (a *autocomplete) update(value string, cursor int) {
	runes := []rune(value)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}

	if a.visible != triggerNone {
		if a.canKeep(runes, cursor) {
			a.query = string(runes[a.index+1 : cursor])
			a.refilter()
			return
		}
		a.hide()
	}

	if cursor >= 1 && runes[0] == '/' {
		if !containsSpace(runes[:cursor]) {
			a.open(triggerSlash, 0)
			a.query = string(runes[1:cursor])
			a.refilter()
			return
		}
	}

	if idx := mentionTrigger(runes, cursor); idx >= 0 {
		a.open(triggerAt, idx)
		a.query = string(runes[idx+1 : cursor])
		a.refilter()
	}
}

func (a *autocomplete) canKeep(runes []rune, cursor int) bool {
	if a.index >= len(runes) {
		return false
	}
	if runes[a.index] != rune(a.visible[0]) {
		return false
	}
	if cursor <= a.index {
		return false
	}
	return !containsSpace(runes[a.index+1 : cursor])
}

func (a *autocomplete) refilter() {
	a.options = a.rank(a.build(a.visible))
	a.selected = 0
}

func (a *autocomplete) rank(cands []ACOption) []ACOption {
	type scored struct {
		o ACOption
		v float64
	}
	var out []scored
	for _, o := range cands {
		key := stripLineRange(o.Value)
		base, ok := fuzzyScore(key, a.query)
		if !ok {
			continue
		}
		if a.visible == triggerAt && base < 0.5 {
			continue
		}
		v := base
		if strings.HasPrefix(strings.ToLower(key), strings.ToLower(string(a.visible)+a.query)) {
			v *= 2
		}
		if o.Path != "" {
			v *= 1 + a.fr.score(o.Path)
		}
		out = append(out, scored{o, v})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].v > out[j].v })
	const maxOptions = 10
	if len(out) > maxOptions {
		out = out[:maxOptions]
	}
	res := make([]ACOption, len(out))
	for i, s := range out {
		res[i] = s.o
	}
	return res
}

// mentionTrigger returns the rune index of the '@' that starts the mention
// ending at cursor, or -1 when no mention is being typed.
func mentionTrigger(runes []rune, cursor int) int {
	for i := cursor - 1; i >= 0; i-- {
		if isSpace(runes[i]) {
			return -1
		}
		if runes[i] == '@' {
			if i == 0 || isSpace(runes[i-1]) {
				return i
			}
			return -1
		}
	}
	return -1
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' }

func containsSpace(runes []rune) bool {
	for _, r := range runes {
		if isSpace(r) {
			return true
		}
	}
	return false
}

func stripLineRange(v string) string {
	if i := strings.IndexByte(v, '#'); i >= 0 {
		return v[:i]
	}
	return v
}

// acCandidates builds the candidate list for a trigger from the current
// model state: slash commands for "/", and the file-tree index for "@".
func (m *model) acCandidates(t Trigger) []ACOption {
	switch t {
	case triggerSlash:
		out := make([]ACOption, 0, len(agent.SlashCommands))
		for _, c := range agent.SlashCommands {
			out = append(out, ACOption{Display: c, Value: c})
		}
		return out
	case triggerAt:
		out := make([]ACOption, 0, len(m.fileIndex))
		for _, f := range m.fileIndex {
			o := ACOption{Path: f.rel, IsDir: f.isDir}
			if f.isDir {
				o.Display = f.rel + "/"
				o.Value = "@" + f.rel + "/"
			} else {
				o.Display = f.rel
				o.Value = "@" + f.rel
			}
			out = append(out, o)
		}
		return out
	}
	return nil
}

// View renders the menu block (empty when no menu is active).
func (a *autocomplete) View(width int) string {
	if a.visible == triggerNone || len(a.options) == 0 {
		return ""
	}
	var b strings.Builder
	for i, o := range a.options {
		line := o.Display
		if o.Description != "" {
			line += "  " + styleHint.Render(o.Description)
		}
		if i == a.selected {
			line = styleTreeSel.Width(width).Render(line)
		} else {
			line = styleTree.Render(line)
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	return b.String()
}
