package tools

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var linuxSubstrings = []string{
	"bash -c", "/bin/bash", "sh -c", "/bin/sh",
	"/usr/", "/etc/", "/home/", "/var/",
}

var linuxCommands = map[string]bool{
	"which": true, "uname": true, "chmod": true, "chown": true,
	"apt": true, "apt-get": true, "yum": true, "dnf": true,
	"head": true, "tail": true, "grep": true, "sed": true,
	"awk": true, "xargs": true, "touch": true, "rm": true,
	"mv": true, "cp": true,
}

const psHelp = "This is Windows. Use PowerShell syntax. " +
	"Common mappings: `ls` = `Get-ChildItem`, `cat` = `Get-Content`, " +
	"`which` = `Get-Command`, `head` = `Select-Object -First`, " +
	"`grep` = `Select-String`, `uname` = `$PSVersionTable`, " +
	"`python3` = `python`, `pip3` = `pip`."

// RunBash runs a shell command and returns its output.
func RunBash(workspace, command string, timeout int) string {
	if timeout < 1 {
		timeout = 30
	}
	if timeout > 120 {
		timeout = 120
	}

	if runtime.GOOS == "windows" {
		command = SanitizeCmd(command)
		if hint := checkLinuxHints(command); hint != "" {
			return fmt.Sprintf("Error: %s\n%s", hint, psHelp)
		}
	}

	var name string
	var args []string
	if runtime.GOOS == "windows" {
		name, args = "powershell", []string{"-NoProfile", "-Command", command}
	} else {
		name, args = "sh", []string{"-c", command}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workspace
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("Error: Command timed out after %d seconds", timeout)
	}

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return fmt.Sprintf("Error: Command not found: %v", err)
		}
	}

	var parts []string
	if s := stdout.String(); s != "" {
		s = strings.TrimRight(s, "\n")
		if len(s) > 10000 {
			s = s[:10000]
		}
		parts = append(parts, s)
	}
	if s := stderr.String(); s != "" {
		s = strings.TrimRight(s, "\n")
		if exitCode != 0 {
			lines := strings.Split(s, "\n")
			if len(lines) > 15 {
				s = "(truncated)\n" + strings.Join(lines[len(lines)-15:], "\n")
			} else if len(s) > 2500 {
				s = strings.TrimLeft(s[len(s)-2500:], " \t\n")
				s = "(truncated)\n" + s
			}
		}
		if len(s) > 5000 {
			s = s[:5000]
		}
		parts = append(parts, "--- stderr ---\n"+s)
	}

	if len(parts) == 0 {
		if pythonScriptRe.MatchString(command) {
			parts = append(parts, "(no output — the script printed nothing. If it was meant to display something, this is a logic bug, not a success.)")
		} else {
			parts = append(parts, "(no output)")
		}
	}

	output := strings.Join(parts, "\n")
	if exitCode != 0 {
		output = fmt.Sprintf("Exit code: %d\n%s", exitCode, output)
		if strings.Contains(stderr.String(), "Traceback") {
			output += "\n\nHINT: The script crashed. Read the error, fix the bug in the .py file, then re-run."
		}
	}
	return output
}

var pythonScriptRe = regexp.MustCompile(`\bpython\b.*\.py\b`)

// CheckLinuxHints reports a rejected Linux command for the model to learn from.
func CheckLinuxHints(command string) string { return checkLinuxHints(command) }

func checkLinuxHints(command string) string {
	lowered := strings.ToLower(strings.TrimSpace(command))
	for _, frag := range linuxSubstrings {
		if strings.Contains(lowered, frag) {
			return fmt.Sprintf("Linux command detected: '%s'", strings.TrimSpace(frag))
		}
	}
	for _, segment := range linuxSegmentRe.Split(lowered, -1) {
		tokens := strings.Fields(segment)
		if len(tokens) == 0 {
			continue
		}
		head := strings.Trim(tokens[0], "\"'")
		if linuxCommands[head] {
			return fmt.Sprintf("Linux command detected: '%s'", head)
		}
	}
	return ""
}

var linuxSegmentRe = regexp.MustCompile(`\|\||&&|[|;&]`)

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
		if wasQuoted {
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
