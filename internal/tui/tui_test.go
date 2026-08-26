package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestResolveSlash(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/help", "/help", true},
		{"/session save foo", "/session save foo", true},
		{"/se", "/session", true},
		{"/plan", "/plan", true},
		{"/pla", "/plan", true},
		{"/nope", "", false},
		{"/", "", false},
		{"/ses x", "/session x", true},
	}
	for _, c := range cases {
		got, ok := resolveSlash(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("resolveSlash(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestInputInsertBackspace(t *testing.T) {
	i := newInput()
	i.Focus()
	for _, r := range "ab" {
		i.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if i.Value() != "ab" || i.cursor != 2 {
		t.Fatalf("after insert: value=%q cursor=%d", i.Value(), i.cursor)
	}
	i.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if i.Value() != "a" || i.cursor != 1 {
		t.Fatalf("after backspace: value=%q cursor=%d", i.Value(), i.cursor)
	}
}

func TestCompletion(t *testing.T) {
	i := newInput()
	i.Focus()
	i.SetValue("/se")
	i.SetCompletion("/session")
	if got := i.Value(); got != "/se" {
		t.Fatalf("value = %q", got)
	}
	i.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := i.Value(); got != "/session" {
		t.Fatalf("after tab: value = %q", got)
	}
	if i.completion != "" {
		t.Fatalf("completion not cleared: %q", i.completion)
	}
}
