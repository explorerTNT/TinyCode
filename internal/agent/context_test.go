package agent

import (
	"strings"
	"testing"
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
