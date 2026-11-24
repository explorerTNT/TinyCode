//go:build !windows && !unix

package tools

import (
	"os/exec"

	"golang.org/x/text/encoding"
)

// setKillTree is a no-op: exec's default kill already stops the shell.
func setKillTree(*exec.Cmd) {}

// systemEncoding is nil: tools are assumed to write UTF-8.
func systemEncoding() encoding.Encoding { return nil }
