package tools

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pmezard/go-difflib/difflib"
	"golang.org/x/text/encoding/charmap"
)

var (
	utf8BOM  = []byte{0xEF, 0xBB, 0xBF}
	reDeclRe = regexp.MustCompile(`^(?:class|def|async\s+def)\s+\w`)
	reDeclP  = regexp.MustCompile(`^\s*((?:async\s+def|def|class)\s+\w+)`)
)

// readText reads a text file, decoding BOM/UTF-8/Windows-1251 and reporting
// whether a UTF-8 BOM was present so it can be written back.
func readText(filepathAbs string) (string, bool, error) {
	b, err := os.ReadFile(filepathAbs)
	if err != nil {
		return "", false, err
	}
	if bytes.HasPrefix(b, utf8BOM) {
		return string(b[len(utf8BOM):]), true, nil
	}
	if utf8.Valid(b) {
		return string(b), false, nil
	}
	if dec, err := charmap.Windows1251.NewDecoder().Bytes(b); err == nil {
		return string(dec), false, nil
	}
	return strings.ToValidUTF8(string(b), "\uFFFD"), false, nil
}

func writeText(filepathAbs, content string, hadBOM bool) error {
	data := []byte(content)
	if hadBOM {
		data = append(append([]byte{}, utf8BOM...), data...)
	}
	return os.WriteFile(filepathAbs, data, 0o644)
}

// matchNewlines re-encodes text with the file's newline style.
func matchNewlines(text, content string) string {
	normalized := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	if strings.Contains(content, "\r\n") {
		return strings.ReplaceAll(normalized, "\n", "\r\n")
	}
	return normalized
}

func seqRatio(a, b string) float64 {
	if a == b {
		return 1.0
	}
	if a == "" || b == "" {
		return 0.0
	}
	return difflib.NewMatcher(runeSeq(a), runeSeq(b)).Ratio()
}

func runeSeq(s string) []string {
	r := []rune(s)
	out := make([]string, len(r))
	for i, c := range r {
		out[i] = string(c)
	}
	return out
}

func replaceLines(filepathAbs, content, shownPath string, hadBOM bool, start, end int, newText string) string {
	newline := "\n"
	if strings.Contains(content, "\r\n") {
		newline = "\r\n"
	}
	lines := strings.Split(content, newline)
	trailing := len(lines) > 0 && lines[len(lines)-1] == ""
	if trailing {
		lines = lines[:len(lines)-1]
	}

	total := len(lines)
	if start < 1 || start > total {
		return fmt.Sprintf("Error: start_line %d is out of range (file has %d lines)", start, total)
	}
	if end < start {
		return fmt.Sprintf("Error: end_line %d is before start_line %d", end, start)
	}
	if end > total {
		return fmt.Sprintf("Error: end_line %d is out of range (file has %d lines)", end, total)
	}

	replaced := lines[start-1 : end]
	newText = strings.ReplaceAll(strings.ReplaceAll(newText, "\r\n", "\n"), "\r", "\n")
	var newLines []string
	if newText != "" {
		newLines = strings.Split(newText, "\n")
	}
	if len(newLines) > 1 && newLines[len(newLines)-1] == "" {
		newLines = newLines[:len(newLines)-1]
	}

	updated := make([]string, 0, len(lines[:start-1])+len(newLines)+len(lines[end:]))
	updated = append(updated, lines[:start-1]...)
	updated = append(updated, newLines...)
	updated = append(updated, lines[end:]...)
	out := strings.Join(updated, newline)
	if trailing {
		out += newline
	}

	if err := writeText(filepathAbs, out, hadBOM); err != nil {
		return fmt.Sprintf("Error editing file: %v", err)
	}

	diff := len(out) - len(content)
	sign := "+"
	if diff < 0 {
		sign = ""
	}
	return fmt.Sprintf("Successfully edited %s (%s%d bytes, lines %d-%d replaced: %d -> %d)",
		shownPath, sign, diff, start, end, len(replaced), len(newLines))
}

