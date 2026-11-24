package agent

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/explorerTNT/TinyCode/internal/config"
)

func bigMessage(role, text string) Message {
	return Message{Role: role, Content: text}
}

func TestTrimMessagesUnderBudget(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	if got := trimMessages(msgs, 1_000_000, 4096); len(got) != len(msgs) {
		t.Fatalf("history under budget must be kept as is, got %d messages", len(got))
	}
}

// A request larger than the whole budget still travels with its results:
// without it the model would answer a question it never saw.
func TestTrimMessagesKeepsLatestRequest(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		bigMessage("user", "old request "+strings.Repeat("a", 3000)),
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c0", Name: "read_file"}}},
		{Role: "tool", ToolCallID: "c0", Content: strings.Repeat("x", 8000)},
		bigMessage("user", "REAL REQUEST "+strings.Repeat("b", 20000)),
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "read_file"}}},
		{Role: "tool", ToolCallID: "c1", Content: strings.Repeat("y", 8000)},
		{Role: "user", Content: "keep going", Nudge: true},
	}
	got := trimMessages(msgs, 3000+4096, 4096)
	if len(got) != 6 || got[1].Content != "[earlier context trimmed]" {
		t.Fatalf("unexpected trimmed history (%d messages)", len(got))
	}
	if !strings.HasPrefix(got[2].Content, "REAL REQUEST") || got[2].Nudge {
		t.Fatalf("the latest request was dropped: %q", got[2].Content)
	}
	for _, m := range got[2:] {
		if strings.Contains(m.Content, "old request") {
			t.Fatal("older request should have been dropped")
		}
	}
}

// The anchor is the user's request, not an agent-written nudge after it.
func TestTrimMessagesAnchorsOnRealRequest(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "OLD " + strings.Repeat("a", 24000)},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "NEW " + strings.Repeat("b", 3000)},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "read_file"}}},
		{Role: "tool", ToolCallID: "c1", Content: strings.Repeat("y", 8000)},
		{Role: "user", Content: "keep going", Nudge: true},
	}
	got := trimMessages(msgs, 5000+4096, 4096)
	var kept []string
	for _, m := range got {
		kept = append(kept, m.Content)
		if strings.Contains(m.Content, "OLD") {
			t.Fatal("the oversized old request should have been dropped")
		}
		if strings.HasPrefix(m.Content, "NEW") {
			return
		}
	}
	t.Fatalf("the NEW request was dropped, kept %d messages: %q", len(kept), kept[len(kept)-80:])
}

func TestDropOrphanTools(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "read_file"}}},
		{Role: "tool", ToolCallID: "c1", Content: "kept"},
		{Role: "tool", ToolCallID: "lost", Content: "orphan"},
		{Role: "user", Content: "hi"},
	}
	got := dropOrphanTools(msgs)
	if len(got) != 3 || got[1].Content != "kept" || got[2].Content != "hi" {
		t.Fatalf("unexpected history: %+v", got)
	}
	// A tool result that opens the history has no call before it either.
	if got := dropOrphanTools([]Message{{Role: "tool", ToolCallID: "x"}, {Role: "user", Content: "hi"}}); len(got) != 1 {
		t.Fatalf("leading tool result not dropped: %+v", got)
	}
}

func TestTrimMessagesHandlesEmptyHistory(t *testing.T) {
	if got := trimMessages(nil, 1000, 4096); got != nil {
		t.Fatalf("nil history should stay nil, got %+v", got)
	}
}

func TestClearOldToolResults(t *testing.T) {
	msgs := []Message{{Role: "tool", ToolCallID: "a", Content: "1"}, {Role: "tool", ToolCallID: "b", Content: "2"}}
	got := clearOldToolResults(msgs, 1)
	if msgs[0].Content != "1" || got[0].Content == "1" || got[1].Content != "2" {
		t.Fatalf("original mutated or copy wrong: %+v vs %+v", msgs, got)
	}
}

func TestTruncateUTF8(t *testing.T) {
	s := strings.Repeat("ж", 10) // 20 bytes
	if got := truncateUTF8(s, 7); got != "жжж" {
		t.Fatalf("truncateUTF8 cut a character: %q", got)
	}
	if got := truncateUTF8(s, 100); got != s {
		t.Fatal("short string was cut")
	}
	if got := truncateStr(s, 4); got != "жж" {
		t.Fatalf("truncateStr cut a character: %q", got)
	}
}

// memIO collects what the agent prints and never answers a prompt.
type memIO struct{ lines []string }

