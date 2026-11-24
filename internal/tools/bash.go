package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

// consoleUTF8 makes PowerShell emit its own messages (errors, warnings,
// formatted objects) as UTF-8 instead of the OEM code page.
const consoleUTF8 = "[Console]::OutputEncoding=[Text.Encoding]::UTF8"

// psExitTrailer makes powershell.exe report the real exit code of a failed
// command instead of a flat 1. Without it a command that fails silently is
// indistinguishable from success.
const psExitTrailer = "\nif (-not $?) { if ($null -ne $LASTEXITCODE -and $LASTEXITCODE -ne 0) { exit $LASTEXITCODE } else { exit 1 } }"

// maxCapturedBytes is how much of each output stream is kept in memory.
const maxCapturedBytes = 256 << 10

var (
	segmentRe = regexp.MustCompile(`\|\||&&|[|;&]`)

	// linuxShells and linuxCommands are commands that do not exist in
	// PowerShell; the model gets a hint instead of a cryptic error.
	linuxShells   = map[string]bool{"bash": true, "sh": true, "/bin/bash": true, "/bin/sh": true, "/usr/bin/env": true}
	linuxCommands = map[string]bool{
		"which": true, "uname": true, "chmod": true, "chown": true,
		"apt": true, "apt-get": true, "yum": true, "dnf": true,
		"head": true, "tail": true, "grep": true, "sed": true,
		"awk": true, "xargs": true, "touch": true, "rm": true,
		"mv": true, "cp": true,
	}

	// linuxPathPrefixes mark a POSIX absolute path. PowerShell would resolve
	// such a path against the root of the current drive.
	linuxPathPrefixes = []string{"/bin/", "/etc/", "/usr/", "/home/", "/var/", "/opt/", "/tmp/", "/dev/"}
)

const psHelp = "This is Windows. Use PowerShell syntax. " +
	"Common mappings: `ls` = `Get-ChildItem`, `cat` = `Get-Content`, " +
	"`which` = `Get-Command`, `head` = `Select-Object -First`, " +
	"`grep` = `Select-String`, `uname` = `$PSVersionTable`, " +
	"`python3` = `python`, `pip3` = `pip`. " +
	"Paths like /home/user/... do not exist here, use paths inside the project."

var pythonScriptRe = regexp.MustCompile(`\bpython\b.*\.py\b`)

// RunBash runs a shell command and returns its output.
func RunBash(workspace, command string, timeout int) string {
	if timeout < 1 {
		timeout = 30
	}
	if timeout > 120 {
		timeout = 120
	}

	var name string
	var args []string
	env := os.Environ()
	if runtime.GOOS == "windows" {
		command = SanitizeCmd(command)
		if hint := checkLinuxHints(command); hint != "" {
			return fmt.Sprintf("Error: %s\n%s", hint, psHelp)
		}
		// Python writes redirected output in the ANSI code page whatever the
		// console says, so it gets its own hint.
		name, args = "powershell", []string{"-NoProfile", "-Command", consoleUTF8 + "; " + command + psExitTrailer}
		env = append(env, "PYTHONIOENCODING=utf-8")
	} else {
		name, args = "sh", []string{"-c", command}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workspace
	cmd.Env = env
	setKillTree(cmd)
	stdout := &limitedBuffer{max: maxCapturedBytes}
	stderr := &limitedBuffer{max: maxCapturedBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("Error: Command timed out after %d seconds and was stopped", timeout)
	}

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return fmt.Sprintf("Error: Command not found: %v", err)
		}
	}

	return formatOutput(command, toOutputText(stdout.text()), toOutputText(stderr.text()), exitCode)
}

