package tools

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// maxOutputChars is the budget for one tool result. The agent cuts anything
// longer at 4000 characters, in the middle of a line; tools stop on a line
// boundary before that happens.
const maxOutputChars = 3600

// truncateRunes cuts s to at most n bytes without splitting a character.
func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// lineBudget collects whole lines until the result budget is used up. What
// no longer fits is only counted, so the caller can tell how much was left
// out.
type lineBudget struct {
	lines []string
	chars int
	full  bool
}

func (b *lineBudget) add(line string) {
	if b.full || b.chars+len(line) > maxOutputChars {
		b.full = true
		return
	}
	b.chars += len(line) + 1
	b.lines = append(b.lines, line)
}

// omitted returns a note about the lines that did not fit, or "" when
// nothing was skipped.
func (b *lineBudget) omitted(skipped int, what string) string {
	if skipped <= 0 {
		return ""
	}
	return fmt.Sprintf("... (%d more %s not shown, narrow the search)", skipped, what)
}

func (b *lineBudget) String() string { return strings.Join(b.lines, "\n") }

// clip cuts s to at most limit bytes on a line boundary. fromEnd keeps the
// tail of the output, which is where compilers and test runners report
// failures.
func clip(s string, limit int, fromEnd bool) string {
	const headMark = "\n... (output clipped)"
	const tailMark = "... (output clipped, showing the end)\n"
	mark := headMark
	if fromEnd {
		mark = tailMark
	}
	if limit <= len(mark) {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	if fromEnd {
		tail := s[len(s)-(limit-len(mark)):]
		if i := strings.IndexByte(tail, '\n'); i >= 0 {
			tail = tail[i+1:]
		}
		for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
			tail = tail[1:]
		}
		return tailMark + tail
	}
	cut := truncateRunes(s, limit-len(mark))
	if i := strings.LastIndexByte(cut, '\n'); i >= 0 {
		cut = cut[:i]
	}
	return cut + headMark
}
