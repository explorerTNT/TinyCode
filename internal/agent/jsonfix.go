package agent

import (
	"encoding/json"
	"regexp"
	"strings"
)

var trailingCommaRe = regexp.MustCompile(`,(\s*[}\]])`)

// safeJSONLoads parses JSON, applying a lightweight json_repair-style fix when
// strict parsing fails (trailing commas, single quotes, truncated output).
func safeJSONLoads(text string) any {
	s := strings.TrimSpace(text)
	if s == "" {
		return nil
	}
	var v any
	if json.Unmarshal([]byte(s), &v) == nil {
		return v
	}
	if r := repairJSON(s); r != s {
		if json.Unmarshal([]byte(r), &v) == nil {
			return v
		}
	}
	return nil
}

// repairJSON applies the common repairs a 2B model's output needs.
func repairJSON(input string) string {
	s := trailingCommaRe.ReplaceAllString(input, "$1")
	s = fixSingleQuotes(s)
	s = balance(s)
	return s
}

// fixSingleQuotes converts single-quoted strings to double-quoted, leaving
// apostrophes inside double-quoted strings alone.
func fixSingleQuotes(s string) string {
	var b strings.Builder
	inDouble := false
	escaped := false
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if inDouble {
			b.WriteRune(c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inDouble = false
			}
			continue
		}
		switch c {
		case '"':
			inDouble = true
			b.WriteRune(c)
		case '\'':
			// Only treat a quote as a string delimiter when it appears where a
			// value is expected (after {, [, , or :).
			if prevNonSpaceIsValueStart(b.String()) {
				b.WriteRune('"')
				// Consume until the closing quote, escaping inner double quotes.
				i++
				for i < len(runes) {
					d := runes[i]
					if d == '\\' && i+1 < len(runes) {
						b.WriteRune(d)
						i++
						b.WriteRune(runes[i])
						i++
						continue
					}
					if d == '"' {
						b.WriteRune('\\')
						b.WriteRune('"')
					} else if d == '\'' {
						b.WriteRune('"')
						break
					} else {
						b.WriteRune(d)
					}
					i++
				}
			} else {
				b.WriteRune(c)
			}
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

func prevNonSpaceIsValueStart(s string) bool {
	trimmed := strings.TrimRight(s, " \t\r\n")
	if trimmed == "" {
		return true
	}
	last := trimmed[len(trimmed)-1]
	switch last {
	case '{', '[', ',', ':':
		return true
	}
	return false
}

// balance appends missing closing brackets/braces and closes an unterminated
// string, so truncated generation still parses.
func balance(s string) string {
	inString := false
	escaped := false
	braceDepth := 0
	bracketDepth := 0
	for _, c := range s {
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		}
	}

	if inString {
		s += `"`
	}
	for i := 0; i < bracketDepth; i++ {
		s += "]"
	}
	for i := 0; i < braceDepth; i++ {
		s += "}"
	}
	return s
}