// formatOutput fits both streams into the result budget. On success the
// start of the output matters; on failure the end, where compilers and test
// runners report what went wrong.
func formatOutput(command, stdout, stderr string, exitCode int) string {
	stdout, stderr = strings.TrimRight(stdout, "\n"), strings.TrimRight(stderr, "\n")
	if stdout == "" && stderr == "" {
		stdout = "(no output)"
		if pythonScriptRe.MatchString(command) {
			stdout = "(no output. If the script was meant to display something, this is a logic bug, not a success.)"
		}
	}

	const stderrHeader = "--- stderr ---\n"
	if exitCode == 0 {
		stdout = clip(stdout, maxOutputChars, false)
		stderr = clip(stderr, maxOutputChars-len(stdout)-len(stderrHeader), false)
	} else {
		stderr = clip(stderr, maxOutputChars*3/5, true)
		stdout = clip(stdout, maxOutputChars-len(stderr)-len(stderrHeader), true)
	}

	var b strings.Builder
	if stdout != "" {
		b.WriteString(stdout)
	}
	if stderr != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(stderrHeader)
		b.WriteString(stderr)
	}

	if exitCode == 0 {
		return b.String()
	}
	output := fmt.Sprintf("Exit code: %d\n%s", exitCode, b.String())
	if strings.Contains(stderr, "Traceback") {
		output += "\n\nHINT: The script crashed. Read the error, fix the bug in the .py file, then re-run."
	}
	return output
}

// toOutputText makes shell output valid UTF-8. Output that is not UTF-8
// comes from a native tool writing in the system ANSI code page.
func toOutputText(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	if enc := systemEncoding(); enc != nil {
		if dec, err := enc.NewDecoder().Bytes([]byte(s)); err == nil {
			return string(dec)
		}
	}
	return strings.ToValidUTF8(s, "\uFFFD")
}

// limitedBuffer keeps the first max bytes of a stream and counts the rest,
// so a chatty or runaway command cannot exhaust memory.
type limitedBuffer struct {
	buf     strings.Builder
	max     int
	dropped int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	keep := b.max - b.buf.Len()
	if keep > len(p) {
		keep = len(p)
	}
	if keep > 0 {
		b.buf.Write(p[:keep])
	}
	b.dropped += len(p) - keep
	return len(p), nil
}

func (b *limitedBuffer) text() string {
	s := b.buf.String()
	// Never leave half a character at the cut: it would make the whole
	// output look like a different encoding.
	for i := 0; i < utf8.UTFMax && !utf8.ValidString(s); i++ {
		s = s[:len(s)-1]
	}
	if b.dropped > 0 {
		s += fmt.Sprintf("\n... (%d bytes of output dropped)", b.dropped)
	}
	return s
}

// checkLinuxHints reports a rejected Linux command or path, so the model can
// learn from the error instead of repeating it.
func checkLinuxHints(command string) string {
	lowered := strings.ToLower(strings.TrimSpace(command))
	for _, segment := range segmentRe.Split(lowered, -1) {
		tokens := strings.Fields(segment)
		if len(tokens) == 0 {
			continue
		}
		if head := strings.Trim(tokens[0], "\"'"); linuxShells[head] || linuxCommands[head] {
			return fmt.Sprintf("Linux command detected: '%s'", head)
		}
		// Only a token that starts with the path counts: /home/ inside a URL
		// is not a path.
		for _, t := range tokens {
			t = strings.Trim(t, "\"'")
			for _, prefix := range linuxPathPrefixes {
				if strings.HasPrefix(t, prefix) {
					return fmt.Sprintf("Linux path detected: '%s'", t)
				}
			}
		}
	}
	return ""
}

// SanitizeCmd fixes harmless Windows/Linux mechanics small models get wrong.
func SanitizeCmd(command string) string {
	cmd := strings.TrimSpace(command)
	cmd = reLeadingCd.ReplaceAllString(cmd, "")
	cmd = rePython3.ReplaceAllString(cmd, "python")
	cmd = rePip3.ReplaceAllString(cmd, "pip")
	cmd = reDevNull2.ReplaceAllString(cmd, "")
	cmd = reDevNull.ReplaceAllString(cmd, "")
	cmd = reCatPipe.ReplaceAllString(cmd, "")
	cmd = squeezeSpacesOutsideQuotes(cmd)
	return strings.Trim(strings.TrimSpace(cmd), ";")
}

