package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveMentionsFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "hello world\n")

	got := resolveMentions("see @a.txt please", dir)
	want := "<" + filepath.Join(dir, "a.txt") + ">\nhello world"
	if !strings.Contains(got, "see ") || !strings.Contains(got, "hello world") || !strings.Contains(got, "a.txt") {
		t.Fatalf("unexpected result:\n%s", got)
	}
	_ = want
}

func TestResolveMentionsLineRange(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "one\ntwo\nthree\nfour\n")

	got := resolveMentions("@a.txt#2-3", dir)
	if !strings.Contains(got, "2: two") || !strings.Contains(got, "3: three") {
		t.Fatalf("expected lines 2-3, got:\n%s", got)
	}
	if strings.Contains(got, "1: one") || strings.Contains(got, "4: four") {
		t.Fatalf("unexpected lines in range result:\n%s", got)
	}
}

func TestResolveMentionsDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sub/b.txt", "bb\n")
	writeFile(t, dir, "sub/c.txt", "cc\n")

	got := resolveMentions("@sub", dir)
	if !strings.Contains(got, "sub/") || !strings.Contains(got, "b.txt") || !strings.Contains(got, "c.txt") {
		t.Fatalf("directory listing missing entries:\n%s", got)
	}
}

func TestResolveMentionsTruncation(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", mentionMaxBytes+100)
	writeFile(t, dir, "big.txt", big)

	got := resolveMentions("@big.txt", dir)
	if strings.Contains(got, "[file truncated") == false {
		t.Fatalf("expected truncation note, got:\n%s", got)
	}
	if len(got) > mentionMaxBytes+1024 {
		t.Fatalf("result unexpectedly large: %d bytes", len(got))
	}
}

func TestResolveMentionsUnknownLeftAlone(t *testing.T) {
	dir := t.TempDir()
	got := resolveMentions("@missing.txt", dir)
	if got != "@missing.txt" {
		t.Fatalf("unknown mention should be left alone, got %q", got)
	}
}

func TestResolveMentionsSkipsSlashAndBang(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "x\n")
	// slash and bang inputs are handled elsewhere; here just confirm the
	// resolver itself doesn't corrupt them (it only touches @ tokens).
	if got := resolveMentions("! ls @a.txt", dir); !strings.Contains(got, "! ls") {
		t.Fatalf("bang command altered: %q", got)
	}
}

func TestResolveMentionsOutsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	got := resolveMentions("@../secret.txt", dir)
	if got != "@../secret.txt" {
		t.Fatalf("out-of-workspace mention should be left alone, got %q", got)
	}
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