// locateBlock finds the byte span in content that probe was meant to match.
func locateBlock(content, probe string) (start, end int, note string, ok bool) {
	probeLines := strings.Split(strings.Trim(probe, "\n"), "\n")
	fileLines := strings.Split(content, "\n")
	span := len(probeLines)
	if span == 0 || span > len(fileLines) {
		return 0, 0, "", false
	}

	offsets := make([]int, len(fileLines))
	pos := 0
	for i, line := range fileLines {
		offsets[i] = pos
		pos += len(line) + 1
	}
	spanBounds := func(i int) (int, int) {
		return offsets[i], offsets[i+span-1] + len(fileLines[i+span-1])
	}

	strippedProbe := make([]string, span)
	for i, l := range probeLines {
		strippedProbe[i] = strings.TrimSpace(l)
	}

	// Level 1: identical ignoring leading/trailing whitespace.
	var exact []int
	for i := 0; i+span <= len(fileLines); i++ {
		match := true
		for j := 0; j < span; j++ {
			if strings.TrimSpace(fileLines[i+j]) != strippedProbe[j] {
				match = false
				break
			}
		}
		if match {
			exact = append(exact, i)
		}
	}
	if len(exact) == 1 {
		s, e := spanBounds(exact[0])
		return s, e, " [matched ignoring indentation]", true
	}
	if len(exact) > 1 {
		return 0, 0, "", false
	}

	// Level 2: near-identical text.
	joinedProbe := strings.Join(strippedProbe, "\n")
	bestI, bestRatio, runnerUp := -1, 0.0, 0.0
	for i := 0; i+span <= len(fileLines); i++ {
		parts := make([]string, span)
		for j := 0; j < span; j++ {
			parts[j] = strings.TrimSpace(fileLines[i+j])
		}
		ratio := seqRatio(joinedProbe, strings.Join(parts, "\n"))
		if ratio > bestRatio {
			bestI, runnerUp, bestRatio = i, bestRatio, ratio
		} else if ratio > runnerUp {
			runnerUp = ratio
		}
	}
	if bestRatio >= 0.92 && bestRatio-runnerUp >= 0.05 {
		s, e := spanBounds(bestI)
		return s, e, fmt.Sprintf(" [fuzzy match, %.0f%% similar]", bestRatio*100), true
	}

	// Level 3: anchor on a declaration.
	if m := reDeclP.FindStringSubmatch(probeLines[0]); m != nil {
		signature := m[1]
		sigRe := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(signature) + `\b`)
		var hits []int
		for i, line := range fileLines {
			if sigRe.MatchString(line) {
				hits = append(hits, i)
			}
		}
		if len(hits) == 1 {
			i := hits[0]
			indent := len(fileLines[i]) - len(strings.TrimLeftFunc(fileLines[i], unicode.IsSpace))
			endI := i + 1
			for endI < len(fileLines) {
				line := fileLines[endI]
				if strings.TrimSpace(line) != "" &&
					len(line)-len(strings.TrimLeftFunc(line, unicode.IsSpace)) <= indent {
					break
				}
				endI++
			}
			return offsets[i], offsets[endI-1] + len(fileLines[endI-1]),
				fmt.Sprintf(" [matched by declaration '%s']", signature), true
		}
	}

	return 0, 0, "", false
}

func nearMiss(content, oldString string, maxHits int) string {
	if maxHits <= 0 {
		maxHits = 3
	}
	var probeLines []string
	for _, ln := range strings.Split(strings.TrimSpace(oldString), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			probeLines = append(probeLines, s)
		}
	}
	if len(probeLines) == 0 {
		return ""
	}

	lines := strings.Split(content, "\n")
	scored := map[int]float64{}
	for _, probe := range probeLines {
		isDecl := reDeclRe.MatchString(probe)
		for ln, line := range lines {
			stripped := strings.TrimSpace(line)
			if stripped == "" {
				continue
			}
			ratio := seqRatio(probe, stripped)
			if ratio < 0.6 {
				continue
			}
			if isDecl && reDeclRe.MatchString(stripped) {
				ratio += 0.5
			}
			if ratio > scored[ln+1] {
				scored[ln+1] = ratio
			}
		}
	}
	if len(scored) == 0 {
		return ""
	}

	if len(probeLines) > 1 {
		boosted := map[int]float64{}
		span := len(probeLines)
		for ln, v := range scored {
			boosted[ln] = v
			for off := 1; off <= span; off++ {
				boosted[ln] += scored[ln+off]
			}
		}
		scored = boosted
	}

	type kv struct {
		ln    int
		score float64
	}
	ranked := make([]kv, 0, len(scored))
	for ln, s := range scored {
		ranked = append(ranked, kv{ln, s})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].ln < ranked[j].ln
	})

	var out []string
	for i := 0; i < len(ranked) && i < maxHits; i++ {
		ln := ranked[i].ln
		out = append(out, fmt.Sprintf("  line %d: %s", ln, truncate(lines[ln-1], 120)))
	}
	if len(ranked) > maxHits {
		out = append(out, fmt.Sprintf("  ... and %d more", len(ranked)-maxHits))
	}
	return strings.Join(out, "\n")
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// asLineNumber coerces a line number sent as text/float. ok=false when unusable.
func asLineNumber(v any) (int, bool) {
	switch x := v.(type) {
	case nil:
		return 0, false
	case bool:
		return 0, false
	case float64:
		if x == float64(int(x)) {
			return int(x), true
		}
		return 0, false
	case int:
		return x, true
	case int64:
		return int(x), true
	case string:
		t := strings.TrimSpace(x)
		if t == "" {
			return 0, false
		}
		if n, err := strconv.Atoi(t); err == nil {
			return n, true
		}
		if f, err := strconv.ParseFloat(t, 64); err == nil && f == float64(int(f)) {
			return int(f), true
		}
		return 0, false
	default:
		return 0, false
	}
}

