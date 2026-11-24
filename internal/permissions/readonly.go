package permissions

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Allowlist of shell commands that run without a prompt in ask mode. A
// command counts as read-only only when every token of it is known to be
// harmless; anything else goes to the user.

// Characters that let a shell run something other than the literal command:
// pipes, redirects, separators, variables, and the PowerShell groupings
// ( ... ), $( ... ), @( ... ) and { ... } that are evaluated before git even
// starts.
const dangerousChars = "><|;&`$\n\r(){}"

// exactCommands are approved as a whole, with no extra arguments.
var exactCommands = map[string]bool{
	"git --version": true, "python --version": true, "node --version": true,
	"npm --version": true, "pip --version": true, "pip list": true,
}

// flagOnlyCommands accept at most one argument: a listed switch, or a path
// inside the workspace.
var flagOnlyCommands = map[string][]string{
	"ls":            {"-a", "-l", "-la", "-lah", "-r", "-h", "-force", "-recurse", "-name", "-directory", "-file", "-hidden"},
	"dir":           {"-a", "-l", "-la", "-lah", "-r", "-h", "-force", "-recurse", "-name", "-directory", "-file", "-hidden"},
	"get-childitem": {"-force", "-recurse", "-name", "-directory", "-file", "-hidden"},
	"cat":           {"-n"},
	"type":          {},
	"get-content":   {"-raw"},
	"cd":            {},
	"pwd":           {},
	"get-location":  {},
	"whoami":        {},
	"where":         {},
}

type gitPolicy struct {
	switches []string // accepted as --name or --name=value
	bare     bool     // revisions and pathspecs accepted
}

// gitPolicies covers the git subcommands that only read. Writing subcommands
// (push, reset, checkout, ...) are absent on purpose, and so are the options
// that turn a read into a write or spawn a program: --output, --ext-diff,
// --textconv, --no-index.
var gitPolicies = map[string]gitPolicy{
	"status": {switches: fields(`-s -b -sb --short --branch --porcelain --no-renames --ignored
		-uno -unormal -uall --untracked-files`)},
	"log": {switches: fields(`-n --oneline --graph --decorate --no-merges --all --reverse --name-only
		--name-status --stat --shortstat --numstat --follow --abbrev-commit --full-history
		--first-parent --no-patch --no-color --topo-order --date-order --ancestry-path --branches
		--tags --remotes --max-count --skip --pretty --format --date --grep --author --committer
		--since --until --after --before`), bare: true},
	"diff": {switches: fields(`-w -b --stat --staged --cached --name-only --name-status --shortstat
		--numstat --check --no-color --ignore-all-space --ignore-blank-lines --ignore-space-change
		--minimal --patience --summary --raw --no-ext-diff --no-textconv --exit-code --quiet
		--unified --word-diff`), bare: true},
	"show": {switches: fields(`-n -s --oneline --stat --name-only --name-status --no-patch
		--shortstat --numstat --no-color --abbrev-commit --pretty --format`), bare: true},
	"branch": {switches: fields(`-a -r -v -vv --all --remotes --list --verbose --show-current --no-color`)},
	"remote": {switches: fields(`-v --verbose`)},
}

var (
	numericFlag = regexp.MustCompile(`^-[nU]?\d+$`)
	revision    = regexp.MustCompile(`^[0-9A-Za-z_][0-9A-Za-z_./@^~:\-]*$`)
)

func fields(s string) []string { return strings.Fields(s) }

// isCommandReadOnly reports whether command cannot change anything and may
// skip the prompt. command must be trimmed and lower-cased.
func isCommandReadOnly(workspace, command string) bool {
	if strings.ContainsAny(command, dangerousChars) {
		return false
	}
	elems := strings.Fields(command)
	if len(elems) == 0 {
		return false
	}
	if exactCommands[command] {
		return true
	}
	if elems[0] == "git" {
		if len(elems) < 2 {
			return false
		}
		policy, ok := gitPolicies[elems[1]]
		if !ok {
			return false
		}
		return gitArgsReadOnly(policy, elems[2:])
	}
	return singleArgReadOnly(workspace, elems)
}

func gitArgsReadOnly(policy gitPolicy, args []string) bool {
	pathspecs := false
	for _, a := range args {
		switch {
		case pathspecs:
		case a == "--":
			pathspecs = true
		case strings.HasPrefix(a, "--"):
			name, _, _ := strings.Cut(strings.TrimPrefix(a, "--"), "=")
			if !slices.Contains(policy.switches, "--"+name) {
				return false
			}
		case strings.HasPrefix(a, "-"):
			if !numericFlag.MatchString(a) && !slices.Contains(policy.switches, a) {
				return false
			}
		default:
			if !policy.bare || !revision.MatchString(a) {
				return false
			}
		}
	}
	return true
}

func singleArgReadOnly(workspace string, elems []string) bool {
	switches, ok := flagOnlyCommands[elems[0]]
	if !ok || len(elems) > 2 {
		return false
	}
	if len(elems) == 1 {
		return true
	}
	arg := elems[1]
	if strings.HasPrefix(arg, "-") {
		return slices.Contains(switches, arg)
	}
	return isWorkspacePath(workspace, arg)
}

// isWorkspacePath reports whether arg is a plain relative path (no drive,
// provider or root prefix) that resolves inside the workspace.
func isWorkspacePath(workspace, arg string) bool {
	if strings.Contains(arg, ":") || strings.HasPrefix(arg, "/") || strings.HasPrefix(arg, `\`) {
		return false
	}
	return insideWorkspace(workspace, filepath.Join(workspace, arg))
}

func insideWorkspace(ws, p string) bool {
	rel, err := filepath.Rel(ws, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
