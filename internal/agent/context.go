package agent

import (
	"encoding/json"
)

// countTokens approximates token count as UTF-8 bytes / 4, matching the
// Python heuristic.
func countTokens(text string) int {
	if text == "" {
		return 0
	}
	n := len([]byte(text)) / 4
	if n < 1 {
		return 1
	}
	return n
}

func countMessageTokens(m Message) int {
	b, err := json.Marshal(m)
	if err != nil {
		return countTokens(m.Content)
	}
	return countTokens(string(b))
}

// ContextManager holds the token budget the conversation has to fit into.
type ContextManager struct {
	maxTokens int
	reserve   int
}

func newContextManager(maxTokens int) *ContextManager {
	return &ContextManager{maxTokens: maxTokens, reserve: 4096}
}

// trimMessages drops the oldest messages (after the system prompt) that no
// longer fit, keeping a placeholder marker. The user's latest request always
// survives together with everything that follows it. The function touches no
// shared state, so a side-question snapshot can trim its own copy without
// racing the main loop.
func trimMessages(messages []Message, maxTokens, reserve int) []Message {
	if len(messages) == 0 {
		return messages
	}

	budget := maxTokens - reserve
	total := 0
	for _, m := range messages {
		total += countMessageTokens(m)
	}
	if total < budget {
		return messages
	}

	system := messages[0]
	rest := messages[1:]

	// Dropping the request itself would leave the model without a task.
	anchor := -1
	for i := len(rest) - 1; i >= 0; i-- {
		if rest[i].Role == "user" && !rest[i].Nudge {
			anchor = i
			break
		}
	}

	marker := Message{Role: "system", Content: "[earlier context trimmed]"}
	keptTotal := countMessageTokens(system) + countMessageTokens(marker)
	var picked []Message
	for i := len(rest) - 1; i >= 0; i-- {
		t := countMessageTokens(rest[i])
		if keptTotal+t > budget {
			break
		}
		picked = append(picked, rest[i])
		keptTotal += t
	}

	dropped := len(rest) - len(picked)
	reverseMessages(picked)
	if anchor >= 0 && dropped > anchor {
		dropped = anchor
		picked = rest[anchor:]
	}
	picked = dropOrphanTools(picked)

	kept := []Message{system}
	if dropped > 0 {
		kept = append(kept, marker)
	}
	kept = append(kept, picked...)

	return kept
}

func reverseMessages(ms []Message) {
	for i, j := 0, len(ms)-1; i < j; i, j = i+1, j-1 {
		ms[i], ms[j] = ms[j], ms[i]
	}
}

// dropOrphanTools removes tool messages whose tool_call_id is not referenced by
// any assistant tool call, and any leading tool messages.
func dropOrphanTools(messages []Message) []Message {
	known := map[string]bool{}
	for _, m := range messages {
		for _, tc := range m.ToolCalls {
			if tc.ID != "" {
				known[tc.ID] = true
			}
		}
	}

	var result []Message
	for _, m := range messages {
		if m.Role == "tool" && !known[m.ToolCallID] {
			continue
		}
		result = append(result, m)
	}
	for len(result) > 0 && result[0].Role == "tool" {
		result = result[1:]
	}
	return result
}

// clearOldToolResults replaces the raw content of tool messages beyond the
// last N with a placeholder, keeping the fact of the call.
func clearOldToolResults(messages []Message, keep int) []Message {
	var toolIdx []int
	for i, m := range messages {
		if m.Role == "tool" {
			toolIdx = append(toolIdx, i)
		}
	}
	if len(toolIdx) <= keep {
		return messages
	}
	stale := map[int]bool{}
	for _, idx := range toolIdx[:len(toolIdx)-keep] {
		stale[idx] = true
	}

	result := make([]Message, len(messages))
	copy(result, messages)
	for i := range result {
		if stale[i] {
			result[i].Content = "[earlier tool result cleared to save context]"
		}
	}
	return result
}
