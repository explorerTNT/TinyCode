package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BINARY_EXTS are extensions of files that are never treated as text: they
// are not read, not searched and not written to.
var BINARY_EXTS = map[string]bool{
	".ico": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".bmp": true, ".webp": true, ".svgz": true, ".exe": true, ".dll": true,
	".so": true, ".dylib": true, ".pdb": true, ".bin": true, ".obj": true,
	".lib": true, ".a": true, ".o": true, ".zip": true, ".rar": true,
	".7z": true, ".gz": true, ".tar": true, ".xz": true, ".jar": true,
	".nupkg": true, ".pdf": true, ".doc": true, ".docx": true, ".xls": true,
	".xlsx": true, ".ppt": true, ".pptx": true, ".mp3": true, ".mp4": true,
	".avi": true, ".mkv": true, ".wav": true, ".mov": true, ".ttf": true,
	".otf": true, ".woff": true, ".woff2": true, ".pyc": true, ".pyd": true,
	".class": true, ".wasm": true, ".db": true, ".sqlite": true, ".iso": true,
	".onnx": true, ".gguf": true, ".safetensors": true, ".pt": true,
	".pth": true, ".parquet": true,
}

// IsBinaryExt reports whether path has a known binary extension.
func IsBinaryExt(path string) bool {
	return BINARY_EXTS[strings.ToLower(filepath.Ext(path))]
}

const (
	maxReadBytes = 400000
	maxLineChars = 2000
)

// ReadFile reads a file and returns its contents with line numbers.
func ReadFile(workspace, path string, offset, limit int) string {
	filepathAbs := resolveUnder(workspace, path)
	if filepathAbs == "" {
		return outsideWorkspaceError(path)
	}
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

	if IsBinaryExt(filepathAbs) {
		return fmt.Sprintf("Error: '%s' is a binary file (%s), not text. Do not read it. Its name and size are all the information you need.",
			filepath.Base(filepathAbs), fmtSize(info.Size()))
	}

	data, err := os.ReadFile(filepathAbs)
	if err != nil {
		return fmt.Sprintf("Error reading file: %v", err)
	}
	if len(data) > maxReadBytes {
		return fmt.Sprintf("Error: '%s' is too large (%s). Use search_files to find the relevant part, then read with offset/limit.",
			filepath.Base(filepathAbs), fmtSize(info.Size()))
	}
	content, _ := decodeText(data)
	if strings.IndexByte(content, 0) >= 0 {
		return fmt.Sprintf("Error: '%s' appears to be binary (%s), not text. Do not read it.",
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

	lines := strings.Split(content, "\n")
	total := len(lines)
	if total > 0 && lines[total-1] == "" {
		total--
	}

	start := offset - 1
	if start > total {
		return fmt.Sprintf("--- %s (offset %d past end, %d lines total) ---\n", path, offset, total)
	}
	end := start + limit
	if end > total {
		end = total
	}

	// Whole lines only, and only as many as fit the result budget: a note
	// then tells the model exactly where to continue.
	var shown strings.Builder
	for i := start; i < end; i++ {
		line := truncateRunes(lines[i], maxLineChars)
		entry := fmt.Sprintf("%d: %s\n", i+1, line)
		if line != lines[i] {
			entry = fmt.Sprintf("%d: %s ... [line truncated]\n", i+1, line)
		}
		if i > start && shown.Len()+len(entry) > maxOutputChars {
			end = i
			break
		}
		shown.WriteString(entry)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s (lines %d-%d of %d) ---\n", path, start+1, end, total)
	b.WriteString(strings.TrimRight(shown.String(), "\n"))
	if end < total {
		fmt.Fprintf(&b, "\n... (showing %d of %d lines, use offset=%d for more)", end-start, total, end+1)
	}
	return b.String()
}
