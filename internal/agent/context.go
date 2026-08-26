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

// ContextManager tracks an approximate running token count and trims history
// when it would exceed the budget.
type ContextManager struct {
	maxTokens int
	reserve   int
	estimated int
}

func newContextManager(maxTokens int) *ContextManager {
	return &ContextManager{maxTokens: maxTokens, reserve: 4096}
}

func (c *ContextManager) push(m Message) bool {
	tokens := countMessageTokens(m)
	c.estimated += tokens
	return c.estimated < c.maxTokens-c.reserve
}

func (c *ContextManager) reset() {
	c.estimated = 0
}

func (c *ContextManager) pushAll(msgs []Message) {
	for _, m := range msgs {
		c.push(m)
	}
}

// trim drops the oldest messages (after the system prompt) that no longer fit,
// keeping a placeholder marker. Returns the trimmed message list.
func (c *ContextManager) trim(messages []Message) []Message {
	if len(messages) == 0 {
		return messages
	}

	budget := c.maxTokens - c.reserve
	c.estimated = 0
	for _, m := range messages {
		c.estimated += countMessageTokens(m)
	}
	if c.estimated < budget {
		return messages
	}

	system := messages[0]
	rest := messages[1:]

	marker := Message{Role: "system", Content: "[earlier context trimmed]"}
	total := countMessageTokens(system) + countMessageTokens(marker)
	var picked []Message
	for i := len(rest) - 1; i >= 0; i-- {
		t := countMessageTokens(rest[i])
		if total+t > budget {
			break
		}
		picked = append(picked, rest[i])
		total += t
	}

	dropped := len(rest) - len(picked)
	reverseMessages(picked)
	picked = dropOrphanTools(picked)

	kept := []Message{system}
	if dropped > 0 {
		kept = append(kept, marker)
	}
	kept = append(kept, picked...)

	c.estimated = 0
	for _, m := range kept {
		c.estimated += countMessageTokens(m)
	}
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
