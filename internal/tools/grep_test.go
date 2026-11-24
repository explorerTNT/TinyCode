package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

func searchTree(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	writeFileAt(t, filepath.Join(ws, "a.py"), "def foo():\n    pass\n")
	writeFileAt(t, filepath.Join(ws, "sub", "b.py"), "foo = 1\n")
	writeFileAt(t, filepath.Join(ws, "sub", "c.txt"), "foo bar\n")
	writeFileAt(t, filepath.Join(ws, ".hidden", "h.py"), "foo\n")
	writeFileAt(t, filepath.Join(ws, ".env"), "SECRET=foo\n")
	writeFileAt(t, filepath.Join(ws, "blob.bin"), "foo\n")
	return ws
}

func TestFallbackGrep(t *testing.T) {
	ws := searchTree(t)

	got := fallbackGrep("foo", ws, "")
	for _, want := range []string{"--- 3 matches ---", "a.py:1: def foo():", "sub/b.py:1: foo = 1", "sub/c.txt:1: foo bar"} {
		if !strings.Contains(got, want) {
			t.Errorf("result misses %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{".env", ".hidden", "blob.bin", "SECRET"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("result leaks %q:\n%s", unwanted, got)
		}
	}

	if got := fallbackGrep("foo", ws, "py"); !strings.Contains(got, "a.py:1") || !strings.Contains(got, "sub/b.py:1") || strings.Contains(got, "c.txt") {
		t.Errorf("include \"py\" should keep only python files:\n%s", got)
	}
	if got := fallbackGrep("foo", ws, "b.py"); !strings.Contains(got, "sub/b.py:1") || strings.Contains(got, "a.py") {
		t.Errorf("include \"b.py\" should keep only that file:\n%s", got)
	}
	if got := fallbackGrep("foo", ws, "sub/*.py"); !strings.Contains(got, "sub/b.py:1") || strings.Contains(got, "a.py") {
		t.Errorf("include \"sub/*.py\" should keep only files under sub:\n%s", got)
	}
	if got := fallbackGrep("nothing-here", ws, ""); !strings.HasPrefix(got, "No matches") {
		t.Errorf("no-match result = %q", got)
	}
}

func TestFallbackGrepStopsAtBudget(t *testing.T) {
	ws := t.TempDir()
	for i := 0; i < 40; i++ {
		dir := filepath.Join(ws, "pkg"+strings.Repeat("x", 1+i%5))
		writeFileAt(t, filepath.Join(dir, "file_"+strings.Repeat("n", 20)+".txt"),
			strings.Repeat("needle "+strings.Repeat("y", 150)+"\n", 5))
	}

	got := fallbackGrep("needle", ws, "")
	if len(got) > maxOutputChars+150 {
		t.Fatalf("result is %d chars, budget is %d", len(got), maxOutputChars)
	}
	if !strings.Contains(got, "more match(es) not shown") {
		t.Fatalf("truncated result must say so:\n...%s", got[len(got)-200:])
	}
	if !strings.Contains(got, "--- 19 matches ---") {
		t.Fatalf("matches beyond the budget should still be counted:\n%s", got[:40])
	}
}

// A pattern that starts with a dash is a search term, not an option, and
// paths are reported relative to the workspace like the fallback does.
func TestSearchFilesPatternStartingWithDash(t *testing.T) {
	if findRg() == "" {
		t.Skip("ripgrep is not installed")
	}
	ws := t.TempDir()
	writeFileAt(t, filepath.Join(ws, "sub", "notes.md"), "use the --verbose flag\n")
	if got := SearchFiles(ws, "--verbose", ".", ""); !strings.Contains(got, "sub/notes.md:1:use the --verbose flag") {
		t.Fatalf("dash-prefixed pattern not found:\n%s", got)
	}
}

func TestSearchFilesSingleFile(t *testing.T) {
	ws := t.TempDir()
	writeFileAt(t, filepath.Join(ws, "a.py"), "x = FOO\ny = foo\nz = 1\n")
	got := SearchFiles(ws, "foo", "a.py", "")
	if !strings.Contains(got, "--- 2 match(es) in a.py ---") || !strings.Contains(got, "a.py:2: y = foo") {
		t.Fatalf("unexpected result:\n%s", got)
	}
}

func TestSearchFilesErrors(t *testing.T) {
	ws := t.TempDir()
	if got := SearchFiles(ws, "a(", ".", ""); !strings.HasPrefix(got, "Error: Invalid regex") {
		t.Fatalf("invalid regex = %q", got)
	}
	if got := SearchFiles(ws, "a", filepath.Join("..", "elsewhere"), ""); !strings.Contains(got, "outside the project directory") {
		t.Fatalf("path outside the workspace = %q", got)
	}
	if got := SearchFiles(ws, "a", "missing", ""); !strings.HasPrefix(got, "Error: Path not found") {
		t.Fatalf("missing path = %q", got)
	}
}

func TestHiddenPath(t *testing.T) {
	for rel, want := range map[string]bool{
		"a.go": false, ".env": true, ".github/x.yml": true, "internal/.env": true, "internal/.config/x": true,
	} {
		if got := hiddenPath(rel); got != want {
			t.Errorf("hiddenPath(%q) = %v, want %v", rel, got, want)
		}
	}
}
