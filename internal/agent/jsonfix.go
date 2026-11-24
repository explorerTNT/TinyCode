package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var (
	trailingCommaRe = regexp.MustCompile(`,(\s*[}\]])`)
	controlCharRe   = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f]`)
)

// parseToolArgs decodes the argument object of a tool call. Small models
// often emit slightly broken JSON, so a repair pass runs when strict parsing
// fails. truncated reports that the repair had to close a string or a
// bracket: the generation was cut off, so the call must not be trusted.
func parseToolArgs(text string) (args map[string]any, truncated, ok bool) {
	s := strings.TrimSpace(text)
	if s == "" {
		return nil, false, false
	}
	if json.Unmarshal([]byte(s), &args) == nil && args != nil {
		return args, false, true
	}
	args = nil
	repaired, closed := repairJSON(s)
	if json.Unmarshal([]byte(repaired), &args) != nil || args == nil {
		return nil, false, false
	}
	return args, closed, true
}

// decodeArgs is parseToolArgs for callers that only need the object.
func decodeArgs(text string) map[string]any {
	args, _, _ := parseToolArgs(text)
	if args == nil {
		return map[string]any{}
	}
	return args
}

// repairJSON applies the common repairs a 2B model's output needs: trailing
// commas, single-quoted strings, raw line breaks inside strings, and missing
// closing quotes or brackets. completed reports that a quote or bracket had
// to be added, which means the text was cut off.
func repairJSON(input string) (string, bool) {
	s := trailingCommaRe.ReplaceAllString(input, "$1")
	s, unterminated := fixSingleQuotes(s)
	s = escapeControlChars(s)
	s, unclosed := balance(s)
	// Closing brackets can expose a trailing comma, as in {"a": 1,}.
	return trailingCommaRe.ReplaceAllString(s, "$1"), unterminated || unclosed
}

// fixSingleQuotes converts single-quoted strings to double-quoted ones,
// escaping inner double quotes and backslashes, since a single-quoted
// Windows path is meant literally. unterminated reports a string that was
// never closed.
func fixSingleQuotes(s string) (string, bool) {
	var b strings.Builder
	inDouble := false
	escaped := false
	unterminated := false
	last := ' '
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if inDouble {
			b.WriteRune(c)
			last = c
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inDouble = false
			}
			continue
		}
		switch c {
		case '"':
			inDouble = true
			b.WriteRune(c)
			last = c
		case '\'':
			// Only a quote where a value is expected (after {, [, , or :)
			// opens a string; elsewhere it is an apostrophe.
			switch last {
			case ' ', '{', '[', ',', ':':
			default:
				b.WriteRune(c)
				continue
			}
			b.WriteRune('"')
			terminated := false
			for i++; i < len(runes); i++ {
				d := runes[i]
				if d == '\'' {
					terminated = true
					break
				}
				switch d {
				case '\\':
					b.WriteString(`\\`)
				case '"':
					b.WriteString(`\"`)
				case '\n':
					b.WriteString(`\n`)
				case '\r':
					b.WriteString(`\r`)
				case '\t':
					b.WriteString(`\t`)
				default:
					b.WriteRune(d)
				}
			}
			b.WriteRune('"')
			last = '"'
			if !terminated {
				unterminated = true
			}
		default:
			b.WriteRune(c)
			switch c {
			case ' ', '\t', '\n', '\r':
			default:
				last = c
			}
		}
	}
	return b.String(), unterminated
}

// escapeControlChars turns raw line breaks, tabs and other control
// characters inside JSON strings into escapes. It leaves existing escapes
// alone, so "\n" written by the model stays a single escape.
func escapeControlChars(s string) string {
	if !strings.ContainsAny(s, "\r\n\t") && !controlCharRe.MatchString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	escaped := false
	for _, c := range s {
		if inString {
			if escaped {
				escaped = false
				b.WriteRune(c)
				continue
			}
			switch {
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			case c == '\n':
				b.WriteString(`\n`)
				continue
			case c == '\r':
				b.WriteString(`\r`)
				continue
			case c == '\t':
				b.WriteString(`\t`)
				continue
			case c < 0x20:
				fmt.Fprintf(&b, `\u%04x`, c)
				continue
			}
			b.WriteRune(c)
			continue
		}
		if c == '"' {
			inString = true
		}
		b.WriteRune(c)
	}
	return b.String()
}

// balance appends missing closing brackets/braces and closes an unterminated
// string. completed reports that anything had to be added.
func balance(s string) (string, bool) {
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

	completed := inString || braceDepth > 0 || bracketDepth > 0
	switch {
	case inString:
		s += `"`
	case reDanglingKey.MatchString(s):
		// Cut right after "key": give the key an empty value.
		s += `""`
		completed = true
	}
	for i := 0; i < bracketDepth; i++ {
		s += "]"
	}
	for i := 0; i < braceDepth; i++ {
		s += "}"
	}
	return s, completed
}

// reDanglingKey matches a colon that ends the text, with no value after it.
var reDanglingKey = regexp.MustCompile(`:\s*$`)
