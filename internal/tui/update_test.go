package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/explorerTNT/TinyCode/internal/config"
	"github.com/explorerTNT/TinyCode/internal/i18n"
	"github.com/explorerTNT/TinyCode/internal/updater"
)

func stubUpdate(t *testing.T, check func() (*updater.Release, error), install func(*updater.Release) error) {
	t.Helper()
	oc, oi := updateCheck, updateInstall
	updateCheck, updateInstall = check, install
	t.Cleanup(func() { updateCheck, updateInstall = oc, oi })
}

func updateModel(t *testing.T) *model {
	t.Helper()
	i18n.Set(i18n.En)
	cfg := &config.Config{
		Language: "en",
		LM:       &config.LMConfig{Name: "m"},
		TN:       &config.TNConfig{Workspace: t.TempDir(), PermissionMode: "ask"},
		Version:  "v1.0.0",
	}
	m := newModel(cfg)
	m.width, m.height = 100, 40
	m.ready = true
	return m
}

func logText(m *model) string { return strings.Join(m.log, "\n") }

func TestUpdateSlashResolve(t *testing.T) {
	i18n.Set(i18n.En)
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"/update", "/update", true},
		{"/upd", "/update", true},
		{"/update now", "/update now", true},
	}
	for _, c := range cases {
		got, ok := resolveSlash(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("resolveSlash(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
	// submitInput matches on the resolveSlash-normalized value; side mode
	// receives the raw text (no autocomplete there, full /update expected).
	if !isUpdateCommand("/update") || !isUpdateCommand("/update arg") {
		t.Error("isUpdateCommand should match /update with optional args")
	}
	if isUpdateCommand("/upd") || isUpdateCommand("/updates") || isUpdateCommand("/btw hi") {
		t.Error("isUpdateCommand false positive")
	}
}

func TestUpdateSubmitIntercepted(t *testing.T) {
	m := updateModel(t)
	installed := false
	stubUpdate(t,
		func() (*updater.Release, error) { return &updater.Release{Tag: "v9.9.9"}, nil },
		func(*updater.Release) error { installed = true; return nil },
	)

	m.inputActive = true
	m.input.Focus()
	m.input.SetValue("/update")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected update command, got nil")
	}
	if len(m.answerCh) != 0 {
		t.Fatalf("answerCh must stay empty for /update, got %d msg(s)", len(m.answerCh))
	}
	if !m.updateRunning {
		t.Fatal("updateRunning not set")
	}
	if !strings.Contains(logText(m), "Checking for updates") {
		t.Fatalf("expected checking log line, got:\n%s", logText(m))
	}

	finished, ok := cmd().(updateFinishedMsg)
	if !ok {
		t.Fatal("cmd did not return updateFinishedMsg")
	}
	if finished.err != nil || !finished.installed || finished.tag != "v9.9.9" {
		t.Fatalf("finished = %+v", finished)
	}
	if !installed {
		t.Fatal("install was not called")
	}

	m.Update(finished)
	if m.updateRunning {
		t.Fatal("updateRunning should clear after finish")
	}
	if !strings.Contains(logText(m), "Updated to v9.9.9") {
		t.Fatalf("expected done log line, got:\n%s", logText(m))
	}
}

func TestUpdateAlreadyLatest(t *testing.T) {
	m := updateModel(t)
	stubUpdate(t,
		func() (*updater.Release, error) { return &updater.Release{Tag: "v1.0.0"}, nil },
		func(*updater.Release) error { t.Fatal("install must not run when up to date"); return nil },
	)

	m.inputActive = true
	m.input.SetValue("/update")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected update command")
	}
	finished := cmd().(updateFinishedMsg)
	if finished.installed || finished.err != nil {
		t.Fatalf("finished = %+v", finished)
	}
	m.Update(finished)
	if !strings.Contains(logText(m), "Already on the latest version") {
		t.Fatalf("expected latest log line, got:\n%s", logText(m))
	}
}

func TestUpdateCheckError(t *testing.T) {
	m := updateModel(t)
	stubUpdate(t,
		func() (*updater.Release, error) { return nil, errors.New("boom") },
		func(*updater.Release) error { t.Fatal("install must not run on check error"); return nil },
	)

	m.inputActive = true
	m.input.SetValue("/update")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	finished := cmd().(updateFinishedMsg)
	if finished.err == nil {
		t.Fatal("expected error in finished msg")
	}
	m.Update(finished)
	if !strings.Contains(logText(m), "Update failed") {
		t.Fatalf("expected failed log line, got:\n%s", logText(m))
	}
}

func TestUpdateSideModeIntercepted(t *testing.T) {
	m := updateModel(t)
	stubUpdate(t,
		func() (*updater.Release, error) { return &updater.Release{Tag: "v9.9.9"}, nil },
		func(*updater.Release) error { return nil },
	)

	m.sideMode = true
	m.input.Focus()
	m.input.SetValue("/update")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected update command from side mode")
	}
	if len(m.answerCh) != 0 {
		t.Fatalf("answerCh must stay empty in side mode, got %d", len(m.answerCh))
	}
	if !m.updateRunning {
		t.Fatal("updateRunning not set")
	}
}
