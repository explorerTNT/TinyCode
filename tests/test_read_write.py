from tools.read import read_file
from tools.write import write_file


class TestRead:
    def test_basic(self, workspace):
        p = workspace / "f.txt"
        p.write_text("alpha\nbeta\n", encoding="utf-8", newline="")
        out = read_file(str(p))
        assert "1: alpha" in out
        assert "2: beta" in out

    def test_offset_and_limit(self, workspace):
        p = workspace / "f.txt"
        p.write_text("\n".join(str(i) for i in range(1, 21)) + "\n", encoding="utf-8", newline="")
        out = read_file(str(p), offset=5, limit=3)
        assert "5: 5" in out and "7: 7" in out
        assert "8: 8" not in out

    def test_offset_past_end(self, workspace):
        p = workspace / "f.txt"
        p.write_text("a\n", encoding="utf-8", newline="")
        assert "past end" in read_file(str(p), offset=50)

    def test_string_offset_accepted(self, workspace):
        p = workspace / "f.txt"
        p.write_text("a\nb\nc\n", encoding="utf-8", newline="")
        assert "2: b" in read_file(str(p), offset="2")

    def test_crlf_numbering(self, workspace):
        p = workspace / "f.txt"
        p.write_bytes(b"a\r\nb\r\nc\r\n")
        out = read_file(str(p))
        assert "2: b" in out
        assert "\r" not in out.split("2: b")[1][:2]

    def test_missing_file_suggests(self, workspace):
        (workspace / "calc.py").write_text("x = 1\n", encoding="utf-8")
        out = read_file(str(workspace / "calc.py".upper().replace("CALC", "calcc")))
        assert "Error" in out

    def test_binary_extension_refused(self, workspace):
        p = workspace / "img.png"
        p.write_bytes(b"\x89PNG\r\n")
        assert "binary" in read_file(str(p))

    def test_nul_byte_detected(self, workspace):
        p = workspace / "data.txt"
        p.write_bytes(b"text\x00more")
        assert "binary" in read_file(str(p))

    def test_directory_rejected(self, workspace):
        d = workspace / "sub"
        d.mkdir()
        assert "Not a file" in read_file(str(d))

    def test_long_line_truncated(self, workspace):
        p = workspace / "f.txt"
        p.write_text("x" * 5000 + "\n", encoding="utf-8", newline="")
        assert "line truncated" in read_file(str(p))

    def test_too_large_refused(self, workspace):
        p = workspace / "big.txt"
        p.write_text("a" * 500_000, encoding="utf-8", newline="")
        assert "too large" in read_file(str(p))


class TestWrite:
    def test_creates_file(self, workspace):
        p = workspace / "new.txt"
        assert "Successfully" in write_file(str(p), "hello")
        assert p.read_text(encoding="utf-8") == "hello"

    def test_creates_parent_dirs(self, workspace):
        p = workspace / "a" / "b" / "c.txt"
        write_file(str(p), "x")
        assert p.exists()

    def test_invalid_python_refused(self, workspace):
        p = workspace / "broken.py"
        result = write_file(str(p), "def f(:\n")
        assert "NOT written" in result
        assert not p.exists()

    def test_valid_python_written(self, workspace):
        p = workspace / "ok.py"
        assert "Successfully" in write_file(str(p), "def f():\n    return 1\n")
        assert p.exists()

    def test_non_python_not_syntax_checked(self, workspace):
        p = workspace / "notes.txt"
        assert "Successfully" in write_file(str(p), "def f(:")

    def test_new_file_uses_lf(self, workspace):
        p = workspace / "n.txt"
        write_file(str(p), "a\nb\n")
        assert p.read_bytes() == b"a\nb\n"

    def test_existing_crlf_file_keeps_crlf(self, workspace):
        # Overwriting a CRLF file with LF-only model output would otherwise
        # rewrite every line in the diff.
        p = workspace / "win.txt"
        p.write_bytes(b"old\r\ncontent\r\n")
        write_file(str(p), "new\nlines\n")
        assert p.read_bytes() == b"new\r\nlines\r\n"

    def test_no_double_cr(self, workspace):
        p = workspace / "win.txt"
        p.write_bytes(b"a\r\n")
        write_file(str(p), "x\r\ny\r\n")
        assert b"\r\r" not in p.read_bytes()

    def test_directory_rejected(self, workspace):
        d = workspace / "sub"
        d.mkdir()
        assert "Error" in write_file(str(d), "x")

    def test_none_content_treated_as_empty(self, workspace):
        p = workspace / "e.txt"
        assert "Successfully" in write_file(str(p), None)
        assert p.read_text(encoding="utf-8") == ""


class TestReadWriteRoundTrip:
    def test_write_then_read_numbers_align(self, workspace):
        p = workspace / "f.py"
        write_file(str(p), "import os\n\ndef main():\n    pass\n")
        out = read_file(str(p))
        assert "1: import os" in out
        assert "3: def main():" in out