var (
	reLeadingCd = regexp.MustCompile(`^\s*cd\s+(?:\.|["']\.["'])\s*(?:&&|;)\s*`)
	rePython3   = regexp.MustCompile(`\bpython3(?:\.\d+)?\b`)
	rePip3      = regexp.MustCompile(`\bpip3\b`)
	reDevNull2  = regexp.MustCompile(`\s*2>\s*/dev/null`)
	reDevNull   = regexp.MustCompile(`\s*1?>\s*/dev/null`)
	reCatPipe   = regexp.MustCompile(`\s+2>&1\s*\|\s*cat\b`)
)

func squeezeSpacesOutsideQuotes(cmd string) string {
	var out strings.Builder
	var quote rune
	prevSpace := false
	for _, ch := range cmd {
		if quote != 0 {
			out.WriteRune(ch)
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			quote = ch
			out.WriteRune(ch)
			prevSpace = false
			continue
		}
		if ch == ' ' || ch == '\t' {
			if !prevSpace {
				out.WriteRune(' ')
			}
			prevSpace = true
			continue
		}
		out.WriteRune(ch)
		prevSpace = false
	}
	return out.String()
}

type cmdToken struct {
	raw    string
	quoted bool
}

// TokenizeCmd splits a command into tokens, keeping quoted spans whole.
func TokenizeCmd(cmd string) []cmdToken {
	var tokens []cmdToken
	var cur strings.Builder
	curQuoted := false
	var inQ rune
	for _, ch := range cmd {
		if inQ != 0 {
			cur.WriteRune(ch)
			if ch == inQ {
				inQ = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			if cur.Len() == 0 {
				curQuoted = true
			}
			inQ = ch
			cur.WriteRune(ch)
			continue
		}
		if ch == ' ' || ch == '\t' {
			if cur.Len() != 0 {
				tokens = append(tokens, cmdToken{cur.String(), curQuoted})
				cur.Reset()
				curQuoted = false
			}
			continue
		}
		cur.WriteRune(ch)
	}
	if cur.Len() != 0 {
		tokens = append(tokens, cmdToken{cur.String(), curQuoted})
	}
	return tokens
}

// QuoteExistingPaths wraps unquoted runs of tokens that name a real workspace
// file in double quotes, so spaced paths survive the shell.
func QuoteExistingPaths(workspace, command string) string {
	tokens := TokenizeCmd(command)
	var out []string
	i, n := 0, len(tokens)
	for i < n {
		raw, wasQuoted := tokens[i].raw, tokens[i].quoted
		// The command name itself is never a path: quoting it would turn
		// the call into a bare string.
		if wasQuoted || i == 0 {
			out = append(out, raw)
			i++
			continue
		}
		best := -1
		for j := i + 1; j <= n; j++ {
			window := tokens[i:j]
			anyQuoted := false
			for _, t := range window {
				if t.quoted {
					anyQuoted = true
					break
				}
			}
			if anyQuoted {
				break
			}
			joined := strings.TrimSpace(strings.Join(raws(window), " "))
			cand := joined
			if !isAbs(cand) {
				cand = joinPath(workspace, joined)
			}
			if pathExists(cand) {
				best = j
			}
		}
		if best != -1 {
			joined := strings.TrimSpace(strings.Join(raws(tokens[i:best]), " "))
			out = append(out, `"`+joined+`"`)
			i = best
		} else {
			out = append(out, raw)
			i++
		}
	}
	return strings.Join(out, " ")
}

func raws(tokens []cmdToken) []string {
	s := make([]string, len(tokens))
	for i, t := range tokens {
		s[i] = t.raw
	}
	return s
}
