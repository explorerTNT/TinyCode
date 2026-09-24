package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/explorerTNT/TinyCode/internal/config"
	"github.com/explorerTNT/TinyCode/internal/i18n"
	"github.com/explorerTNT/TinyCode/internal/updater"
)

// updateFinishedMsg carries the result of an in-app update run back into
// the Update loop.
type updateFinishedMsg struct {
	tag       string
	installed bool
	err       error
}

// Indirections so tests can stub the network-facing parts.
var (
	updateCheck   = updater.CheckLatest
	updateInstall = updater.Install
)

// isUpdateCommand reports whether value is the /update slash command
// (with or without trailing arguments).
func isUpdateCommand(value string) bool {
	token := value
	if i := strings.IndexAny(value, " \t"); i >= 0 {
		token = value[:i]
	}
	return token == "/update"
}

// beginUpdate starts an async check->install run. Returns nil when an
// update is already in progress.
func (m *model) beginUpdate(raw string) tea.Cmd {
	if m.updateRunning {
		return nil
	}
	m.updateRunning = true
	m.appendLog(">>> " + raw)
	m.appendLog(i18n.T("update.checking"))
	return updateCmd(m.cfg.Version)
}

// updateCmd checks the latest release and installs it when newer than the
// running version. Safe to run as a bubbletea command (background goroutine).
func updateCmd(version string) tea.Cmd {
	return func() tea.Msg {
		rel, err := updateCheck()
		if err != nil {
			return updateFinishedMsg{err: err}
		}
		if updater.Compare(rel.Tag, version) <= 0 {
			return updateFinishedMsg{tag: rel.Tag}
		}
		if err := updateInstall(rel); err != nil {
			return updateFinishedMsg{err: err}
		}
		return updateFinishedMsg{tag: rel.Tag, installed: true}
	}
}

// startUpdateCheck watches GitHub for a newer release while the TUI runs
// and posts a single-line hint into the log (stderr is hidden by the
// alt screen).
func startUpdateCheck(p *tea.Program, cfg *config.Config) {
	go func() {
		rel, err := updater.CheckLatest()
		if err != nil {
			return
		}
		if updater.Compare(rel.Tag, cfg.Version) <= 0 {
			return
		}
		p.Send(logMsg{line: i18n.T("update.available", rel.Tag, cfg.Version)})
	}()
}
