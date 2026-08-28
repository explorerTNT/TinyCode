package tui

import "testing"

func TestFuzzyScore(t *testing.T) {
	cases := []struct {
		target, query string
		ok            bool
	}{
		{"file.go", "f", true},
		{"file.go", "fg", true},
		{"file.go", "FILE", true},
		{"file.go", "flg", true},  // subsequence
		{"file.go", "go", true},   // not a subsequence-in-order? g then o: yes "file.go" has g,o
		{"file.go", "xyz", false}, // no match
		{"file.go", "og", false},  // wrong order
		{"folder/sub/file.go", "sf", true},
	}
	for _, c := range cases {
		if _, ok := fuzzyScore(c.target, c.query); ok != c.ok {
			t.Errorf("fuzzyScore(%q, %q) ok=%v, want %v", c.target, c.query, ok, c.ok)
		}
	}
}

func TestFuzzyScoreEmptyQuery(t *testing.T) {
	score, ok := fuzzyScore("anything", "")
	if !ok || score != 1 {
		t.Fatalf("empty query: (%v, %v), want (1, true)", score, ok)
	}
}

func TestFuzzyScorePrefersPrefixAndContiguous(t *testing.T) {
	prefix, _ := fuzzyScore("session.go", "se")
	scattered, _ := fuzzyScore("session.go", "so")
	if prefix <= scattered {
		t.Fatalf("expected prefix match to score higher: prefix=%v scattered=%v", prefix, scattered)
	}
}

func TestFrecency(t *testing.T) {
	f := newFrecency()
	if got := f.score("a"); got != 0 {
		t.Fatalf("untouched score = %v, want 0", got)
	}
	f.touch("a")
	s1 := f.score("a")
	if s1 <= 0 {
		t.Fatalf("touched score = %v, want > 0", s1)
	}
	f.touch("a")
	if s2 := f.score("a"); s2 <= s1 {
		t.Fatalf("second touch should raise score: %v -> %v", s1, s2)
	}
}
