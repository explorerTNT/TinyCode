package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// SyntaxCheckEnabled switches the check after edits; the agent sets it from
// the config.
var SyntaxCheckEnabled = true

// checkTimeout bounds one checker run; a slow checker must not stall the
// agent loop.
const checkTimeout = 15 * time.Second

// pythonAST parses a file without writing bytecode or running it.
const pythonAST = "import ast,sys\nast.parse(open(sys.argv[1],encoding='utf-8-sig').read(),sys.argv[1])"

type checkerStatus int

const (
	checkerMissing checkerStatus = iota
	checkerOK
	checkerFailed
)

// SyntaxCheck runs the cheapest available parser over a source file and
// returns a report for the model: "" when nothing applies, a "CHECK:" line
// otherwise. A small model then sees a syntax error together with the edit
// that caused it, instead of three tool calls later.
func SyntaxCheck(path string) string {
	if !SyntaxCheckEnabled {
		return ""
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		stdout, stderr, status := runChecker("gofmt", "", "-l", path)
		switch status {
		case checkerMissing:
			return ""
		case checkerOK:
			if strings.TrimSpace(stdout) != "" {
				return "\nCHECK (gofmt): syntax OK, but the file is not gofmt-formatted. Run: gofmt -w " + filepath.ToSlash(path)
			}
			return "\nCHECK (gofmt): syntax OK"
		}
		return "\nCHECK (gofmt): SYNTAX ERROR, fix it now:\n" + lines(stderr, 12)
	case ".json":
		return checkJSON(path)
	case ".py":
		return checkerReport("python", "python3", "python", "-c", pythonAST, path)
	case ".js", ".mjs", ".cjs":
		return checkerReport("node", "", "node", "--check", path)
	case ".ps1":
		return checkerReport("powershell", "pwsh", "powershell", "-NoProfile", "-Command", psParse(path))
	case ".sh", ".bash":
		if runtime.GOOS == "windows" {
			return ""
		}
		return checkerReport("sh", "", "sh", "-n", path)
	}
	return ""
}

// checkerReport runs a checker whose non-zero exit means a syntax error.
func checkerReport(binary, fallback, label string, args ...string) string {
	_, stderr, status := runChecker(binary, fallback, args...)
	switch status {
	case checkerMissing:
		return ""
	case checkerOK:
		return "\nCHECK (" + label + "): syntax OK"
	}
	// The reason is at the end of a Python traceback and at the start of
	// most other checkers; the last lines cover both.
	return "\nCHECK (" + label + "): SYNTAX ERROR, fix it now:\n" + tailLines(stderr, 10)
}

// runChecker runs a syntax checker. A missing checker is not an error: the
// check is skipped. So are a timeout or a failure to start.
func runChecker(binary, fallback string, args ...string) (stdout, stderr string, status checkerStatus) {
	name, err := exec.LookPath(binary)
	if err != nil && fallback != "" {
		name, err = exec.LookPath(fallback)
	}
	if err != nil {
		return "", "", checkerMissing
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	hideConsole(cmd)
	var out, errOut limitedBuffer
	out.max, errOut.max = 16<<10, 16<<10
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return "", "", checkerMissing
		}
		return out.buf.String(), errOut.text(), checkerFailed
	}
	return out.text(), errOut.text(), checkerOK
}

func checkJSON(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var v any
	if err := dec.Decode(&v); err != nil {
		return "\nCHECK (json): SYNTAX ERROR, fix it now:\n" + err.Error()
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return "\nCHECK (json): SYNTAX ERROR, fix it now:\nextra data after the top-level value"
	}
	return "\nCHECK (json): syntax OK"
}

// psParse is a PowerShell script that parses a file and prints its syntax
// errors as "line: message".
func psParse(path string) string {
	quoted := strings.ReplaceAll(path, "'", "''")
	return "$t=$null;$e=$null;[void][System.Management.Automation.Language.Parser]::ParseFile('" + quoted +
		"',[ref]$t,[ref]$e); if($e.Count){ $e | ForEach-Object { '{0}: {1}' -f $_.Extent.StartLineNumber, $_.Message }; exit 1 }"
}

const maxCheckChars = 900

// lines returns the first n lines of a checker report, within the budget.
func lines(s string, n int) string {
	all := strings.Split(strings.TrimSpace(s), "\n")
	if len(all) > n {
		all = append(all[:n], "...")
	}
	return truncateRunes(strings.Join(all, "\n"), maxCheckChars)
}

// tailLines returns the last n lines, within the budget.
func tailLines(s string, n int) string {
	all := strings.Split(strings.TrimSpace(s), "\n")
	if len(all) > n {
		all = append([]string{"..."}, all[len(all)-n:]...)
	}
	return truncateRunes(strings.Join(all, "\n"), maxCheckChars)
}
