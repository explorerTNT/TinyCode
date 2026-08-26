package tools

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BINARY_EXTS are extensions never read as text.
var BINARY_EXTS = map[string]bool{
	".ico": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".bmp": true, ".webp": true, ".svgz": true, ".exe": true, ".dll": true,
	".so": true, ".dylib": true, ".pdb": true, ".bin": true, ".obj": true,
	".lib": true, ".zip": true, ".rar": true, ".7z": true, ".gz": true,
	".tar": true, ".xz": true, ".jar": true, ".nupkg": true, ".pdf": true,
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true,
	".pptx": true, ".mp3": true, ".mp4": true, ".avi": true, ".mkv": true,
	".wav": true, ".mov": true, ".ttf": true, ".otf": true, ".woff": true,
	".woff2": true, ".pyc": true, ".pyd": true, ".class": true, ".wasm": true,
	".db": true, ".sqlite": true,
}

const (
	maxReadBytes = 400000
	maxLineChars = 2000
)

// ReadFile reads a file and returns its contents with line numbers.
func ReadFile(workspace, path string, offset, limit int) string {
	filepathAbs := resolveUnder(workspace, path)
	info, err := os.Stat(filepathAbs)
	if os.IsNotExist(err) {
		msg := fmt.Sprintf("Error: File not found: %s", path)
		candidates := SuggestFiles(workspace, path, 5)
		if len(candidates) > 0 {
			msg += " Did you mean:\n" + strings.Join(candidates, "\n  ")
		}
		return msg
	}
	if err != nil {
		return fmt.Sprintf("Error reading file: %v", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Sprintf("Error: Not a file: %s", path)
	}

	if BINARY_EXTS[strings.ToLower(filepath.Ext(filepathAbs))] {
		return fmt.Sprintf("Error: '%s' is a binary file (%s), not text. Do not read it. Its name and size are all the information you need.",
			filepath.Base(filepathAbs), fmtSize(info.Size()))
	}

	data, err := os.ReadFile(filepathAbs)
	if err != nil {
		return fmt.Sprintf("Error reading file: %v", err)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return fmt.Sprintf("Error: '%s' appears to be binary (%s), not text. Do not read it.",
			filepath.Base(filepathAbs), fmtSize(info.Size()))
	}
	if len(data) > maxReadBytes {
		return fmt.Sprintf("Error: '%s' is too large (%s). Use search_files to find the relevant part, then read with offset/limit.",
			filepath.Base(filepathAbs), fmtSize(info.Size()))
	}

	if offset < 1 {
		offset = 1
	}
	if limit < 1 {
		limit = 2000
	}
	if limit > 5000 {
		limit = 5000
	}

	lines := strings.Split(string(data), "\n")
	total := len(lines)
	if total > 0 && lines[total-1] == "" {
		total--
	}

	start := offset - 1
	end := start + limit
	if start > total {
		return fmt.Sprintf("--- %s (offset %d past end, %d lines total) ---\n", filepathAbs, offset, total)
	}
	if end > total {
		end = total
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s (lines %d-%d of %d) ---\n", filepathAbs, start+1, end, total)
	for i := start; i < end; i++ {
		line := lines[i]
		if len(line) > maxLineChars {
			line = line[:maxLineChars] + " ... [line truncated]"
		}
		fmt.Fprintf(&b, "%d: %s\n", i+1, line)
	}
	if end < total {
		fmt.Fprintf(&b, "... (showing %d of %d lines, use offset=%d for more)\n", end-start, total, end+1)
	}
	return strings.TrimRight(b.String(), "\n")
}
