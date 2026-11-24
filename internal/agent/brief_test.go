package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/explorerTNT/TinyCode/internal/config"
)

func briefAgent(t *testing.T, ws string) *Agent {
	t.Helper()
	cfg := config.Default()
	cfg.TN.Workspace = ws
	cfg.TN.RepoMapChars = 4000
	a := &Agent{config: cfg, workspace: ws}
	a.brief = loadBrief(ws, cfg.TN.RepoMapChars)
	return a
}

func TestBriefGoesIntoTheSystemPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	ws := t.TempDir()
	writeAgentFile(t, filepath.Join(ws, "main.go"), "package main\n\nfunc main() {}\n")
	writeAgentFile(t, filepath.Join(ws, "AGENTS.md"), "\uFEFF# Rules\r\n\r\nRun tests with `go test ./...`.\r\n")
	writeAgentFile(t, filepath.Join(home, ".tinycode", "AGENTS.md"), "Never use an em dash.")

	a := briefAgent(t, ws)
	prompt := a.systemPrompt()
	for _, want := range []string{
		"You are tiny-code", "PROJECT MAP", "main.go: main",
		"PROJECT INSTRUCTIONS", "Run tests with `go test ./...`.",
		"USER PREFERENCES", "Never use an em dash.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("system prompt misses %q", want)
		}
	}
	if strings.Contains(prompt, "\uFEFF") || strings.Contains(prompt, "\r") {
		t.Error("BOM and CRLF from the instruction file leaked into the prompt")
	}
	if !strings.Contains(a.planPrompt(), "PLAN MODE") || !strings.Contains(a.planPrompt(), "Run tests with") {
		t.Error("plan mode must carry the brief too")
	}
}

func TestBriefIsOptional(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	a := briefAgent(t, t.TempDir())
	if got := a.systemPrompt(); got != SYSTEM_PROMPT {
		t.Fatalf("without instruction files the prompt must stay the base one:\n%s", got[len(SYSTEM_PROMPT):])
	}
}

func TestInstructionFilePreference(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	ws := t.TempDir()
	writeAgentFile(t, filepath.Join(ws, "CLAUDE.md"), "from claude")
	writeAgentFile(t, filepath.Join(ws, "AGENTS.md"), "from agents")
	if b := loadBrief(ws, 0); b.project != "from agents" {
		t.Fatalf("AGENTS.md must win: %q", b.project)
	}
}

func TestLongInstructionsAreCut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	writeAgentFile(t, path, strings.Repeat("я", 4000))
	got := readInstructions(path)
	if len([]rune(got)) > 3000+70 || !strings.Contains(got, "was cut here") {
		t.Fatalf("long file not capped: %d runes", len([]rune(got)))
	}
	if readInstructions(filepath.Join(t.TempDir(), "missing.md")) != "" {
		t.Fatal("a missing file must read as empty")
	}
}

// Editing the instruction file between two requests takes effect from the
// next request on, and leaves the rest of the history alone.
func TestRefreshBriefResyncsSystemMessage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	ws := t.TempDir()
	a := briefAgent(t, ws)

	a.refreshBrief()
	a.addMsg(Message{Role: "user", Content: "hello"})
	writeAgentFile(t, filepath.Join(ws, "AGENTS.md"), "Use tabs.")
	a.refreshBrief()

	if len(a.messages) != 2 || a.messages[1].Content != "hello" {
		t.Fatalf("history disturbed: %+v", a.messages)
	}
	if !strings.Contains(a.messages[0].Content, "Use tabs.") {
		t.Fatal("the changed instruction file was not picked up")
	}
}

func writeAgentFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
