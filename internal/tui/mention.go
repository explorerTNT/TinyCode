package tui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/explorerTNT/TinyCode/internal/tools"
)

const (
	mentionMaxBytes = 24 * 1024
	mentionMaxFiles = 20
)

var mentionRe = regexp.MustCompile(`(^|[\s])@(\S+)`)

// resolveMentions expands @path tokens (optionally with a #start-end line
// range) into file contents or directory listings. It leaves unreadable or
// out-of-workspace tokens untouched so the agent can still report them.
func resolveMentions(input, workspace string) string {
	return mentionRe.ReplaceAllStringFunc(input, func(m string) string {
		at := strings.IndexByte(m, '@')
		prefix := m[:at]
		body := strings.TrimSpace(m[at+1:])
		if body == "" {
			return m
		}
		if resolved := resolveMention(body, workspace); resolved != "" {
			return prefix + resolved
		}
		return m
	})
}

func resolveMention(body, workspace string) string {
	pathPart, start, end, hasRange := parseLineRange(body)

	rel := filepath.FromSlash(pathPart)
	abs := filepath.Clean(filepath.Join(workspace, rel))
	if !insideWorkspace(workspace, abs) {
		return ""
	}

	info, err := os.Stat(abs)
	if err != nil {
		return ""
	}

	if info.IsDir() {
		return mentionDirListing(abs, filepath.ToSlash(rel))
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	if isBinary(abs, data) {
		return ""
	}

	content := string(data)
	if hasRange {
		return mentionLineRange(abs, content, start, end)
	}
	return mentionFileInline(abs, content)
}

// parseLineRange splits a mention body into a path and an optional #start-end
// 1-indexed inclusive line range.
func parseLineRange(body string) (path string, start, end int, ok bool) {
	hash := strings.IndexByte(body, '#')
	if hash < 0 {
		return body, 0, 0, false
	}
	path = body[:hash]
	spec := body[hash+1:]
	parts := strings.SplitN(spec, "-", 2)
	s, err := strconv.Atoi(parts[0])
	if err != nil || s < 1 {
		return body, 0, 0, false
	}
	end = s
	if len(parts) == 2 {
		if e, err := strconv.Atoi(parts[1]); err == nil && e >= s {
			end = e
		}
	}
	return path, s, end, true
}

func mentionFileInline(abs, content string) string {
	rel := filepath.ToSlash(abs)
	if len(content) > mentionMaxBytes {
		cut := content[:mentionMaxBytes]
		if idx := strings.LastIndexByte(cut, '\n'); idx > 0 {
			cut = cut[:idx]
		}
		return fmt.Sprintf("<%s>\n%s\n[file truncated: showing first %d bytes; use @%s#L to read a specific range]",
			rel, cut, len(cut), rel)
	}
	return fmt.Sprintf("<%s>\n%s", rel, content)
}

func mentionLineRange(abs, content string, start, end int) string {
	rel := filepath.ToSlash(abs)
	lines := strings.Split(content, "\n")
	total := len(lines)
	if total > 0 && lines[total-1] == "" {
		total--
	}
	if start > total {
		return fmt.Sprintf("<%s>\n[mention: offset %d past end (%d lines total)]", rel, start, total)
	}
	if end > total {
		end = total
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<%s> (lines %d-%d of %d)\n", rel, start, end, total)
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "%d: %s\n", i, lines[i-1])
	}
	return strings.TrimRight(b.String(), "\n")
}

func mentionDirListing(abs, rel string) string {
	entries, err := os.ReadDir(abs)
	if err != nil {
		return ""
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		files = append(files, filepath.ToSlash(filepath.Join(rel, e.Name())))
	}
	sort.Strings(files)
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<%s/> (%d files)\n", rel, len(files))
	for i, f := range files {
		if i >= mentionMaxFiles {
			fmt.Fprintf(&b, "... and %d more files\n", len(files)-mentionMaxFiles)
			break
		}
		fmt.Fprintf(&b, "@%s\n", f)
	}
	return strings.TrimRight(b.String(), "\n")
}

func isBinary(abs string, data []byte) bool {
	if tools.BINARY_EXTS[strings.ToLower(filepath.Ext(abs))] {
		return true
	}
	return bytes.IndexByte(data, 0) >= 0
}

func insideWorkspace(ws, p string) bool {
	rel, err := filepath.Rel(ws, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
