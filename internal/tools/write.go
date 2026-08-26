package tools

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile creates or overwrites a file with the given content.
func WriteFile(workspace, path, content string) string {
	filepathAbs := resolveUnder(workspace, path)
	if err := os.MkdirAll(filepath.Dir(filepathAbs), 0o755); err != nil {
		return fmt.Sprintf("Error writing file: %v", err)
	}
	// Write bytes as-is: no newline translation, no syntax checking.
	if err := os.WriteFile(filepathAbs, []byte(content), 0o644); err != nil {
		return fmt.Sprintf("Error writing file: %v", err)
	}
	return fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), filepathAbs)
}
