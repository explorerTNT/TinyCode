//go:build windows

package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// PowerShell writes its own messages in the OEM code page unless told
// otherwise; without the UTF-8 prefix Cyrillic arrives as mojibake.
func TestRunBashEmitsUTF8(t *testing.T) {
	ws := t.TempDir()
	if got := RunBash(ws, `Write-Output "привет мир"`, 30); !strings.Contains(got, "привет мир") {
		t.Fatalf("Cyrillic output was garbled: %q", got)
	}
	got := RunBash(ws, `Get-Content "нет такого файла.txt"`, 30)
	if !strings.Contains(got, "Exit code: 1") || !strings.Contains(got, "нет такого файла.txt") {
		t.Fatalf("Cyrillic error message was garbled: %q", got)
	}
}

func TestRunBashDecodesANSIOutput(t *testing.T) {
	if windows.GetACP() != 1251 {
		t.Skip("needs a Windows-1251 system code page")
	}
	if got := toOutputText(string(cp1251(t, "привет"))); got != "привет" {
		t.Fatalf("ANSI output not decoded: %q", got)
	}
}

// A timed-out command must take its process tree with it: stopping only the
// shell would leave a child running with its ports and files held.
func TestTimeoutKillsProcessTree(t *testing.T) {
	ws := t.TempDir()
	script := `$p = Start-Process powershell -WindowStyle Hidden -PassThru -ArgumentList '-NoProfile','-Command','Start-Sleep -Seconds 60'; ` +
		`Set-Content -Path child.pid -Value $p.Id; Start-Sleep -Seconds 60`
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", consoleUTF8+"; "+script)
	cmd.Dir = ws
	setKillTree(cmd)
	start := time.Now()
	err := cmd.Run()
	if ctx.Err() == nil {
		t.Fatalf("command finished with %v before the timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 12*time.Second {
		t.Fatalf("Wait took %v after the timeout", elapsed)
	}

	data, readErr := os.ReadFile(filepath.Join(ws, "child.pid"))
	if readErr != nil {
		t.Skipf("the shell did not report a child pid: %v", readErr)
	}
	pid := strings.TrimSpace(string(data))
	for deadline := time.Now().Add(8 * time.Second); processAlive(pid); {
		if time.Now().After(deadline) {
			t.Fatalf("child process %s survived the timeout", pid)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func processAlive(pid string) bool {
	out, err := exec.Command("tasklist", "/FI", "PID eq "+pid, "/NH", "/FO", "CSV").Output()
	return err == nil && strings.Contains(string(out), `"`+pid+`"`)
}
