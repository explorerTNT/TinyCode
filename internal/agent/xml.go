package agent

import (
	"encoding/json"
	"regexp"
	"strings"
)

var (
	reToolCallBlock = regexp.MustCompile(`(?s)<tool_call>.*?</tool_call>`)
	reFunctionBlock = regexp.MustCompile(`(?s)<function>.*?</function>`)
	reInvokeBlock   = regexp.MustCompile(`(?s)<invoke>.*?</invoke>`)
	reOrphanTags    = regexp.MustCompile(`(?s)</?(?:tool_call|function|invoke|parameter|parameters|arguments)\b[^>]*>`)
	reToolCallInner = regexp.MustCompile(`(?s)<tool_call>(.*?)</tool_call>`)
)

// isXMLToolCall reports whether content is a single XML tool call.
func isXMLToolCall(content string) bool {
	s := strings.TrimSpace(content)
	return strings.HasPrefix(s, "<tool_call>") && strings.HasSuffix(s, "</tool_call>")
}

// stripXMLToolCalls removes XML tool-call markup from a text reply.
func stripXMLToolCalls(text string) string {
	text = reToolCallBlock.ReplaceAllString(text, "")
	text = reFunctionBlock.ReplaceAllString(text, "")
	text = reInvokeBlock.ReplaceAllString(text, "")
	text = reOrphanTags.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

// parseXMLToolCalls extracts tool calls embedded in XML from a text reply.
func parseXMLToolCalls(content string) []ToolCall {
	var calls []ToolCall
	for _, m := range reToolCallInner.FindAllStringSubmatch(content, -1) {
		text := strings.TrimSpace(m[1])
		braceDepth := 0
		start := -1
		for i, ch := range text {
			switch ch {
			case '{':
				if braceDepth == 0 {
					start = i
				}
				braceDepth++
			case '}':
				braceDepth--
				if braceDepth == 0 && start >= 0 {
					parsed := safeJSONLoads(text[start : i+1])
					if parsed != nil {
						if obj, ok := parsed.(map[string]any); ok {
							name, _ := obj["name"].(string)
							args := obj["arguments"]
							if args == nil {
								args = obj["parameters"]
							}
							if argsObj, ok := args.(map[string]any); ok {
								b, _ := json.Marshal(argsObj)
								calls = append(calls, ToolCall{
									ID:        "xml_call_" + itoa(len(calls)),
									Type:      "function",
									Name:      name,
									Arguments: string(b),
								})
							}
						}
					}
					start = -1
				}
			}
		}
	}
	if len(calls) == 0 {
		return nil
	}
	return calls
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
