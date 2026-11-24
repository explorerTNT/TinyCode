package tools

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func TestSyntaxCheckGo(t *testing.T) {
	if !hasBinary("gofmt") {
		t.Skip("gofmt is not installed")
	}
	ws := t.TempDir()
	writeFileAt(t, filepath.Join(ws, "ok.go"), "package main\n\nfunc main() {}\n")
	writeFileAt(t, filepath.Join(ws, "ugly.go"), "package main\nfunc  main() {}\n")
	writeFileAt(t, filepath.Join(ws, "bad.go"), "package main\n\nfunc main() {\n")

	if got := SyntaxCheck(filepath.Join(ws, "ok.go")); got != "\nCHECK (gofmt): syntax OK" {
		t.Errorf("valid file = %q", got)
	}
	if got := SyntaxCheck(filepath.Join(ws, "ugly.go")); !strings.Contains(got, "not gofmt-formatted") || !strings.Contains(got, "gofmt -w") {
		t.Errorf("unformatted file = %q", got)
	}
	if got := SyntaxCheck(filepath.Join(ws, "bad.go")); !strings.Contains(got, "SYNTAX ERROR") || !strings.Contains(got, "bad.go") {
		t.Errorf("broken file = %q", got)
	}
}

func TestSyntaxCheckJSON(t *testing.T) {
	ws := t.TempDir()
	writeFileAt(t, filepath.Join(ws, "ok.json"), "{\"a\": [1, 2]}\n")
	writeFileAt(t, filepath.Join(ws, "bad.json"), "{\"a\": 1,}")
	writeFileAt(t, filepath.Join(ws, "extra.json"), "{} {}")
	writeFileAt(t, filepath.Join(ws, "notes.txt"), "{not json at all")

	if got := SyntaxCheck(filepath.Join(ws, "ok.json")); got != "\nCHECK (json): syntax OK" {
		t.Errorf("valid json = %q", got)
	}
	if got := SyntaxCheck(filepath.Join(ws, "bad.json")); !strings.Contains(got, "SYNTAX ERROR") {
		t.Errorf("invalid json = %q", got)
	}
	if got := SyntaxCheck(filepath.Join(ws, "extra.json")); !strings.Contains(got, "extra data") {
		t.Errorf("trailing data = %q", got)
	}
	if got := SyntaxCheck(filepath.Join(ws, "notes.txt")); got != "" {
		t.Errorf("unknown type must not be checked: %q", got)
	}
}

func TestSyntaxCheckPython(t *testing.T) {
	if !hasBinary("python") && !hasBinary("python3") {
		t.Skip("python is not installed")
	}
	ws := t.TempDir()
	writeFileAt(t, filepath.Join(ws, "ok.py"), "def main():\n    pass\n")
	writeFileAt(t, filepath.Join(ws, "bad.py"), "def main(:\n    pass\n")

	if got := SyntaxCheck(filepath.Join(ws, "ok.py")); !strings.Contains(got, "syntax OK") {
		t.Errorf("valid python = %q", got)
	}
	if got := SyntaxCheck(filepath.Join(ws, "bad.py")); !strings.Contains(got, "SYNTAX ERROR") || !strings.Contains(got, "line 1") {
		t.Errorf("broken python = %q", got)
	}
}

func TestSyntaxCheckPowerShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell parser needs Windows")
	}
	ws := t.TempDir()
	writeFileAt(t, filepath.Join(ws, "ok.ps1"), "function Get-Thing {\n    param($x)\n    return $x\n}\n")
	writeFileAt(t, filepath.Join(ws, "bad.ps1"), "function Get-Thing {\n    return $x\n")

	if got := SyntaxCheck(filepath.Join(ws, "ok.ps1")); !strings.Contains(got, "syntax OK") {
		t.Errorf("valid script = %q", got)
	}
	if got := SyntaxCheck(filepath.Join(ws, "bad.ps1")); !strings.Contains(got, "SYNTAX ERROR") {
		t.Errorf("broken script = %q", got)
	}
}

func TestSyntaxCheckCanSwitchOff(t *testing.T) {
	defer func(on bool) { SyntaxCheckEnabled = on }(SyntaxCheckEnabled)
	SyntaxCheckEnabled = false
	if got := SyntaxCheck(filepath.Join(t.TempDir(), "x.json")); got != "" {
		t.Fatalf("disabled check reported %q", got)
	}
}

// The model breaks a file with an edit and learns about it from the same
// tool result.
func TestEditReportsSyntaxError(t *testing.T) {
	if !hasBinary("gofmt") {
		t.Skip("gofmt is not installed")
	}
	ws := t.TempDir()
	writeFileAt(t, filepath.Join(ws, "a.go"), "package main\n\nfunc main() {\n\treturn\n}\n")

	got := EditFile(ws, "a.go", "", "\treturn(\n", float64(4), float64(4))
	if !strings.HasPrefix(got, "Successfully edited") || !strings.Contains(got, "SYNTAX ERROR") {
		t.Fatalf("the broken edit must be reported: %q", got)
	}
	got = EditFile(ws, "a.go", "", "\treturn\n", float64(4), float64(4))
	if !strings.Contains(got, "syntax OK") {
		t.Fatalf("the fixed edit must be confirmed: %q", got)
	}
	if got := WriteFile(ws, "cfg.json", "{\"broken\": }"); !strings.Contains(got, "SYNTAX ERROR") {
		t.Fatalf("write_file must check too: %q", got)
	}
}
