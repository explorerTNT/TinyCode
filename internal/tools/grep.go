package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// SearchFiles greps file contents with a regex, using ripgrep when available.
func SearchFiles(workspace, pattern, path, include string) string {
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Sprintf("Error: Invalid regex '%s': %v. Escape regex metacharacters like ( ) [ ] * + ? to search for them literally.",
			pattern, err)
	}

	searchPath := resolveUnder(workspace, path)
	info, err := os.Stat(searchPath)
	if os.IsNotExist(err) {
		return fmt.Sprintf("Error: Path not found: %s", path)
	}
	if err == nil && info.Mode().IsRegular() {
		return grepSingleFile(pattern, searchPath)
	}
	if err == nil && !info.IsDir() {
		return fmt.Sprintf("Error: Not a directory: %s", path)
	}

	if out := tryRg(pattern, searchPath, include); out != "" {
		return out
	}
	return fallbackGrep(pattern, searchPath, include)
}

func grepSingleFile(pattern string, filepathAbs string) string {
	regex, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return fmt.Sprintf("Error: Invalid regex '%s': %v", pattern, err)
	}
	f, err := os.Open(filepathAbs)
	if err != nil {
		return fmt.Sprintf("Error reading %s: %v", filepath.Base(filepathAbs), err)
	}
	defer f.Close()

	var results []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	ln := 0
	for scanner.Scan() {
		ln++
		line := scanner.Text()
		if regex.MatchString(line) {
			line = truncate(line, 200)
			results = append(results, fmt.Sprintf("%s:%d: %s", filepath.Base(filepathAbs), ln, line))
			if len(results) >= 50 {
				break
			}
		}
	}
	if len(results) == 0 {
		return fmt.Sprintf("No matches for '%s' in %s", pattern, filepath.Base(filepathAbs))
	}
	return fmt.Sprintf("--- %d match(es) in %s ---\n%s", len(results), filepath.Base(filepathAbs), strings.Join(results, "\n"))
}

func tryRg(pattern, searchPath, include string) string {
	rgBin := findRg()
	if rgBin == "" {
		return ""
	}

	args := []string{"-n", "-i", pattern, searchPath}
	if include != "" {
		g := include
		if !strings.Contains(g, ".") {
			g = "*." + strings.TrimPrefix(g, "*.")
		}
		args = append(args, "-g", g)
	}
	args = append(args, "--no-heading", "-m", "5")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, rgBin, args...)
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return ""
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && (ee.ExitCode() == 1 || ee.ExitCode() == 0) {
			// fall through to read output
		} else {
			return ""
		}
	}

	output := strings.TrimRight(string(out), "\n")
	if output == "" {
		return fmt.Sprintf("No matches for '%s' in %s", pattern, searchPath)
	}
	lines := strings.Split(output, "\n")
	if len(lines) > 50 {
		lines = lines[:50]
	}
	return fmt.Sprintf("--- %d match(es) ---\n%s", len(strings.Split(output, "\n")), strings.Join(lines, "\n"))
}

func fallbackGrep(pattern, searchPath, include string) string {
	regex, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return fmt.Sprintf("Error: Invalid regex '%s': %v", pattern, err)
	}

	var results []string
	_ = filepath.Walk(searchPath, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if p != searchPath && EXCLUDE_DIRS[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if include != "" && filepath.Ext(p) != "."+strings.TrimPrefix(include, "*.") {
			return nil
		}
		if NOISE_EXTS[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		rel, _ := filepath.Rel(searchPath, p)
		if strings.HasPrefix(rel, ".") {
			return nil
		}

		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		ln := 0
		for scanner.Scan() {
			ln++
			line := scanner.Text()
			if regex.MatchString(line) {
				results = append(results, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(rel), ln, truncate(line, 200)))
				if len(results) >= 50 {
					return nil
				}
			}
		}
		return nil
	})

	if len(results) == 0 {
		return fmt.Sprintf("No matches for '%s' in %s", pattern, searchPath)
	}
	suffix := ""
	if len(results) > 50 {
		suffix = "\n..."
	}
	return fmt.Sprintf("--- %d matches ---\n%s%s", len(results), strings.Join(results[:min(50, len(results))], "\n"), suffix)
}

func findRg() string {
	if p, err := exec.LookPath("rg"); err == nil {
		return p
	}
	return ""
}