func (m *memIO) Printf(format string, args ...any) { m.Println(fmt.Sprintf(format, args...)) }
func (m *memIO) Println(a ...any)                  { m.lines = append(m.lines, fmt.Sprintln(a...)) }
func (m *memIO) Status(string)                     {}
func (m *memIO) Input(string) (string, error)      { return "", io.EOF }

func (m *memIO) text() string { return strings.Join(m.lines, "\n") }

func historyAgent(contextLimit int) *Agent {
	cfg := config.Default()
	cfg.TN.ContextLimit = contextLimit
	a := &Agent{config: cfg, io: &memIO{}, ctx: newContextManager(contextLimit)}
	a.addMsg(Message{Role: "system", Content: "sys"})
	return a
}

// toolExchange appends one call and its result of roughly 900 characters.
func (a *Agent) toolExchange(i int) {
	id := fmt.Sprintf("c%d", i)
	a.addMsg(Message{Role: "assistant", ToolCalls: []ToolCall{{ID: id, Name: "read_file"}}})
	a.addMsg(Message{Role: "tool", ToolCallID: id, Content: strings.Repeat("x", 900)})
}

// The server reuses its prompt cache by common prefix, so a request must
// never differ from the history it is built from.
func TestPrepareMessagesIsAppendOnly(t *testing.T) {
	a := historyAgent(1_000_000)
	for i := 0; i < 8; i++ {
		a.toolExchange(i)
	}
	got := a.prepareMessages()
	if len(got) != 17 {
		t.Fatalf("got %d messages, want 17", len(got))
	}
	for _, m := range got {
		if m.Role == "tool" && strings.Contains(m.Content, "cleared") {
			t.Fatal("prepareMessages rewrote a tool result; that breaks the prompt cache on every call")
		}
	}
}

// Old tool results are cleared in one batch once the context is nearly
// full, which costs the server a single cache miss instead of one per call.
func TestTidyContextShrinksInOneBatch(t *testing.T) {
	a := historyAgent(4096 + 1500)
	for i := 0; i < 8; i++ {
		a.toolExchange(i)
	}
	before := a.estimateTokens()
	if before < 1350 {
		t.Fatalf("test history is too small for the shrink to trigger: %d tokens", before)
	}

	a.tidyContext()
	after := a.estimateTokens()
	if after >= before || after >= 1500 {
		t.Fatalf("history not shrunk below the budget: %d -> %d", before, after)
	}
	full, cleared := 0, 0
	for _, m := range a.snapshotMessages() {
		switch {
		case m.Role != "tool":
		case strings.Contains(m.Content, "cleared"):
			cleared++
		default:
			full++
		}
	}
	if full != 5 || cleared != 3 {
		t.Fatalf("kept %d full results and cleared %d, want 5 and 3", full, cleared)
	}
	if !strings.Contains(a.io.(*memIO).text(), "older tool results cleared") {
		t.Fatal("the user must see that the context was shrunk")
	}

	// Already below the threshold: nothing more happens, in particular no
	// compaction call to the model.
	a.tidyContext()
	if got := a.estimateTokens(); got != after {
		t.Fatalf("a second tidy changed the history: %d -> %d", after, got)
	}
}

func TestTidyContextLeavesSmallHistoryAlone(t *testing.T) {
	a := historyAgent(4096 + 1500)
	a.toolExchange(0)
	before := a.estimateTokens()
	a.tidyContext()
	if got := a.estimateTokens(); got != before || a.io.(*memIO).text() != "" {
		t.Fatal("a small history must not be touched")
	}
}

func TestSyncEnvStateOnlyWhenChanged(t *testing.T) {
	ws := t.TempDir()
	a := &Agent{workspace: ws}

	a.syncEnvState()
	a.syncEnvState()
	if got := a.snapshotMessages(); len(got) != 1 {
		t.Fatalf("unchanged state written twice: %d messages", len(got))
	}

	writeAgentFile(t, filepath.Join(ws, "new.go"), "package main\n")
	a.syncEnvState()
	got := a.snapshotMessages()
	if len(got) != 2 || !strings.Contains(got[1].Content, "new.go") || !got[1].Nudge {
		t.Fatalf("changed listing not recorded: %+v", got)
	}
	a.clearMessages()
	a.syncEnvState()
	if got := a.snapshotMessages(); len(got) != 1 {
		t.Fatalf("after /clear the listing must be written again: %d messages", len(got))
	}
}
