package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	styleInputText = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	styleCursor    = lipgloss.NewStyle().Background(lipgloss.Color("7")).Foreground(lipgloss.Color("0"))
	styleErr       = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

// input is a single-line text input with inline gray completion.
type input struct {
	value      []rune
	cursor     int
	completion string // full suggested command; the suffix is rendered gray
	errorText  string
	focused    bool
	width      int
}

func newInput() *input { return &input{} }

func (i *input) Value() string { return string(i.value) }
func (i *input) Cursor() int   { return i.cursor }

func (i *input) SetValue(s string) {
	i.value = []rune(s)
	i.cursor = len(i.value)
}

func (i *input) Reset() {
	i.value = nil
	i.cursor = 0
	i.completion = ""
	i.errorText = ""
}

func (i *input) Focus() { i.focused = true }
func (i *input) Blur()  { i.focused = false }

func (i *input) SetWidth(w int) { i.width = w }

func (i *input) SetCompletion(c string) { i.completion = c }
func (i *input) SetError(e string)      { i.errorText = e }
func (i *input) Error() string          { return i.errorText }

// Update handles editing keys. It is expected to be called only while focused.
func (i *input) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.Type {
	case tea.KeyRunes:
		i.insert(key.Runes)
		i.errorText = ""
	case tea.KeySpace:
		i.insert([]rune{' '})
		i.errorText = ""
	case tea.KeyBackspace:
		if i.cursor > 0 {
			i.value = append(i.value[:i.cursor-1], i.value[i.cursor:]...)
			i.cursor--
		}
		i.errorText = ""
	case tea.KeyDelete:
		if i.cursor < len(i.value) {
			i.value = append(i.value[:i.cursor], i.value[i.cursor+1:]...)
		}
		i.errorText = ""
	case tea.KeyLeft, tea.KeyCtrlB:
		if i.cursor > 0 {
			i.cursor--
		}
	case tea.KeyRight, tea.KeyCtrlF:
		if i.cursor < len(i.value) {
			i.cursor++
		}
	case tea.KeyHome, tea.KeyCtrlA:
		i.cursor = 0
	case tea.KeyEnd, tea.KeyCtrlE:
		i.cursor = len(i.value)
	case tea.KeyTab:
		i.acceptCompletion()
	}
	return nil
}

func (i *input) insert(runes []rune) {
	if len(runes) == 0 {
		return
	}
	i.value = append(i.value, make([]rune, len(runes))...)
	copy(i.value[i.cursor+len(runes):], i.value[i.cursor:])
	copy(i.value[i.cursor:], runes)
	i.cursor += len(runes)
}

func (i *input) acceptCompletion() {
	if i.completion == "" {
		return
	}
	cur := i.Value()
	if i.cursor == len(cur) && len(i.completion) > len(cur) {
		i.insert([]rune(i.completion[len(cur):]))
	}
	i.completion = ""
}

func (i *input) View() string {
	v := i.value
	cursor := i.cursor
	if cursor > len(v) {
		cursor = len(v)
	}
	if cursor < 0 {
		cursor = 0
	}
	before := string(v[:cursor])
	var cur, after string
	if cursor < len(v) {
		cur = string(v[cursor])
		after = string(v[cursor+1:])
	} else {
		cur = " "
		after = ""
	}

	var b strings.Builder
	b.WriteString(styleInputText.Render(before))
	if i.focused {
		b.WriteString(styleCursor.Render(cur))
	} else {
		b.WriteString(styleInputText.Render(cur))
	}
	b.WriteString(styleInputText.Render(after))

	if i.completion != "" && cursor == len(v) {
		if len(i.completion) > len(string(v)) {
			suffix := i.completion[len(string(v)):]
			b.WriteString(styleHint.Render(suffix))
		}
	}
	return b.String()
}
