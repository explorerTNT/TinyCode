package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/explorerTNT/TinyCode/internal/config"
	"github.com/explorerTNT/TinyCode/internal/i18n"
	"github.com/explorerTNT/TinyCode/internal/updater"
)

var errTest = errors.New("boom")

// idleModel is a TUI whose agent is waiting for the user's next prompt.
func idleModel(t *testing.T) *model {
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
	m.inputActive = true
	m.answerPending = true
	m.input.Focus()
	return m
}

// /update typed at the main prompt must not strand the agent: it is still
// waiting in Input() for the answer the command used to swallow.
func TestUpdateKeepsPromptOpen(t *testing.T) {
	for _, finished := range []tea.Msg{
		updateFinishedMsg{tag: "v1.0.0"},
		updateFinishedMsg{err: errTest},
		updateFinishedMsg{tag: "v9.9.9", installed: true},
	} {
		m := idleModel(t)
		stubUpdate(t,
			func() (*updater.Release, error) { return &updater.Release{Tag: "v9.9.9"}, nil },
			func(*updater.Release) error { return nil },
		)
		m.input.SetValue("/update")

		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("expected an update command")
		}
		msg, ok := cmd().(updateFinishedMsg)
		if !ok {
			t.Fatalf("cmd returned %T", msg)
		}
		m.Update(finished)

		if !m.inputActive || !m.answerPending {
			t.Fatalf("after %+v the prompt is closed and the agent can never receive input", finished)
		}
		if m.input.Value() != "" {
			t.Fatalf("input not cleared: %q", m.input.Value())
		}
		if len(m.answerCh) != 0 {
			t.Fatal("/update must not put anything on the answer channel")
		}

		// The next request reaches the agent normally.
		m.input.SetValue("do the thing")
		if cmd := m.submitInput(); cmd != nil {
			t.Fatal("submitting a request must not return a command")
		}
		if got := <-m.answerCh; got != "do the thing" {
			t.Fatalf("agent received %q", got)
		}
	}
}

func TestUpdateWhileRunningIsReported(t *testing.T) {
	m := idleModel(t)
	m.updateRunning = true
	if cmd := m.beginUpdate("/update"); cmd != nil {
		t.Fatal("a second concurrent update must not start")
	}
	if !strings.Contains(logText(m), i18n.T("update.busy")) {
		t.Fatalf("the user must learn why nothing happened:\n%s", logText(m))
	}
}

// A mention inserted from the file tree while the agent is busy goes to the
// side-question input. Sending it to answerCh would let the next prompt of
// the agent (say, a permission request) consume it as an answer.
func TestMentionWhileBusyStaysInSideInput(t *testing.T) {
	m := idleModel(t)
	m.inputActive, m.answerPending = false, false
	dir := m.cfg.TN.Workspace
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.rebuildTree()
	var node *treeNode
	for _, f := range m.fileIndex {
		if f.rel == "a.txt" {
			node = &treeNode{name: "a.txt", path: f.abs}
		}
	}
	if node == nil {
		t.Fatal("file not indexed")
	}

	m.insertMention(node)
	if !m.sideMode || m.inputActive {
		t.Fatalf("busy agent: mention must open the side input (side=%v active=%v)", m.sideMode, m.inputActive)
	}
	if got := m.input.Value(); got != "@a.txt " {
		t.Fatalf("input = %q", got)
	}
	if q, ok := parseBtw(m.input.Value() + "/btw what is in here"); !ok || q != "@a.txt what is in here" {
		t.Fatalf("the mention in front of /btw must survive: (%q, %v)", q, ok)
	}
}

func TestMentionWhileIdleGoesToPrompt(t *testing.T) {
	m := idleModel(t)
	m.prompt = "[Plan ready. Approve and execute? (y/n/edit)] "
	node := &treeNode{name: "a.txt", path: filepath.Join(m.cfg.TN.Workspace, "a.txt")}
	m.insertMention(node)
	if !m.inputActive || m.sideMode || m.prompt == "" {
		t.Fatalf("idle agent: mention must go to the waiting prompt (active=%v side=%v prompt=%q)", m.inputActive, m.sideMode, m.prompt)
	}
}

func TestParseBtw(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/btw what is this", "what is this", true},
		{"/btw", "", false},
		{"@src/a.go /btw what is this", "@src/a.go what is this", true},
		{"/btw @src/a.go", "@src/a.go", true},
		{"/help me", "", false},
		{"/btwx nope", "", false},
	}
	for _, c := range cases {
		if got, ok := parseBtw(c.in); got != c.want || ok != c.ok {
			t.Errorf("parseBtw(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestTypedRunes(t *testing.T) {
	i := newInput()
	i.Focus()
	i.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a\x1b[200~b\nc\td\r\ne\x1b[201~")})
	if got := i.Value(); got != "ab c d  e" {
		t.Fatalf("input = %q", got)
	}
}

func TestAcceptCompletionAfterCyrillic(t *testing.T) {
	i := newInput()
	i.SetValue("/пла")
	i.SetCompletion("/plan")
	i.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := i.Value(); got != "/пла" {
		t.Fatalf("a completion that is not a prefix must not be applied: %q", got)
	}

	i.SetValue("/pl")
	i.SetCompletion("/plan")
	i.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := i.Value(); got != "/plan" {
		t.Fatalf("completion = %q", got)
	}
}

func TestMentionLineRangeIsCapped(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 4000; i++ {
		b.WriteString("line with some content\n")
	}
	writeFile(t, dir, "big.txt", b.String())

	got := resolveMentions("@big.txt#1-4000", dir)
	if len(got) > mentionMaxBytes+200 {
		t.Fatalf("mention is %d bytes, cap is %d", len(got), mentionMaxBytes)
	}
	if !strings.Contains(got, "mention truncated at line") {
		t.Fatalf("a capped range must say where it stopped:\n...%s", got[len(got)-120:])
	}
}
