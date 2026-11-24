package tools

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile creates or overwrites a file with the given content. A file that
// already exists keeps its encoding.
func WriteFile(workspace, path, content string) string {
	filepathAbs := resolveUnder(workspace, path)
	if filepathAbs == "" {
		return outsideWorkspaceError(path)
	}
	enc := encUTF8
	if info, err := os.Stat(filepathAbs); err == nil {
		if info.IsDir() {
			return fmt.Sprintf("Error: %s is a directory", path)
		}
		if old, err := os.ReadFile(filepathAbs); err == nil {
			_, enc = decodeText(old)
		}
	}
	if err := os.MkdirAll(filepath.Dir(filepathAbs), 0o755); err != nil {
		return fmt.Sprintf("Error writing file: %v", err)
	}
	// Bytes are written in the file's encoding: no newline translation, no
	// syntax checking.
	if err := writeText(filepathAbs, content, enc); err != nil {
		return fmt.Sprintf("Error writing file: %v", err)
	}
	return fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path)
}
