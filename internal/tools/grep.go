package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// maxLineChars is the longest match line shown by search_files.
const maxMatchChars = 200

// maxMatchesPerFile and maxMatches bound what one search returns.
const (
	maxMatchesPerFile = 5
	maxMatches        = 50
)

// maxSearchFileBytes spares the fallback search from crawling huge files
// that ripgrep would have skipped.
const maxSearchFileBytes = 2 << 20

// SearchFiles greps file contents with a regex, using ripgrep when available.
func SearchFiles(workspace, pattern, path, include string) string {
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Sprintf("Error: Invalid regex '%s': %v. Escape regex metacharacters like ( ) [ ] * + ? to search for them literally.",
			pattern, err)
	}

	searchPath := resolveUnder(workspace, path)
	if searchPath == "" {
		return outsideWorkspaceError(path)
	}
	info, err := os.Stat(searchPath)
	if os.IsNotExist(err) {
		return fmt.Sprintf("Error: Path not found: %s", path)
	}
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if info.Mode().IsRegular() {
		return grepSingleFile(pattern, searchPath)
	}
	if !info.IsDir() {
		return fmt.Sprintf("Error: Not a directory: %s", path)
	}

	if out := tryRg(pattern, workspace, searchPath, include); out != "" {
		return out
	}
	return fallbackGrep(pattern, searchPath, include)
}

func grepSingleFile(pattern, filepathAbs string) string {
	regex, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return fmt.Sprintf("Error: Invalid regex '%s': %v", pattern, err)
	}
	f, err := os.Open(filepathAbs)
	if err != nil {
		return fmt.Sprintf("Error reading %s: %v", filepath.Base(filepathAbs), err)
	}
	defer f.Close()

	name := filepath.Base(filepathAbs)
	var found lineCollector
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for ln := 1; scanner.Scan(); ln++ {
		// A line longer than the buffer only ends the scan; the matches
		// found so far are still worth returning.
		if !regex.Match(scanner.Bytes()) {
			continue
		}
		if !found.add(fmt.Sprintf("%s:%d: %s", name, ln, truncateRunes(scanner.Text(), maxMatchChars))) {
			break
		}
	}
	return found.result("--- %d match(es) in "+name+" ---", fmt.Sprintf("No matches for '%s' in %s", pattern, name))
}

// lineCollector gathers matches, keeping only those that fit the result
// budget and counting the rest.
type lineCollector struct {
	b     lineBudget
	total int
}

// add records a line and reports whether more lines are wanted. A line that
// no longer fits the budget still counts as found.
func (c *lineCollector) add(line string) bool {
	c.total++
	c.b.add(line)
	return c.total < maxMatches && !c.b.full
}

func (c *lineCollector) result(headerFmt, noMatch string) string {
	if c.total == 0 {
		return noMatch
	}
	return fmt.Sprintf(headerFmt, c.total) + "\n" + c.b.String() + "\n" + c.b.omitted(c.total-len(c.b.lines), "match(es)")
}

func tryRg(pattern, workspace, searchPath, include string) string {
	rgBin := findRg()
	if rgBin == "" {
		return ""
	}

	// Run from the workspace with a relative path, so matches are reported
	// the same way the fallback reports them.
	shown := searchPath
	if rel, err := filepath.Rel(workspace, searchPath); err == nil {
		shown = rel
	}

	// Flags first and the pattern behind -e, so a pattern that starts with
	// a dash is searched for instead of being parsed as an option.
	args := []string{"-n", "-i", "--no-heading", "-m", fmt.Sprint(maxMatchesPerFile)}
	if g := includeGlob(include); g != "" {
		args = append(args, "-g", g)
	}
	args = append(args, "-e", pattern)
	// Without a path argument ripgrep searches the working directory and
	// prints clean relative paths (no leading .\).
	if shown != "." {
		args = append(args, "--", shown)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, rgBin, args...)
	cmd.Dir = workspace
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return ""
	}
	if err != nil {
		// Exit code 1 means "no match"; anything else is a broken search
		// and falls through to the fallback.
		if ee, ok := err.(*exec.ExitError); !ok || (ee.ExitCode() != 1 && ee.ExitCode() != 0) {
			return ""
		}
	}

	var found lineCollector
	if text := strings.TrimRight(string(out), "\n"); text != "" {
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "Binary file ") {
				continue
			}
			if !found.add(truncateRunes(toOutputText(slashPath(strings.TrimRight(line, "\r"))), maxMatchChars)) {
				break
			}
		}
	}
	return found.result("--- %d match(es) ---", fmt.Sprintf("No matches for '%s' in %s", pattern, searchPath))
}

var reRgLine = regexp.MustCompile(`^([^:\n]+):(\d+):(.*)$`)

// slashPath normalises the separators of a "path:line: text" match line.
func slashPath(line string) string {
	m := reRgLine.FindStringSubmatch(line)
	if m == nil {
		return line
	}
	return filepath.ToSlash(m[1]) + ":" + m[2] + ":" + m[3]
}

func fallbackGrep(pattern, searchPath, include string) string {
	regex, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return fmt.Sprintf("Error: Invalid regex '%s': %v", pattern, err)
	}

	var found lineCollector
	_ = filepath.Walk(searchPath, func(p string, info os.FileInfo, err error) error {
		if err != nil || found.total >= maxMatches || found.b.full {
			return nil
		}
		if info.IsDir() {
			if p != searchPath && EXCLUDE_DIRS[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(searchPath, p)
		relPosix := filepath.ToSlash(rel)
		if hiddenPath(relPosix) || IsBinaryExt(p) || info.Size() > maxSearchFileBytes {
			return nil
		}
		if !includeMatches(relPosix, include) {
			return nil
		}

		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		perFile := 0
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for ln := 1; scanner.Scan() && perFile < maxMatchesPerFile; ln++ {
			if !regex.Match(scanner.Bytes()) {
				continue
			}
			perFile++
			if !found.add(fmt.Sprintf("%s:%d: %s", relPosix, ln, truncateRunes(scanner.Text(), maxMatchChars))) {
				return nil
			}
		}
		return nil
	})

	return found.result("--- %d matches ---", fmt.Sprintf("No matches for '%s' in %s", pattern, searchPath))
}

// hiddenPath reports whether any element of a slash-separated relative path
// is dot-prefixed, the way ripgrep skips hidden files by default.
func hiddenPath(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// includeGlob turns the include argument into a glob: a bare word such as
// "py" names an extension, anything else is already a glob.
func includeGlob(include string) string {
	if include == "" {
		return ""
	}
	g := strings.ReplaceAll(strings.TrimSpace(include), "\\", "/")
	if !strings.ContainsAny(g, "*?[") && !strings.Contains(g, ".") {
		return "*." + g
	}
	return g
}

func includeMatches(relPosix, include string) bool {
	g := includeGlob(include)
	if g == "" {
		return true
	}
	if strings.Contains(g, "/") {
		if ok, _ := globMatch(g, relPosix); ok {
			return true
		}
		ok, _ := globMatch("**/"+g, relPosix)
		return ok
	}
	ok, _ := globMatch(g, path.Base(relPosix))
	return ok
}

func findRg() string {
	if p, err := exec.LookPath("rg"); err == nil {
		return p
	}
	return ""
}
