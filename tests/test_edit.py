from tools.edit import edit_file
from tools.read import read_file


def read_raw(p):
    return p.read_bytes()


class TestLineNumbers:
    def test_replace_single_line(self, workspace):
        p = workspace / "f.py"
        p.write_text("a\nb\nc\n", encoding="utf-8", newline="")
        result = edit_file(str(p), new_string="B", start_line=2)
        assert "Successfully" in result
        assert p.read_text(encoding="utf-8") == "a\nB\nc\n"

    def test_replace_range(self, workspace):
        p = workspace / "f.py"
        p.write_text("1\n2\n3\n4\n", encoding="utf-8", newline="")
        edit_file(str(p), new_string="X", start_line=2, end_line=3)
        assert p.read_text(encoding="utf-8") == "1\nX\n4\n"

    def test_delete_lines(self, workspace):
        p = workspace / "f.py"
        p.write_text("1\n2\n3\n", encoding="utf-8", newline="")
        edit_file(str(p), new_string="", start_line=2, end_line=2)
        assert p.read_text(encoding="utf-8") == "1\n3\n"

    def test_multiline_replacement(self, workspace):
        p = workspace / "f.py"
        p.write_text("a\nb\n", encoding="utf-8", newline="")
        edit_file(str(p), new_string="x\ny\nz", start_line=1, end_line=1)
        assert p.read_text(encoding="utf-8") == "x\ny\nz\nb\n"

    def test_out_of_range_start(self, workspace):
        p = workspace / "f.py"
        p.write_text("a\n", encoding="utf-8", newline="")
        assert "out of range" in edit_file(str(p), new_string="x", start_line=99)

    def test_end_before_start(self, workspace):
        p = workspace / "f.py"
        p.write_text("a\nb\nc\n", encoding="utf-8", newline="")
        assert "before start_line" in edit_file(
            str(p), new_string="x", start_line=3, end_line=2
        )

    def test_string_line_number_accepted(self, workspace):
        p = workspace / "f.py"
        p.write_text("a\nb\n", encoding="utf-8", newline="")
        assert "Successfully" in edit_file(str(p), new_string="B", start_line="2")

    def test_float_line_number_accepted(self, workspace):
        p = workspace / "f.py"
        p.write_text("a\nb\n", encoding="utf-8", newline="")
        assert "Successfully" in edit_file(str(p), new_string="B", start_line=2.0)

    def test_garbage_line_number_rejected(self, workspace):
        p = workspace / "f.py"
        p.write_text("a\nb\n", encoding="utf-8", newline="")
        assert "must be a line number" in edit_file(
            str(p), new_string="B", start_line="second"
        )


class TestCRLF:
    def test_crlf_preserved_on_line_edit(self, workspace):
        p = workspace / "f.py"
        p.write_bytes(b"a\r\nb\r\nc\r\n")
        edit_file(str(p), new_string="B", start_line=2)
        assert read_raw(p) == b"a\r\nB\r\nc\r\n"
        assert b"\r\r" not in read_raw(p)

    def test_crlf_preserved_on_old_string_edit(self, workspace):
        p = workspace / "f.py"
        p.write_bytes(b"x = 1\r\ny = 2\r\n")
        edit_file(str(p), old_string="y = 2", new_string="y = 3")
        assert read_raw(p) == b"x = 1\r\ny = 3\r\n"

    def test_multiline_old_string_matches_across_crlf(self, workspace):
        p = workspace / "f.py"
        p.write_bytes(b"def f():\r\n    return 1\r\n")
        result = edit_file(
            str(p), old_string="def f():\n    return 1", new_string="def f():\n    return 2"
        )
        assert "Successfully" in result
        assert read_raw(p) == b"def f():\r\n    return 2\r\n"

    def test_crlf_new_string_does_not_double(self, workspace):
        p = workspace / "f.py"
        p.write_bytes(b"a\r\nb\r\n")
        edit_file(str(p), new_string="X\r\nY", start_line=1, end_line=1)
        assert b"\r\r" not in read_raw(p)
        assert read_raw(p) == b"X\r\nY\r\nb\r\n"

    def test_lf_file_stays_lf(self, workspace):
        p = workspace / "f.py"
        p.write_bytes(b"a\nb\n")
        edit_file(str(p), new_string="B", start_line=2)
        assert read_raw(p) == b"a\nB\n"


class TestLineNumberAgreement:
    def test_read_and_edit_agree_on_mixed_endings(self, workspace):
        # The core bug: read_file opened in text mode (normalising) while
        # edit_file split on a single style, so the numbers shown were not the
        # numbers edited and the change landed on the wrong line.
        p = workspace / "f.txt"
        p.write_bytes(b"one\r\ntwo\nthree\rfour\n")
        listing = read_file(str(p))
        assert "3: three" in listing
        edit_file(str(p), new_string="THREE", start_line=3)
        assert b"THREE" in read_raw(p)
        assert b"two" in read_raw(p)
        assert b"four" in read_raw(p)

    def test_read_numbers_match_edit_target(self, workspace):
        p = workspace / "f.py"
        p.write_bytes(b"import os\r\n\r\ndef main():\r\n    pass\r\n")
        listing = read_file(str(p))
        assert "3: def main():" in listing
        edit_file(str(p), new_string="def main(argv):", start_line=3)
        assert b"def main(argv):" in read_raw(p)


