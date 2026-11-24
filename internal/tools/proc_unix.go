//go:build unix

package tools

import (
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/text/encoding"
)

// setKillTree makes a timed-out command take its whole process group with
// it. The shell usually spawns the real worker, and stopping only the shell
// leaves npm, python or a dev server running, holding the output pipes so
// that Wait cannot return before they exit.
func setKillTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 3 * time.Second
}

// hideConsole is a no-op: there is no console window to hide.
func hideConsole(*exec.Cmd) {}

// systemEncoding is nil: POSIX tools are assumed to write UTF-8.
func systemEncoding() encoding.Encoding { return nil }
