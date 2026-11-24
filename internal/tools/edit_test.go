package tools

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Editing one line of a legacy file must not re-encode the whole file.
func TestEditFileKeepsEncoding(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "cfg.py")
	writeBytesAt(t, path, cp1251(t, "# комментарий\nx = 1\n"))

	if got := EditFile(ws, "cfg.py", "", "x = 2\n", float64(2), float64(2)); !strings.HasPrefix(got, "Successfully edited") {
		t.Fatalf("edit failed: %s", got)
	}
	got := readBytes(t, path)
	if want := cp1251(t, "# комментарий\nx = 2\n"); !bytes.Equal(got, want) {
		t.Fatalf("file encoding changed:\n got %x\nwant %x", got, want)
	}
}

func TestEditFileKeepsBOMAndCRLF(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	writeBytesAt(t, path, append(append([]byte{}, utf8BOM...), []byte("one\r\ntwo\r\n")...))

	EditFile(ws, "a.txt", "", "TWO\r\n", float64(2), float64(2))
	got := readBytes(t, path)
	if !bytes.HasPrefix(got, utf8BOM) || string(got[len(utf8BOM):]) != "one\r\nTWO\r\n" {
		t.Fatalf("BOM or CRLF lost: %q", got)
	}
}

func TestEditFileKeepsUTF16(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "u16.txt")
	writeBytesAt(t, path, mustEncode(t, "a\nb\n", encUTF16LE))

	EditFile(ws, "u16.txt", "", "B\n", float64(2), float64(2))
	if got, want := readBytes(t, path), mustEncode(t, "a\nB\n", encUTF16LE); !bytes.Equal(got, want) {
		t.Fatalf("UTF-16 not preserved:\n got %x\nwant %x", got, want)
	}
}

// Text the file's encoding cannot hold is refused: converting the whole file
// or silently dropping the character would both corrupt the user's project.
func TestEditFileRefusesUnencodableText(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "cfg.py")
	writeBytesAt(t, path, cp1251(t, "х = 1\n"))

	got := EditFile(ws, "cfg.py", "", "х = '\U0001F6A9'\n", float64(1), float64(1))
	if !strings.HasPrefix(got, "Error editing file: the file is Windows-1251") || !strings.Contains(got, "Use characters") {
		t.Fatalf("unencodable text must be refused with a hint: %s", got)
	}
	if got, want := readBytes(t, path), cp1251(t, "х = 1\n"); !bytes.Equal(got, want) {
		t.Fatalf("a refused edit must leave the file alone:\n got %x\nwant %x", got, want)
	}
	if got := WriteFile(ws, "cfg.py", "\U0001F6A9"); !strings.HasPrefix(got, "Error writing file: the file is Windows-1251") {
		t.Fatalf("write_file must refuse too: %s", got)
	}
}

func TestEditFileOldStringAcrossCRLF(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "crlf.go")
	writeFileAt(t, path, "func a() {\r\n\treturn 1\r\n}\r\n")

	got := EditFile(ws, "crlf.go", "func a() {\n\treturn 1\n", "func a() {\n\treturn 2\n", nil, nil)
	if !strings.HasPrefix(got, "Successfully edited") {
		t.Fatalf("edit failed: %s", got)
	}
	content := string(readBytes(t, path))
	if content != "func a() {\r\n\treturn 2\r\n}\r\n" {
		t.Fatalf("line endings mixed up: %q", content)
	}
}

func TestEditFileErrors(t *testing.T) {
	ws := t.TempDir()
	writeFileAt(t, filepath.Join(ws, "a.txt"), "1\n2\n3\n")

	if got := EditFile(ws, "a.txt", "", "x", float64(9), nil); !strings.Contains(got, "out of range") {
		t.Errorf("start past end = %q", got)
	}
	if got := EditFile(ws, "a.txt", "", "x", float64(3), float64(2)); !strings.Contains(got, "before start_line") {
		t.Errorf("end before start = %q", got)
	}
	if got := EditFile(ws, "a.txt", "", "x", nil, nil); !strings.Contains(got, "provide either start_line") {
		t.Errorf("no addressing = %q", got)
	}
	if got := EditFile(ws, filepath.Join("..", "a.txt"), "", "x", float64(1), nil); !strings.Contains(got, "outside the project directory") {
		t.Errorf("path outside the workspace = %q", got)
	}
	if got := EditFile(ws, "a.txt", "missing text", "x", nil, nil); !strings.HasPrefix(got, "Error: Could not find") {
		t.Errorf("missing old_string = %q", got)
	}
	if got := EditFile(ws, "a.txt", "", "9\n", "start", "start"); !strings.Contains(got, "must be a line number") {
		t.Errorf("text as line number = %q", got)
	}
}

func TestWriteFileKeepsEncodingAndGuardsDirectories(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "cfg.py")
	writeBytesAt(t, path, cp1251(t, "старое\n"))

	if got := WriteFile(ws, "cfg.py", "новое\n"); !strings.HasPrefix(got, "Successfully wrote") {
		t.Fatalf("write failed: %s", got)
	}
	if got, want := readBytes(t, path), cp1251(t, "новое\n"); !bytes.Equal(got, want) {
		t.Fatalf("encoding not kept:\n got %x\nwant %x", got, want)
	}

	if got := WriteFile(ws, "new/dir/file.txt", "x"); !strings.HasPrefix(got, "Successfully wrote") {
		t.Fatalf("nested write failed: %s", got)
	}
	if got := WriteFile(ws, "new", "x"); !strings.Contains(got, "is a directory") {
		t.Fatalf("directory write = %q", got)
	}
	if got := WriteFile(ws, filepath.Join("..", "x.txt"), "x"); !strings.Contains(got, "outside the project directory") {
		t.Fatalf("path outside the workspace = %q", got)
	}
}
