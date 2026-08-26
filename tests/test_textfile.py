from tools.textfile import (
    apply_newlines,
    dominant_newline,
    join_lines,
    normalize,
    read_text,
    split_lines,
    write_text,
)


class TestSplitLines:
    def test_lf(self):
        assert split_lines("a\nb\nc") == (["a", "b", "c"], False)

    def test_lf_trailing(self):
        assert split_lines("a\nb\n") == (["a", "b"], True)

    def test_crlf_counts_same_as_lf(self):
        lf, _ = split_lines("a\nb\nc")
        crlf, _ = split_lines("a\r\nb\r\nc")
        assert lf == crlf

    def test_lone_cr_is_a_line_break(self):
        # An old-Mac or corrupted file must not be seen as one giant line,
        # otherwise read_file and edit_file disagree on every line number.
        assert split_lines("a\rb\rc")[0] == ["a", "b", "c"]

    def test_mixed_endings(self):
        assert split_lines("a\r\nb\nc\rd")[0] == ["a", "b", "c", "d"]

    def test_empty(self):
        assert split_lines("") == ([], False)

    def test_only_newline(self):
        assert split_lines("\n") == ([""], True)


class TestDominantNewline:
    def test_pure_lf(self):
        assert dominant_newline("a\nb\nc") == "\n"

    def test_pure_crlf(self):
        assert dominant_newline("a\r\nb\r\nc") == "\r\n"

    def test_single_stray_crlf_does_not_flip_an_lf_file(self):
        content = "\n".join(f"line{i}" for i in range(50)) + "\r\n"
        assert dominant_newline(content) == "\n"

    def test_mostly_crlf_wins(self):
        assert dominant_newline("a\r\nb\r\nc\nd") == "\r\n"

    def test_no_newline_defaults_to_lf(self):
        assert dominant_newline("single line") == "\n"


class TestRoundTrip:
    def test_join_inverts_split(self):
        for text in ("a\nb\n", "a\r\nb\r\n", "a\nb", "x"):
            lines, trailing = split_lines(text)
            rebuilt = join_lines(lines, dominant_newline(text), trailing)
            assert normalize(rebuilt) == normalize(text)

    def test_apply_newlines_has_no_double_cr(self):
        out = apply_newlines("a\r\nb\nc\r", "\r\n")
        assert "\r\r" not in out
        assert out == "a\r\nb\r\nc\r\n"


class TestReadWrite:
    def test_preserves_crlf(self, tmp_path):
        p = tmp_path / "f.txt"
        p.write_bytes(b"a\r\nb\r\n")
        content, encoding = read_text(p)
        assert content == "a\r\nb\r\n"
        write_text(p, content, encoding)
        assert p.read_bytes() == b"a\r\nb\r\n"

    def test_utf8_bom_round_trips(self, tmp_path):
        p = tmp_path / "f.txt"
        p.write_bytes(b"\xef\xbb\xbfhello")
        content, encoding = read_text(p)
        assert "hello" in content
        write_text(p, content, encoding)
        assert b"hello" in p.read_bytes()

    def test_cp1251_fallback(self, tmp_path):
        p = tmp_path / "f.txt"
        p.write_bytes("привет".encode("cp1251"))
        content, encoding = read_text(p)
        assert content == "привет"
        assert encoding == "cp1251"
