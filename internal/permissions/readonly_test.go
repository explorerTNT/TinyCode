package permissions

import (
	"runtime"
	"strings"
	"testing"
)

func TestIsCommandReadOnly(t *testing.T) {
	ws := t.TempDir()
	approved := []string{
		"git status", "git status -sb", "git log", "git log --oneline -30",
		"git log --oneline -n 5 abc123..def456", "git log --max-count=5 -- src",
		"git diff", "git diff --staged --stat", "git diff head~1..head",
		"git show 1a2b3c4d", "git show head:main.go", "git branch -a", "git remote -v",
		"git --version", "pip list", "python --version",
		"ls", "ls -la", "get-childitem -recurse", "get-content", "get-content main.go",
		"get-content src/app.py", "get-content -raw", "cat a.txt", "where python", "pwd",
	}
	for _, c := range approved {
		if !isCommandReadOnly(ws, strings.ToLower(c)) {
			t.Errorf("read-only command not approved: %q", c)
		}
	}

	rejected := []string{
		// PowerShell evaluates these before git starts.
		"git log (remove-item -recurse -force .)",
		"git log @(new-item -itemtype file probe.txt)",
		"git log $(rm -r .)",
		"git log { rm -r . }",
		"git log `rm -r .`",
		"git log\nrm -r .",
		// read-only command chained with something else
		"git status; rm -r .", "git status && rm -r .", "git status | remove-item",
		"ls | remove-item", "get-content a.txt > b.txt",
		// git writes and options that spawn or write
		"git branch -d main", "git branch feature/x", "git remote set-url origin https://x",
		"git remote add origin https://x", "git log --output=out.txt", "git log --ext-diff",
		"git diff --no-index nul c:/windows/win.ini", "git push", "git checkout .",
		"git commit -m x", "git log head@{1}",
		// reads outside the workspace
		"get-content c:/users/someone/secret.txt", "get-content ../secret.txt",
		"get-content -path:c:/windows/win.ini", "get-content -path c:/windows/win.ini",
		"cat /etc/passwd", "ls ..",
		// unknown or empty
		"git", "",
		"curl https://example.com", "python script.py", "cmd /c dir",
	}
	if runtime.GOOS == "windows" {
		rejected = append(rejected,
			`get-content \windows\win.ini`, "get-content c:secret.txt", `get-content ..\secret.txt`,
		)
	}
	for _, c := range rejected {
		if isCommandReadOnly(ws, strings.ToLower(c)) {
			t.Errorf("command must not be auto-approved: %q", c)
		}
	}
}
