package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

func mapWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	writeFileAt(t, filepath.Join(ws, "go.mod"), "module demo\n")
	writeFileAt(t, filepath.Join(ws, "main.go"), "package main\n\nfunc main() {}\n")
	writeFileAt(t, filepath.Join(ws, "internal", "user", "user.go"), "package user\n\ntype User struct{}\n\nfunc GetUserName() string { return \"\" }\n")
	writeFileAt(t, filepath.Join(ws, "internal", "user", "user_test.go"), "package user\n\nfunc TestUser(t *testing.T) {}\n")
	writeFileAt(t, filepath.Join(ws, "internal", "auth", "auth.py"), "def login(name):\n    pass\n\nclass Session:\n    pass\n")
	writeFileAt(t, filepath.Join(ws, "web", "src", "app.js"), "export function render() {}\nexport const SIZE = 3;\n")
	writeFileAt(t, filepath.Join(ws, "node_modules", "x", "y.js"), "function ignored() {}\n")
	writeFileAt(t, filepath.Join(ws, "logo.png"), "\x89PNG")
	return ws
}

func TestRepoMapText(t *testing.T) {
	got := RepoMapText(mapWorkspace(t), 4000)
	for _, want := range []string{
		"internal/user/ (1 go)", "internal/auth/ (1 py)", "web/src/ (1 js)",
		"root files: go.mod, logo.png, main.go",
		"main.go: main", "internal/user/user.go: User, GetUserName",
		"internal/auth/auth.py: login, Session", "web/src/app.js: render, SIZE",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("map misses %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"TestUser", "node_modules", "ignored"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("map contains %q:\n%s", unwanted, got)
		}
	}
}

func TestRepoMapBudget(t *testing.T) {
	ws := mapWorkspace(t)
	tight := RepoMapText(ws, 160)
	if len(tight) > 160 {
		t.Fatalf("map is %d chars, budget is 160:\n%s", len(tight), tight)
	}
	if !strings.HasPrefix(tight, "directories (source files by extension):") || !strings.Contains(tight, "root files") {
		t.Fatalf("a tight budget must keep the layout:\n%s", tight)
	}
	if strings.Contains(tight, "names (file") {
		t.Fatalf("a tight budget must drop the name outline first:\n%s", tight)
	}
	if RepoMapText(ws, 0) != "" {
		t.Fatal("a zero budget disables the map")
	}
	if RepoMapText(t.TempDir(), 4000) != "" {
		t.Fatal("an empty project has no map")
	}
}

func TestSuggestIdentifier(t *testing.T) {
	ws := mapWorkspace(t)
	cases := map[string]string{
		"getUserNam":               "GetUserName",
		"getUserNme":               "GetUserName",
		"Sesion":                   "Session",
		"renderPage":               "",
		"completelyDifferentThing": "",
		"not a name":               "",
	}
	for in, want := range cases {
		got := SuggestIdentifier(ws, in)
		if want == "" && got != "" {
			t.Errorf("SuggestIdentifier(%q) = %q, want nothing", in, got)
		}
		if want != "" && !strings.Contains(got, "'"+want+"'") {
			t.Errorf("SuggestIdentifier(%q) = %q, want %q", in, got, want)
		}
	}
	if got := SuggestIdentifier(ws, "getusernma"); !strings.Contains(got, "internal/user/user.go") {
		t.Errorf("the hint must name the file: %q", got)
	}
}

// A search that finds nothing answers with the right spelling instead of a
// bare "no matches".
func TestSearchSuggestsSpelling(t *testing.T) {
	ws := mapWorkspace(t)
	got := SearchFiles(ws, "getUsrName", ".", "")
	if !strings.HasPrefix(got, "No matches") || !strings.Contains(got, "Did you mean 'GetUserName' (internal/user/user.go)") {
		t.Fatalf("search result = %q", got)
	}
	if got := SearchFiles(ws, "GetUserName", ".", ""); !strings.Contains(got, "user.go:5") {
		t.Fatalf("a correct name must find its definition: %q", got)
	}
}

func TestLevenshtein(t *testing.T) {
	cases := map[[2]string]int{{"abc", "abc"}: 0, {"abc", "abd"}: 1, {"kitten", "sitting"}: 3, {"", "ab"}: 2, {"ab", ""}: 2}
	for pair, want := range cases {
		if got := levenshtein(pair[0], pair[1]); got != want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", pair[0], pair[1], got, want)
		}
	}
}

func TestIsTestFile(t *testing.T) {
	for rel, want := range map[string]bool{
		"a_test.go": true, "app.spec.ts": true, "tests/test_x.py": true, "conftest.py": true,
		"a.go": false, "contest.py": false, "x.py": false,
	} {
		if got := isTestFile(rel); got != want {
			t.Errorf("isTestFile(%q) = %v, want %v", rel, got, want)
		}
	}
}
