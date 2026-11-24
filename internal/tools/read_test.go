package tools

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeText(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
		enc  textEncoding
	}{
		{"utf8", []byte("привет"), "привет", encUTF8},
		{"utf8 with BOM", append(append([]byte{}, utf8BOM...), []byte("hi")...), "hi", encUTF8BOM},
		{"cp1251", cp1251(t, "привет"), "привет", encCP1251},
		{"utf16le with BOM", mustEncode(t, "привет", encUTF16LE), "привет", encUTF16LE},
		{"utf16be with BOM", mustEncode(t, "привет", encUTF16BE), "привет", encUTF16BE},
		{"utf16le without BOM", mustEncode(t, "привет мир\n", encUTF16LE)[2:], "привет мир\n", encUTF16LE},
	}
	for _, c := range cases {
		got, enc := decodeText(c.in)
		if got != c.want || enc != c.enc {
			t.Errorf("%s: decodeText = (%q, %d), want (%q, %d)", c.name, got, enc, c.want, c.enc)
		}
	}

	// Anything that is not UTF-16 is still read as Windows-1251.
	if _, enc := decodeText(cp1251(t, "текст без нулевых байтов")); enc != encCP1251 {
		t.Errorf("Cyrillic text without NUL bytes decoded as encoding %d", enc)
	}
}

func mustEncode(t *testing.T, s string, enc textEncoding) []byte {
	t.Helper()
	b, err := encodeText(s, enc)
	if err != nil {
		t.Fatalf("encodeText(%q): %v", s, err)
	}
	return b
}

func TestEncodeTextKeepsBOM(t *testing.T) {
	if got := mustEncode(t, "x", encUTF8BOM); !bytes.HasPrefix(got, utf8BOM) {
		t.Fatalf("UTF-8 BOM lost: %x", got)
	}
	if got := mustEncode(t, "x", encUTF8); bytes.HasPrefix(got, utf8BOM) {
		t.Fatalf("plain UTF-8 must not gain a BOM: %x", got)
	}
}

func TestEncodeTextRefusesUnrepresentableText(t *testing.T) {
	// The flag emoji does not exist in Windows-1251.
	if _, err := encodeText("ok \U0001F6A9", encCP1251); err == nil {
		t.Fatal("expected an error for a character outside Windows-1251")
	}
	if _, err := encodeText("ок", encCP1251); err != nil {
		t.Fatalf("representable text rejected: %v", err)
	}
}

func TestReadFileDecodesEncodings(t *testing.T) {
	ws := t.TempDir()
	writeBytesAt(t, filepath.Join(ws, "cp.py"), cp1251(t, "# комментарий\nx = 1\n"))
	writeBytesAt(t, filepath.Join(ws, "u16.txt"), mustEncode(t, "// заметка\n", encUTF16LE))
	writeBytesAt(t, filepath.Join(ws, "bom.txt"), append(append([]byte{}, utf8BOM...), []byte("first\n")...))

	if got := ReadFile(ws, "cp.py", 1, 10); !strings.Contains(got, "1: # комментарий") {
		t.Errorf("Windows-1251 file not decoded:\n%s", got)
	}
	if got := ReadFile(ws, "u16.txt", 1, 10); !strings.Contains(got, "1: // заметка") {
		t.Errorf("UTF-16 file not decoded:\n%s", got)
	}
	if got := ReadFile(ws, "bom.txt", 1, 10); !strings.Contains(got, "1: first") {
		t.Errorf("UTF-8 BOM not stripped:\n%s", got)
	}
}

func TestReadFileStopsAtLineBoundary(t *testing.T) {
	ws := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 150; i++ {
		b.WriteString(strings.Repeat("x", 60) + "\n")
	}
	writeFileAt(t, filepath.Join(ws, "long.txt"), b.String())

	got := ReadFile(ws, "long.txt", 1, 2000)
	if len(got) > maxOutputChars+120 {
		t.Fatalf("read_file returned %d chars, budget is %d", len(got), maxOutputChars)
	}
	if !strings.Contains(got, "use offset=") || !strings.Contains(got, "of 150)") {
		t.Fatalf("truncated read must tell where to continue:\n...%s", got[len(got)-120:])
	}
	// Every shown line is complete: 60 x's, never a cut one.
	for _, line := range strings.Split(got, "\n")[1:] {
		if n := strings.Count(line, "x"); n != 0 && n != 60 {
			t.Fatalf("line cut in the middle (%d x): %q", n, line)
		}
	}
}

func TestReadFileRejectsBinary(t *testing.T) {
	ws := t.TempDir()
	writeBytesAt(t, filepath.Join(ws, "pic.png"), []byte("\x89PNG\x00\x00"))
	writeBytesAt(t, filepath.Join(ws, "blob.dat"), []byte("abc\x00def"))

	if got := ReadFile(ws, "pic.png", 1, 10); !strings.Contains(got, "is a binary file") {
		t.Errorf("png = %q", got)
	}
	if got := ReadFile(ws, "blob.dat", 1, 10); !strings.Contains(got, "appears to be binary") {
		t.Errorf("NUL bytes = %q", got)
	}
}

func TestReadFileErrors(t *testing.T) {
	ws := t.TempDir()
	writeFileAt(t, filepath.Join(ws, "agent.go"), "x")

	if got := ReadFile(ws, filepath.Join("..", "secret.txt"), 1, 10); !strings.Contains(got, "outside the project directory") {
		t.Errorf("path outside the workspace = %q", got)
	}
	if got := ReadFile(ws, "agent.g", 1, 10); !strings.Contains(got, "Did you mean") || !strings.Contains(got, "agent.go") {
		t.Errorf("missing file should suggest the closest name: %q", got)
	}
	if got := ReadFile(ws, "agent.go", 500, 10); !strings.Contains(got, "past end") {
		t.Errorf("offset past end = %q", got)
	}
}
