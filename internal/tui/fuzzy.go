package tui

import (
	"strings"
	"time"
)

// fuzzyScore performs a case-insensitive subsequence match of query against
// target. A higher score is better; ok is false when query is not a
// subsequence. An empty query matches everything with a neutral score.
func fuzzyScore(target, query string) (float64, bool) {
	t := []rune(strings.ToLower(target))
	q := []rune(strings.ToLower(query))
	if len(q) == 0 {
		return 1, true
	}
	var score float64
	run, prev := 0, -1
	ti := 0
	for _, qc := range q {
		found := -1
		for ti < len(t) {
			if t[ti] == qc {
				found = ti
				break
			}
			ti++
		}
		if found == -1 {
			return 0, false
		}
		if found == prev+1 {
			run++
		} else {
			run = 1
		}
		score += float64(run)
		score += 1.0 / (1.0 + float64(found))
		prev, ti = found, found+1
	}
	return score, true
}

// frecency tracks in-memory frequency/recency of file mentions. The score is
// frequency divided by age, mirroring opencode's frecency.tsx formula.
type frecency struct {
	m map[string]struct {
		freq int
		last time.Time
	}
}

func newFrecency() *frecency {
	return &frecency{m: map[string]struct {
		freq int
		last time.Time
	}{}}
}

func (f *frecency) score(path string) float64 {
	e, ok := f.m[path]
	if !ok {
		return 0
	}
	return float64(e.freq) / (1 + time.Since(e.last).Hours()/24)
}

func (f *frecency) touch(path string) {
	e := f.m[path]
	e.freq++
	e.last = time.Now()
	f.m[path] = e
}
