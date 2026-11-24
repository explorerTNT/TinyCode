package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// EXCLUDE_DIRS are directory names skipped during file walks (build/binary noise).
var EXCLUDE_DIRS = map[string]bool{
	".venv": true, "venv": true, ".git": true, "__pycache__": true,
	"node_modules": true, ".hg": true, ".svn": true, ".idea": true,
	".vscode": true, ".tox": true, "build": true, "dist": true,
	".next": true, ".turbo": true, "bin": true, "obj": true,
	"target": true, ".gradle": true, ".pytest_cache": true,
	".mypy_cache": true, ".vs": true, ".vscode-test": true,
	".cache": true, ".parcel-cache": true, "coverage": true, ".nuget": true,
}

const (
	maxFiles     = 20000
	scanTimeout  = 10 * time.Second
	maxListLimit = 1000
)

// ListFiles lists files matching a glob pattern, newest first.
func ListFiles(workspace, pattern, path string) string {
	base := resolveUnder(workspace, path)
	if base == "" {
		return outsideWorkspaceError(path)
	}
	info, err := os.Stat(base)
	if err != nil || !info.IsDir() {
		return fmt.Sprintf("Error: Not a directory: %s", path)
	}

	results, err := walkGlob(base, pattern)
	if err != nil {
		return err.Error()
	}
	if len(results) == 0 {
		return fmt.Sprintf("No files matching '%s' in %s", pattern, base)
	}

	limit := maxListLimit
	if strings.ContainsAny(pattern, "*?") {
		limit = 200
	}
	var b lineBudget
	b.add(fmt.Sprintf("--- %d files matching '%s' (newest first) ---", len(results), pattern))
	shown := 0
	for _, r := range results {
		if shown >= limit {
			break
		}
		before := len(b.lines)
		b.add(fmt.Sprintf("%s (%s, %s)", r.rel, fmtSize(r.size), r.mtime.Format("2006-01-02 15:04")))
		if len(b.lines) == before {
			break
		}
		shown++
	}
	if note := b.omitted(len(results)-shown, "files"); note != "" {
		return b.String() + "\n" + note
	}
	return b.String()
}

type globResult struct {
	rel   string
	size  int64
	mtime time.Time
}

