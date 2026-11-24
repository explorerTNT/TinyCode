package tools

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// The project map is a pocket outline of a code base: its directories, files
// and top-level names, small enough to sit in a system prompt. A small model
// then starts every task knowing where things live instead of spending its
// first rounds on exploration.

const (
	maxMapFiles    = 600
	maxMapFileSize = 256 << 10
	// maxSymbolsPerFile keeps a file's line short; the layout matters more
	// than a complete index.
	maxSymbolsPerFile = 6
	maxRootFiles      = 40
	mapScanTTL        = time.Minute
	// mapTrailerReserve leaves room for the "... more files" note.
	mapTrailerReserve = 70
	// minExportedNames: below this, unexported names are shown as well, so
	// a file of internal helpers is not left nameless.
	minExportedNames = 3
)

// casedLanguages mark visibility by case: an upper-case name is the
// public surface of the file, a lower-case one is a helper.
var casedLanguages = map[string]bool{".go": true, ".cs": true, ".java": true, ".kt": true}

// reOutline extracts the top-level names of a source file per language. The
// first non-empty group is the name.
var reOutline = map[string]*regexp.Regexp{
	".go":   regexp.MustCompile(`^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)|^type\s+([A-Za-z_]\w*)|^(?:var|const)\s+([A-Za-z_]\w*)`),
	".py":   regexp.MustCompile(`^(?:async\s+)?(?:def|class)\s+(\w+)`),
	".js":   regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)|^\s*(?:export\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)|^\s*export\s+(?:const|let|var)\s+([A-Za-z_$][\w$]*)`),
	".rs":   regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:fn|struct|enum|trait|mod|type)\s+(\w+)`),
	".cs":   regexp.MustCompile(`^\s*(?:(?:public|internal|private|protected|static|abstract|sealed|partial)\s+)*(?:class|interface|enum|struct|record)\s+([A-Za-z_]\w*)|^\s*(?:public|internal)\s+(?:static\s+|async\s+|virtual\s+|override\s+)*[\w<>\[\],.?]+\s+([A-Za-z_]\w*)\s*\(`),
	".java": regexp.MustCompile(`^\s*(?:(?:public|final|abstract)\s+)*(?:class|interface|enum)\s+(\w+)|^\s*(?:public|protected)\s+(?:static\s+)?[\w<>\[\]]+\s+(\w+)\s*\(`),
	".kt":   regexp.MustCompile(`^\s*(?:fun|class|object|interface)\s+(\w+)`),
	".c":    regexp.MustCompile(`^\s*(?:struct|class|enum|union)\s+(\w+)`),
	".cpp":  regexp.MustCompile(`^\s*(?:struct|class|enum|union)\s+(\w+)`),
	".h":    regexp.MustCompile(`^\s*(?:struct|class|enum|union)\s+(\w+)`),
	".rb":   regexp.MustCompile(`^\s*(?:class|module)\s+(\w+)|^\s*def\s+(?:self\.)?(\w+)`),
	".php":  regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|abstract|final)\s+)*(?:class|interface|trait|function)\s+(\w+)`),
	".ps1":  regexp.MustCompile(`^\s*function\s+([\w-]+)`),
	".sh":   regexp.MustCompile(`^\s*(?:function\s+)?([A-Za-z_]\w*)\s*\(\)\s*\{`),
}

func init() {
	for _, ext := range []string{".mjs", ".cjs", ".jsx", ".ts", ".tsx", ".mts", ".cts"} {
		reOutline[ext] = reOutline[".js"]
	}
}

type mapFile struct {
	rel     string
	ext     string
	symbols []string
}

// repoScan is what one walk over the workspace found.
type repoScan struct {
	files     []mapFile
	rootFiles map[string]bool
	// symbols maps a lower-cased identifier to its real spelling and the
	// files that define it.
	symbols map[string]symbol
}

type symbol struct {
	name  string
	files []string
}

// scanCache remembers the last scan for a minute, so neither a new request
// nor a misspelt search walks the whole project again.
var scanCache struct {
	sync.Mutex
	dir     string
	builtAt time.Time
	scan    repoScan
}

func scanRepo(workspace string) repoScan {
	scanCache.Lock()
	defer scanCache.Unlock()
	if scanCache.dir == workspace && time.Since(scanCache.builtAt) < mapScanTTL {
		return scanCache.scan
	}
	scan := repoScan{rootFiles: map[string]bool{}, symbols: map[string]symbol{}}
	if base := resolveUnder(workspace, "."); base != "" {
		_ = filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				if p != base && (EXCLUDE_DIRS[info.Name()] || strings.HasPrefix(info.Name(), ".")) {
					return filepath.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(base, p)
			rel = filepath.ToSlash(rel)
			if !strings.Contains(rel, "/") && !strings.HasPrefix(rel, ".") && !IsBinaryExt(info.Name()) {
				scan.rootFiles[rel] = true
			}
			if len(scan.files) >= maxMapFiles || IsBinaryExt(info.Name()) || isTestFile(rel) || info.Size() > maxMapFileSize {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(info.Name()))
			re, ok := reOutline[ext]
			if !ok {
				return nil
			}
			shown, all := outlineSymbols(p, re, casedLanguages[ext])
			scan.files = append(scan.files, mapFile{rel: rel, ext: ext, symbols: shown})
			for _, s := range all {
				key := strings.ToLower(s)
				sym := scan.symbols[key]
				sym.name = s
				sym.files = append(sym.files, rel)
				scan.symbols[key] = sym
			}
			return nil
		})
	}
	scanCache.dir, scanCache.builtAt, scanCache.scan = workspace, time.Now(), scan
	return scan
}

// RepoMapText renders the project map in at most maxChars characters, or
// "" when the project has no source files or maxChars is not positive.
func RepoMapText(workspace string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	scan := scanRepo(workspace)
	if len(scan.files) == 0 && len(scan.rootFiles) == 0 {
		return ""
	}
	return renderRepoMap(scan.files, scan.rootFiles, maxChars)
}

// isTestFile keeps test code out of the map: it doubles the outline and is
// rarely where an edit has to go.
func isTestFile(rel string) bool {
	name := filepath.Base(rel)
	for _, suffix := range []string{"_test.go", ".test.js", ".test.jsx", ".spec.ts", ".spec.tsx", ".test.tsx", "_test.py", "_test.rs", "Test.java", "Tests.cs"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return name == "conftest.py" || (strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".py"))
}

// outlineSymbols lists a file's public names first. In a language where
// case marks visibility, helpers and unexported package variables only
// show up when the file has almost nothing public to show. Every name is
// returned as the second result: spelling hints want the full index.
func outlineSymbols(path string, re *regexp.Regexp, cased bool) (shown, all []string) {
	data, err := os.ReadFile(path)
	if err != nil || bytes.IndexByte(data, 0) >= 0 {
		return nil, nil
	}
	var exported, helpers []string
	for _, line := range strings.Split(string(data), "\n") {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for _, g := range m[1:] {
			if g == "" || g == "init" || strings.HasPrefix(g, "_") {
				continue
			}
			if !containsString(all, g) {
				all = append(all, g)
			}
			if len(exported) < maxSymbolsPerFile && !containsString(exported, g) {
				switch {
				case !cased || unicode.IsUpper([]rune(g)[0]):
					exported = append(exported, g)
				case !strings.HasPrefix(m[0], "var") && !strings.HasPrefix(m[0], "const"):
					helpers = append(helpers, g)
				}
			}
			break
		}
	}
	if len(exported) < minExportedNames {
		room := minInt(len(helpers), maxSymbolsPerFile-len(exported))
		exported = append(exported, helpers[:room]...)
	}
	return exported, all
}

// renderRepoMap lays the directories out first, so a tight budget costs the
// name outline and never the layout.
func renderRepoMap(files []mapFile, rootFiles map[string]bool, maxChars int) string {
	counts := map[string]map[string]int{}
	var dirs []string
	for _, f := range files {
		parts := strings.SplitN(f.rel, "/", 3)
		dir := ""
		switch len(parts) {
		case 1:
			continue
		case 2:
			dir = parts[0]
		default:
			dir = parts[0] + "/" + parts[1]
		}
		if counts[dir] == nil {
			counts[dir] = map[string]int{}
			dirs = append(dirs, dir)
		}
		counts[dir][f.ext]++
	}
	sort.Strings(dirs)

	var b strings.Builder
	b.WriteString("directories (source files by extension):\n")
	for i, dir := range dirs {
		var parts []string
		for ext, n := range counts[dir] {
			parts = append(parts, fmt.Sprintf("%d %s", n, strings.TrimPrefix(ext, ".")))
		}
		sort.Strings(parts)
		line := fmt.Sprintf("%s/ (%s)\n", dir, strings.Join(parts, ", "))
		if b.Len()+len(line) > maxChars {
			fmt.Fprintf(&b, "... (%d more directories)\n", len(dirs)-i)
			return strings.TrimRight(b.String(), "\n")
		}
		b.WriteString(line)
	}

	var roots []string
	for name := range rootFiles {
		roots = append(roots, name)
	}
	sort.Strings(roots)
	if len(roots) > maxRootFiles {
		roots = append(roots[:maxRootFiles], "...")
	}
	fmt.Fprintf(&b, "root files: %s\n", strings.Join(roots, ", "))
	layout := b.String()
	if len(layout) > maxChars {
		return truncateRunes(layout, maxChars) + "\n... (map truncated, use list_files)"
	}

	b.WriteString("names (file: functions, types, classes defined there):\n")
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	listed, omitted := 0, 0
	for _, f := range files {
		if len(f.symbols) == 0 {
			continue
		}
		line := f.rel + ": " + strings.Join(f.symbols, ", ") + "\n"
		if b.Len()+len(line) > maxChars-mapTrailerReserve {
			omitted++
			continue
		}
		b.WriteString(line)
		listed++
	}
	if listed == 0 {
		return strings.TrimRight(layout, "\n")
	}
	text := strings.TrimRight(b.String(), "\n")
	if omitted > 0 {
		text += fmt.Sprintf("\n... (%d more files with names not shown, use search_files)", omitted)
	}
	return text
}

var reIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// SuggestIdentifier answers a search that found nothing with the closest
// known identifier, if one is close enough. Small models misspell names,
// and a hint costs one round trip less than a failed edit.
func SuggestIdentifier(workspace, name string) string {
	if !reIdentifier.MatchString(name) {
		return ""
	}
	symbols := scanRepo(workspace).symbols
	want := strings.ToLower(name)
	limit := 1
	if len(want) >= 6 {
		limit = 2
	}
	best, bestDist := "", limit+1
	for id := range symbols {
		if abs(len(id)-len(want)) > limit {
			continue
		}
		if d := levenshtein(want, id); d < bestDist || (d == bestDist && id < best) {
			best, bestDist = id, d
		}
	}
	if best == "" || bestDist == 0 || bestDist > limit {
		return ""
	}
	files := append([]string{}, symbols[best].files...)
	sort.Strings(files)
	if len(files) > 3 {
		files = files[:3]
	}
	return fmt.Sprintf(" Did you mean '%s' (%s)?", symbols[best].name, strings.Join(files, ", "))
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = minInt(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func minInt(vals ...int) int {
	out := vals[0]
	for _, v := range vals[1:] {
		if v < out {
			out = v
		}
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
