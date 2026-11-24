package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

func writeFileAt(t *testing.T, path, content string) {
	t.Helper()
	writeBytesAt(t, path, []byte(content))
}

func writeBytesAt(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func cp1251(t *testing.T, s string) []byte {
	t.Helper()
	b, err := charmap.Windows1251.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "internal/agent/agent.go", true},
		{"*.go", "notes.txt", false},
		{"**/*.go", "main.go", true},
		{"**/*.go", "internal/agent/agent.go", true},
		{"src/**/*.go", "src/a.go", true},
		{"src/**/*.go", "src/deep/a.go", true},
		{"src/**/*.go", "other/a.go", false},
		{"src/*.go", "src/deep/a.go", true},
		{"a?.go", "ab.go", true},
		{"a?.go", "abc.go", false},
		{"*.{md,txt}", "README.md", true},
		{"*.{md,txt}", "README.go", false},
		{"[abc].go", "b.go", true},
		{"[abc].go", "d.go", false},
		{"main.go", "main.go", true},
		{"main.go", "internal/main.go", false},
	}
	for _, c := range cases {
		got, err := globMatch(c.pattern, c.path)
		if err != nil || got != c.want {
			t.Errorf("globMatch(%q, %q) = (%v, %v), want %v", c.pattern, c.path, got, err, c.want)
		}
	}
}

func TestGlobMatchRejectsOpenBracket(t *testing.T) {
	if _, err := globMatch("[a", "a.go"); err == nil {
		t.Fatal("an unclosed [ must be reported, not silently matched")
	}
}

func TestListFiles(t *testing.T) {
	ws := t.TempDir()
	writeFileAt(t, filepath.Join(ws, "main.go"), "package main\n")
	writeFileAt(t, filepath.Join(ws, "internal", "agent", "agent.go"), "package agent\n")
	writeFileAt(t, filepath.Join(ws, "internal", "agent", "deep", "x.go"), "package deep\n")
	writeFileAt(t, filepath.Join(ws, "notes.txt"), "notes\n")
	writeFileAt(t, filepath.Join(ws, "logo.png"), "\x89PNG")

	for _, pattern := range []string{"*.go", "**/*.go"} {
		got := ListFiles(ws, pattern, ".")
		for _, want := range []string{"main.go", "internal/agent/agent.go", "internal/agent/deep/x.go"} {
			if !strings.Contains(got, want) {
				t.Errorf("ListFiles(%q) misses %s:\n%s", pattern, want, got)
			}
		}
		if strings.Contains(got, "notes.txt") {
			t.Errorf("ListFiles(%q) returned a txt file:\n%s", pattern, got)
		}
	}

	if got := ListFiles(ws, "agent.go", "."); !strings.Contains(got, "internal/agent/agent.go") {
		t.Errorf("a bare file name should find the file at any depth:\n%s", got)
	}
	if got := ListFiles(ws, "internal/**/*.go", "."); !strings.Contains(got, "agent.go") || strings.Contains(got, "main.go") {
		t.Errorf("unexpected result for internal/**/*.go:\n%s", got)
	}
	if got := ListFiles(ws, "*", "."); !strings.Contains(got, "notes.txt") || strings.Contains(got, "logo.png") {
		t.Errorf("the default listing should skip binaries:\n%s", got)
	}
	if got := ListFiles(ws, "[", "."); !strings.HasPrefix(got, "Error: Invalid glob") {
		t.Errorf("invalid glob = %q", got)
	}
	if got := ListFiles(ws, "*", filepath.Join("..", "elsewhere")); !strings.Contains(got, "outside the project directory") {
		t.Errorf("path outside the workspace = %q", got)
	}
}

func TestListFilesStopsAtBudget(t *testing.T) {
	ws := t.TempDir()
	for i := 0; i < 300; i++ {
		writeFileAt(t, filepath.Join(ws, "pkg", strings.Repeat("d", 1+i%5), "file_with_a_long_name_"+strings.Repeat("x", 20)+string(rune('a'+i%26))+string(rune('a'+i/26))+".go"), "x")
	}
	got := ListFiles(ws, "*.go", ".")
	if len(got) > maxOutputChars+200 {
		t.Fatalf("listing is %d chars, budget is %d", len(got), maxOutputChars)
	}
	if !strings.Contains(got, "more files not shown") {
		t.Fatalf("truncated listing must say so:\n%s", got)
	}
}
