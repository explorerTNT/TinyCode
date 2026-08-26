package tools

import (
	"os"
	"path/filepath"
)

func isAbs(p string) bool { return filepath.IsAbs(p) }

func joinPath(base, p string) string { return filepath.Join(base, p) }

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