class TestOldStringSafety:
    def test_ambiguous_duplicate_refused(self, workspace):
        p = workspace / "f.py"
        p.write_text("x = 1\nx = 1\n", encoding="utf-8", newline="")
        result = edit_file(str(p), old_string="x = 1", new_string="x = 2")
        assert "Error" in result and "2 occurrences" in result
        assert p.read_text(encoding="utf-8") == "x = 1\nx = 1\n"

    def test_near_identical_blocks_not_guessed(self, workspace):
        # Two blocks differing by one character: a fuzzy match would pick one
        # arbitrarily and silently corrupt the other's meaning.
        p = workspace / "f.py"
        p.write_text(
            "def a():\n    return compute(1)\n\n"
            "def b():\n    return compute(2)\n",
            encoding="utf-8", newline="",
        )
        before = p.read_text(encoding="utf-8")
        result = edit_file(
            str(p), old_string="    return compute(3)", new_string="    return compute(9)"
        )
        assert "Error" in result
        assert p.read_text(encoding="utf-8") == before

    def test_close_probe_replaces_only_the_lines_it_describes(self, workspace):
        body = "\n".join(f"    line{i} = {i}" for i in range(40))
        p = workspace / "f.py"
        p.write_text(f"def big():\n{body}\n", encoding="utf-8", newline="")
        # A 2-line probe may match, but only those 2 lines may change - the
        # other 39 lines of the function must survive untouched.
        result = edit_file(
            str(p), old_string="def big(x):\n    line0 = 0", new_string="def big():\n    pass"
        )
        assert "Successfully" in result
        after = p.read_text(encoding="utf-8")
        for i in range(1, 40):
            assert f"    line{i} = {i}" in after

    def test_declaration_anchor_does_not_eat_a_huge_block(self, workspace):
        body = "\n".join(f"    value_{i} = compute({i})" for i in range(40))
        p = workspace / "f.py"
        p.write_text(f"def big():\n{body}\n", encoding="utf-8", newline="")
        before = p.read_text(encoding="utf-8")
        # Similarity is far too low for a fuzzy match, so only the declaration
        # anchor could fire - and a 2-line probe must not authorise replacing
        # a 41-line function.
        result = edit_file(
            str(p),
            old_string="def big(alpha, beta, gamma):\n    totally unrelated text here",
            new_string="def big():\n    pass",
        )
        assert "Error" in result
        assert p.read_text(encoding="utf-8") == before

    def test_indentation_slip_is_forgiven(self, workspace):
        p = workspace / "f.py"
        p.write_text("def f():\n        return 1\n", encoding="utf-8", newline="")
        result = edit_file(
            str(p), old_string="def f():\n    return 1", new_string="def f():\n    return 2"
        )
        assert "Successfully" in result
        assert "return 2" in p.read_text(encoding="utf-8")

    def test_identical_strings_reported_clearly(self, workspace):
        p = workspace / "f.py"
        p.write_text("a = 1\n", encoding="utf-8", newline="")
        result = edit_file(str(p), old_string="a = 1", new_string="a = 1")
        assert "identical" in result

    def test_missing_text_suggests_line_numbers(self, workspace):
        p = workspace / "f.py"
        p.write_text("alpha = 1\nbeta = 2\n", encoding="utf-8", newline="")
        result = edit_file(str(p), old_string="gamma = 3", new_string="x")
        assert "Error" in result
        assert "start_line" in result

    def test_no_addressing_mode_is_an_error(self, workspace):
        p = workspace / "f.py"
        p.write_text("a\n", encoding="utf-8", newline="")
        assert "Error" in edit_file(str(p), new_string="x")


class TestEditErrors:
    def test_missing_file(self, workspace):
        assert "not found" in edit_file(str(workspace / "nope.py"), new_string="x", start_line=1).lower()

    def test_directory_rejected(self, workspace):
        d = workspace / "sub"
        d.mkdir()
        assert "Not a file" in edit_file(str(d), new_string="x", start_line=1)

    def test_empty_file(self, workspace):
        p = workspace / "f.py"
        p.write_text("", encoding="utf-8", newline="")
        assert "Error" in edit_file(str(p), new_string="x", start_line=1)


class TestEncodingPreserved:
    def test_cp1251_file_stays_readable(self, workspace):
        p = workspace / "f.txt"
        p.write_bytes("привет\nмир\n".encode("cp1251"))
        edit_file(str(p), new_string="мир!", start_line=2)
        assert p.read_bytes().decode("cp1251") == "привет\nмир!\n"
