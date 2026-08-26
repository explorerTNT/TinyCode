package tools

import (
	"fmt"
	"os"
	"path/filepath"
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

// NOISE_EXTS are file extensions treated as build/binary noise.
var NOISE_EXTS = map[string]bool{
	".exe": true, ".dll": true, ".pdb": true, ".obj": true, ".lib": true,
	".so": true, ".dylib": true, ".class": true, ".pyc": true, ".pyd": true,
	".rar": true, ".zip": true, ".7z": true, ".gz": true, ".tar": true,
	".nupkg": true, ".ico": true, ".png": true, ".jpg": true, ".jpeg": true,
	".gif": true, ".bmp": true, ".webp": true, ".mp3": true, ".mp4": true,
	".avi": true, ".mkv": true, ".wav": true, ".ttf": true, ".otf": true,
	".woff": true, ".woff2": true,
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
		return fmt.Sprintf("Error: Path not found: %s", path)
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
	var b strings.Builder
	fmt.Fprintf(&b, "--- %d files matching '%s' (newest first) ---\n", len(results), pattern)
	for i, r := range results {
		if i >= limit {
			fmt.Fprintf(&b, "... and %d more files\n", len(results)-limit)
			break
		}
		fmt.Fprintf(&b, "%s (%s, %s)\n", r.rel, fmtSize(r.size),
			r.mtime.Format("2006-01-02 15:04"))
	}
	return strings.TrimRight(b.String(), "\n")
}

type globResult struct {
	rel   string
	size  int64
	mtime time.Time
}

func walkGlob(base, pattern string) ([]globResult, error) {
	normPattern := strings.ReplaceAll(pattern, "\\", "/")
	pathScoped := strings.Contains(normPattern, "/")
	explicit := pattern != "*" && pattern != "*.*" && pattern != ""

	deadline := time.Now().Add(scanTimeout)
	scanned := 0
	var results []globResult

	err := filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
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

		name := info.Name()
		var matched bool
		if pathScoped {
			rel, _ := filepath.Rel(base, p)
			relPosix := filepath.ToSlash(rel)
			matched, _ = filepath.Match(normPattern, relPosix)
			if !matched && !strings.Contains(normPattern, "**") {
				alt := strings.Replace(normPattern, "/", "/*", 1)
				matched, _ = filepath.Match(alt, relPosix)
			}
		} else {
			matched, _ = filepath.Match(pattern, name)
		}
		if !matched {
			return nil
		}

		if !explicit && NOISE_EXTS[strings.ToLower(filepath.Ext(name))] {
			return nil
		}

		rel, _ := filepath.Rel(base, p)
		results = append(results, globResult{rel: filepath.ToSlash(rel), size: info.Size(), mtime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(results, func(i, j int) bool { return results[i].mtime.After(results[j].mtime) })
	return results, nil
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
		} else if strings.Contains(low, target) && !NOISE_EXTS[strings.ToLower(filepath.Ext(info.Name()))] {
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

// resolveUnder resolves an absolute or workspace-relative path strictly under
// the workspace. Returns "" when the input is unusable.
func resolveUnder(workspace, p string) string {
	if p == "" {
		p = "."
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(workspace, p)
	}
	return filepath.Clean(p)
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
