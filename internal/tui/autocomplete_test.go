package tui

import "testing"

func testAutocomplete() *autocomplete {
	a := &autocomplete{fr: newFrecency()}
	a.build = func(t Trigger) []ACOption {
		return []ACOption{
			{Display: "file.go", Value: "@file.go", Path: "file.go"},
			{Display: "folder/", Value: "@folder/", Path: "folder", IsDir: true},
			{Display: "readme.md", Value: "@readme.md", Path: "readme.md"},
		}
	}
	return a
}

func TestUpdateTriggers(t *testing.T) {
	a := testAutocomplete()

	a.update("hello ", 6)
	if a.active() {
		t.Fatalf("no trigger should be active")
	}

	a.update("@", 1)
	if a.visible != triggerAt {
		t.Fatalf("bare @ should open mention, got %q", a.visible)
	}

	a.update("@fi", 3)
	if a.query != "fi" {
		t.Fatalf("query = %q, want fi", a.query)
	}

	a.update("/", 1)
	if a.visible != triggerSlash {
		t.Fatalf("bare / should open slash, got %q", a.visible)
	}

	a.update("/help", 5)
	if a.visible != triggerSlash || a.query != "help" {
		t.Fatalf("slash query = %q visible=%q", a.query, a.visible)
	}

	a.update("/help extra", 10)
	if a.active() {
		t.Fatalf("slash with args should close menu")
	}
}

func TestUpdateMentionDetection(t *testing.T) {
	a := testAutocomplete()

	a.update("look at @src/ma", 15)
	if a.visible != triggerAt {
		t.Fatalf("expected @ mention, got %q", a.visible)
	}
	if a.index != 8 {
		t.Fatalf("index = %d, want 8", a.index)
	}
	if a.query != "src/ma" {
		t.Fatalf("query = %q, want src/ma", a.query)
	}

	a.update("email a@b.c", 10)
	if a.active() {
		t.Fatalf("@ without leading space should not trigger")
	}
}

func TestRankPrefixBoost(t *testing.T) {
	a := testAutocomplete()
	a.update("@fi", 3)
	if len(a.options) == 0 {
		t.Fatalf("no options")
	}
	if a.options[0].Value != "@file.go" {
		t.Fatalf("top option = %q, want @file.go", a.options[0].Value)
	}
}

func TestRankFrecencyBoost(t *testing.T) {
	a := testAutocomplete()
	a.fr.touch("readme.md")
	a.update("@", 1)
	if len(a.options) < 2 {
		t.Fatalf("expected >= 2 options, got %d", len(a.options))
	}
	if a.options[0].Value != "@readme.md" {
		t.Fatalf("frecency should rank readme first, got %q", a.options[0].Value)
	}
}

func TestMoveWraps(t *testing.T) {
	a := testAutocomplete()
	a.update("@", 1)
	n := len(a.options)
	if n == 0 {
		t.Fatalf("no options")
	}
	a.move(-1)
	if a.selected != n-1 {
		t.Fatalf("wrap up: selected=%d want %d", a.selected, n-1)
	}
	a.move(1)
	if a.selected != 0 {
		t.Fatalf("wrap down: selected=%d want 0", a.selected)
	}
}