func isBlankLineArg(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case bool:
		return !x
	case string:
		return strings.TrimSpace(x) == ""
	case float64:
		return x == 0
	case int:
		return x == 0
	case int64:
		return x == 0
	default:
		return false
	}
}

// EditFile replaces text in a file, preferring start_line/end_line.
func EditFile(workspace, path string, oldString, newString string, rawStart, rawEnd any) string {
	filepathAbs := resolveUnder(workspace, path)
	info, err := os.Stat(filepathAbs)
	if os.IsNotExist(err) {
		return fmt.Sprintf("Error: File not found: %s", path)
	}
	if err != nil {
		return fmt.Sprintf("Error editing file: %v", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Sprintf("Error: Not a file: %s", path)
	}

	startLine, okStart := asLineNumber(rawStart)
	if !okStart && !isBlankLineArg(rawStart) {
		return fmt.Sprintf("Error: start_line must be a line number, got %v. Use the numbers printed by read_file.", rawStart)
	}
	endLine, okEnd := asLineNumber(rawEnd)
	if !okEnd && !isBlankLineArg(rawEnd) {
		return fmt.Sprintf("Error: end_line must be a line number, got %v. Use the numbers printed by read_file.", rawEnd)
	}

	content, hadBOM, err := readText(filepathAbs)
	if err != nil {
		return fmt.Sprintf("Error editing file: %v", err)
	}

	if startLine > 0 {
		if oldString != "" {
			note("[edit_file: start_line and old_string both given for %s; using line numbers]", path)
		}
		if endLine == 0 {
			endLine = startLine
		}
		return replaceLines(filepathAbs, content, path, hadBOM, startLine, endLine, newString)
	}

	if oldString == "" {
		return "Error: provide either start_line (preferred, from read_file) or old_string."
	}

	crlf := strings.Contains(content, "\r\n")
	if crlf && !strings.Contains(oldString, "\r\n") {
		contentCmp := strings.ReplaceAll(content, "\r\n", "\n")
		if strings.Count(contentCmp, oldString) == 1 {
			newLF := strings.ReplaceAll(strings.ReplaceAll(newString, "\r\n", "\n"), "\r", "\n")
			newCmp := strings.Replace(contentCmp, oldString, newLF, 1)
			newContent := strings.ReplaceAll(newCmp, "\n", "\r\n")
			if err := writeText(filepathAbs, newContent, hadBOM); err != nil {
				return fmt.Sprintf("Error editing file: %v", err)
			}
			diff := len(newContent) - len(content)
			sign := "+"
			if diff < 0 {
				sign = ""
			}
			return fmt.Sprintf("Successfully edited %s (%s%d bytes, %d lines changed)",
				path, sign, diff, strings.Count(oldString, "\n")+1)
		}
	}

	count := strings.Count(content, oldString)
	if count == 0 {
		start, end, note, ok := locateBlock(content, oldString)
		if ok {
			newContent := content[:start] + matchNewlines(newString, content) + content[end:]
			if err := writeText(filepathAbs, newContent, hadBOM); err != nil {
				return fmt.Sprintf("Error editing file: %v", err)
			}
			diff := len(newContent) - len(content)
			sign := "+"
			if diff < 0 {
				sign = ""
			}
			return fmt.Sprintf("Successfully edited %s (%s%d bytes, %d lines changed)%s",
				path, sign, diff, strings.Count(oldString, "\n")+1, note)
		}

		short := strings.ReplaceAll(truncate(oldString, 50), "\n", "\\n")
		hint := nearMiss(content, oldString, 3)
		msg := fmt.Sprintf("Error: Could not find '%s...' in %s", short, path)
		if hint != "" {
			msg += "\nClosest lines in the file:\n" + hint +
				"\nUse start_line/end_line with those numbers instead of retyping the text."
		} else {
			msg += "\nRun read_file first, then edit by start_line/end_line instead of matching text."
		}
		return msg
	}
	if count > 1 {
		short := strings.ReplaceAll(truncate(oldString, 50), "\n", "\\n")
		return fmt.Sprintf("Error: Found %d occurrences of '%s...' in %s. Provide more surrounding context to make old_string unique.",
			count, short, path)
	}

	newContent := strings.Replace(content, oldString, matchNewlines(newString, content), 1)
	if newContent == content {
		return "No changes made: old_string and new_string are identical. To fix a typo, new_string must differ from old_string."
	}
	if err := writeText(filepathAbs, newContent, hadBOM); err != nil {
		return fmt.Sprintf("Error editing file: %v", err)
	}
	diff := len(newContent) - len(content)
	sign := "+"
	if diff < 0 {
		sign = ""
	}
	return fmt.Sprintf("Successfully edited %s (%s%d bytes, %d lines changed)",
		path, sign, diff, strings.Count(oldString, "\n")+1)
}

// note is wired by the registry to the agent's reporter; edit_file uses it for
// the "both addressing modes given" side note.
var note = func(format string, args ...any) {}

// SetNote wires the reporter used for edit_file side notes.
func SetNote(fn func(format string, args ...any)) {
	if fn != nil {
		note = fn
	}
}
