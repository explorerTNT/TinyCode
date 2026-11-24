package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"session_1750000000": "session_1750000000", // plain names keep their file
		"":                   "session",
		"con":                "_con",
		"nul":                "_nul",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
	// Different names that sanitise to the same text must not collide.
	if sanitizeName("a b") == sanitizeName("a_b") || sanitizeName("моя сессия") == sanitizeName("нова сессия") {
		t.Fatal("distinct session names share a file")
	}
}

func TestSessionSaveLoadRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	sm, err := newSessionManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	msgs := []Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "привет", Nudge: false}}
	path, err := sm.save("моя сессия", msgs, true, "m", "ws")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(path) != ".json" || filepath.Base(path) == "моя сессия.json" {
		t.Fatalf("unexpected file name %q", path)
	}
	data, err := sm.load("моя сессия")
	if err != nil || len(data.Messages) != 2 || !data.PlanMode || data.Model != "m" {
		t.Fatalf("load = (%+v, %v)", data, err)
	}
	list := sm.list()
	if len(list) != 1 || list[0].Name != "моя сессия" || list[0].Messages != 2 {
		t.Fatalf("list = %+v", list)
	}
	if sm.lastSession() != "моя сессия" {
		t.Fatalf("lastSession = %q", sm.lastSession())
	}
}

// Sessions saved before names were hashed live under the plain sanitised
// name and must still open.
func TestSessionLoadReadsLegacyFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	sm, err := newSessionManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(sm.sessionDir, "my_session.json")
	content := `{"name":"my session","model":"m","workspace":"w","timestamp":1700000000,` +
		`"messages":[{"role":"user","content":"hi"}]}`
	if err := os.WriteFile(legacy, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := sm.load("my session")
	if err != nil || len(data.Messages) != 1 {
		t.Fatalf("legacy session not loaded: (%+v, %v)", data, err)
	}
}
