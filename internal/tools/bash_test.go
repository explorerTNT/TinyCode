package tools

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSanitizeCmd(t *testing.T) {
	cases := map[string]string{
		"cd . && python app.py":                "python app.py",
		"python3 app.py":                       "python app.py",
		"pip3  install   requests > /dev/null": "pip install requests",
		"python app.py 2>&1 | cat":             "python app.py",
		"ls   -la":                             "ls -la",
		`echo "a   b"`:                         `echo "a   b"`,
		"cd . ; go build ./...;":               "go build ./...",
	}
	for in, want := range cases {
		if got := SanitizeCmd(in); got != want {
			t.Errorf("SanitizeCmd(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuoteExistingPaths(t *testing.T) {
	ws := t.TempDir()
	writeFileAt(t, filepath.Join(ws, "my file.txt"), "x")
	writeFileAt(t, filepath.Join(ws, "build", "x.o"), "x")

	cases := map[string]string{
		// The command itself is never quoted, even when a folder shares its name.
		"build":                 "build",
		"go build ./...":        `go "build" "./..."`,
		`type "my file.txt"`:    `type "my file.txt"`,
		"type my file.txt":      `type "my file.txt"`,
		"type missing file.txt": "type missing file.txt",
	}
	for in, want := range cases {
		if got := QuoteExistingPaths(ws, in); got != want {
			t.Errorf("QuoteExistingPaths(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckLinuxHints(t *testing.T) {
	flagged := []string{
		"cat /etc/passwd", "ls /home/user", "grep -r foo .", "bash -c 'ls'", "/bin/sh x.sh",
		"touch a.txt", "Get-Content /etc/hosts", "ls; rm -r build",
	}
	for _, c := range flagged {
		if checkLinuxHints(strings.ToLower(c)) == "" {
			t.Errorf("Linux command not detected: %q", c)
		}
	}

	clean := []string{
		"dotnet publish -c Release", "refresh -c auto",
		"Invoke-WebRequest https://example.com/home/page",
		"git log --oneline", `Get-Content C:\etc\hosts`, `python build\x.py`,
	}
	for _, c := range clean {
		if got := checkLinuxHints(strings.ToLower(c)); got != "" {
			t.Errorf("valid Windows command rejected as %q: %q", got, c)
		}
	}
}

func TestFormatOutputKeepsStartOnSuccess(t *testing.T) {
	out := strings.Repeat("head line\n", 40) + strings.Repeat("tail line\n", 600)
	got := formatOutput("", out, "", 0)
	if !strings.HasPrefix(got, "head line\n") || strings.Count(got, "tail line") >= 600 {
		t.Fatalf("success output should keep its start:\n%s", got[:300])
	}
	if !strings.Contains(got, "output clipped") || len(got) > maxOutputChars+60 {
		t.Fatalf("output not clipped to the budget (%d chars)", len(got))
	}
}

func TestFormatOutputKeepsEndOnFailure(t *testing.T) {
	out := strings.Repeat("progress\n", 900)
	got := formatOutput("python app.py", out, "error: boom\nTraceback (most recent call last):", 1)
	for _, want := range []string{"Exit code: 1", "error: boom", "showing the end", "HINT: The script crashed"} {
		if !strings.Contains(got, want) {
			t.Errorf("failure output misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "progress") && !strings.HasSuffix(strings.SplitN(got, "\n--- stderr ---", 2)[0], "progress") {
		t.Fatal("failure output should keep only the tail of stdout")
	}
	if len(got) > maxOutputChars+120 {
		t.Fatalf("output exceeds the budget: %d chars", len(got))
	}
}

func TestFormatOutputSmall(t *testing.T) {
	if got := formatOutput("", "", "", 3); got != "Exit code: 3\n(no output)" {
		t.Fatalf("a silent failure must still report its exit code: %q", got)
	}
	if got := formatOutput("python app.py", "", "", 0); !strings.Contains(got, "logic bug") {
		t.Fatalf("silent script = %q", got)
	}
	if got := formatOutput("", "out\n", "warn\n", 0); got != "out\n--- stderr ---\nwarn" {
		t.Fatalf("both streams = %q", got)
	}
	if got := formatOutput("", "", "bad\n", 2); got != "Exit code: 2\n--- stderr ---\nbad" {
		t.Fatalf("stderr only = %q", got)
	}
}

func TestClip(t *testing.T) {
	s := strings.Repeat("line\n", 100)
	if got := clip(s, 0, false); got != "" {
		t.Errorf("zero budget = %q", got)
	}
	if got := clip(s, 1000, false); got != s {
		t.Errorf("short text was cut: %q", got)
	}
	if got := clip(s, 30, false); got != "line\n... (output clipped)" {
		t.Errorf("head clip = %q", got)
	}
	if got := clip(s, 58, true); got != "... (output clipped, showing the end)\nline\nline\nline\n" {
		t.Errorf("tail clip = %q", got)
	}
	// 22 bytes of room hold 11 two-byte characters, never half of one.
	if got := clip(strings.Repeat("ж", 100), 60, true); len([]rune(got)) != len("... (output clipped, showing the end)\n")+11 {
		t.Errorf("tail clip split a character: %q", got)
	}
}

func TestLimitedBuffer(t *testing.T) {
	var b limitedBuffer
	b.max = 7
	text := "абвгд" // 10 bytes, 5 characters
	if n, err := b.Write([]byte(text)); n != len(text) || err != nil {
		t.Fatalf("Write = (%d, %v)", n, err)
	}
	// 7 bytes keep "абв" and half of "г"; the half character is dropped.
	if got, want := b.text(), "абв\n... (3 bytes of output dropped)"; got != want {
		t.Fatalf("buffer text = %q, want %q", got, want)
	}
}

func TestRunBashTimesOut(t *testing.T) {
	sleep := "sleep 60"
	if runtime.GOOS == "windows" {
		sleep = "Start-Sleep -Seconds 60"
	}
	start := time.Now()
	got := RunBash(t.TempDir(), sleep, 1)
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("timed-out command took %v", elapsed)
	}
	if !strings.Contains(got, "timed out after 1 seconds") {
		t.Fatalf("timeout = %q", got)
	}
}

func TestRunBashExitCodeAndOutput(t *testing.T) {
	fail := "exit 3"
	list := "ls"
	if runtime.GOOS == "windows" {
		fail = "cmd /c exit 3"
		list = "Get-ChildItem -Name"
	}
	ws := t.TempDir()
	writeFileAt(t, filepath.Join(ws, "a.txt"), "x")
	if got := RunBash(ws, fail, 10); !strings.Contains(got, "Exit code: 3") {
		t.Fatalf("a failing command must report its exit code: %q", got)
	}
	if got := RunBash(ws, list, 10); !strings.Contains(got, "a.txt") {
		t.Fatalf("listing = %q", got)
	}
	if got := RunBash(ws, "this-command-does-not-exist-tinycode", 10); !strings.HasPrefix(got, "Exit code: ") && !strings.HasPrefix(got, "Error:") {
		t.Fatalf("unknown command = %q", got)
	}
}