func walkGlob(base, pattern string) ([]globResult, error) {
	normPattern := strings.ReplaceAll(pattern, "\\", "/")
	// A pattern without a slash may also match by file name alone.
	byName := !strings.Contains(normPattern, "/")
	match, err := globRegexp(normPattern)
	if err != nil {
		return nil, err
	}
	explicit := pattern != "*" && pattern != "*.*" && pattern != ""

	deadline := time.Now().Add(scanTimeout)
	scanned := 0
	var results []globResult

	err = filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("Error: Scan timed out (> %ds). Narrow your search.", int(scanTimeout.Seconds()))
		}
		if info.IsDir() {
			if p != base && EXCLUDE_DIRS[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		scanned++
		if scanned > maxFiles {
			return fmt.Errorf("Error: Too many files (%d+). Narrow your search.", maxFiles)
		}

		rel, _ := filepath.Rel(base, p)
		relPosix := filepath.ToSlash(rel)
		matched := match.MatchString(relPosix)
		if !matched && byName {
			matched = match.MatchString(info.Name())
		}
		if !matched {
			return nil
		}
		if !explicit && IsBinaryExt(info.Name()) {
			return nil
		}

		results = append(results, globResult{rel: relPosix, size: info.Size(), mtime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(results, func(i, j int) bool { return results[i].mtime.After(results[j].mtime) })
	return results, nil
}

// globMatch reports whether rel, a slash-separated path, matches pattern.
func globMatch(pattern, rel string) (bool, error) {
	match, err := globRegexp(strings.ReplaceAll(pattern, "\\", "/"))
	if err != nil {
		return false, err
	}
	return match.MatchString(rel), nil
}

// globRegexp compiles a glob to an anchored regexp. It behaves the same on
// every OS (unlike filepath.Match, whose separator rules differ), and both
// `*` and `**` may cross directories, which is what small models expect
// from them: `*.go` finds Go files at any depth, `src/**/*.go` also finds
// the ones directly under src.
func globRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	if runtime.GOOS == "windows" {
		b.WriteString("(?i)")
	}
	braceDepth := 0
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			stars := 1
			for i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				stars++
			}
			if stars > 1 && i+1 < len(pattern) && pattern[i+1] == '/' {
				b.WriteString("(?:.*/)?")
				i++
			} else {
				b.WriteString(".*")
			}
		case '?':
			b.WriteString(".")
		case '[':
			j := i + 1
			if j < len(pattern) && (pattern[j] == '!' || pattern[j] == '^') {
				j++
			}
			if j < len(pattern) && pattern[j] == ']' {
				j++
			}
			for j < len(pattern) && pattern[j] != ']' {
				j++
			}
			if j >= len(pattern) {
				return nil, fmt.Errorf("Error: Invalid glob '%s': '[' is never closed.", pattern)
			}
			class := pattern[i : j+1]
			if class[1] == '!' {
				class = "[^" + class[2:]
			}
			b.WriteString(class)
			i = j
		case '{':
			braceDepth++
			b.WriteString("(?:")
		case '}':
			if braceDepth == 0 {
				b.WriteString("}")
				continue
			}
			braceDepth--
			b.WriteString(")")
		case ',':
			if braceDepth > 0 {
				b.WriteString("|")
			} else {
				b.WriteString(",")
			}
		default:
			if strings.IndexByte("\\.+()|^$", c) >= 0 {
				b.WriteByte('\\')
			}
			b.WriteByte(c)
		}
	}
	if braceDepth > 0 {
		b.WriteString(")")
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// SuggestFiles finds workspace files whose name matches a target basename,
// for "did you mean" hints. Returns relative paths, best matches first.
func SuggestFiles(workspace, target string, maxResults int) []string {
	if maxResults <= 0 {
		maxResults = 5
	}
	target = strings.ToLower(filepath.Base(strings.ReplaceAll(target, "\\", "/")))
	if target == "" {
		return nil
	}

	base := resolveUnder(workspace, ".")
	if base == "" {
		return nil
	}

	var exact, contains []string
	deadline := time.Now().Add(scanTimeout)
	_ = filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err != nil || time.Now().After(deadline) {
			return nil
		}
		if info.IsDir() {
			if p != base && EXCLUDE_DIRS[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		low := strings.ToLower(info.Name())
		rel, _ := filepath.Rel(base, p)
		rel = filepath.ToSlash(rel)
		if low == target {
			exact = append(exact, rel)
		} else if strings.Contains(low, target) && !IsBinaryExt(info.Name()) {
			contains = append(contains, rel)
		}
		return nil
	})

	sort.Strings(exact)
	sort.Strings(contains)
	merged := append(exact, contains...)
	if len(merged) > maxResults {
		merged = merged[:maxResults]
	}
	return merged
}

// resolveUnder resolves an absolute or workspace-relative path, returning ""
// when it is unusable or points outside the workspace.
func resolveUnder(workspace, p string) string {
	if p == "" {
		p = "."
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(workspace, p)
	}
	p = filepath.Clean(p)
	if workspace == "" {
		return p
	}
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	if !insideWorkspace(workspace, p) {
		return ""
	}
	return p
}

func insideWorkspace(ws, p string) bool {
	rel, err := filepath.Rel(ws, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func outsideWorkspaceError(path string) string {
	return fmt.Sprintf("Error: '%s' is outside the project directory. Use a bare relative name like calc.py instead.", path)
}

func fmtSize(size int64) string {
	switch {
	case size < 1024:
		return fmt.Sprintf("%dB", size)
	case size < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(size)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(size)/1024/1024)
	}
}
