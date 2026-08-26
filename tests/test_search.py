import pytest

from tools.glob import list_files, suggest_files
from tools.grep import _fallback_grep, _glob_for, search_files


class TestGlobFor:
    @pytest.mark.parametrize("value,expected", [
        ("*.py", "*.py"),
        ("py", "*.py"),
        (".py", "*.py"),
        ("test_*.py", "test_*.py"),
        ("Makefile", "Makefile"),
        ("", ""),
        (None, ""),
    ])
    def test_normalisation(self, value, expected):
        assert _glob_for(value) == expected

    def test_prefixed_glob_survives(self):
        # str.lstrip('*.') strips a character set, not a prefix, so patterns
        # beginning with '*' or '.' used to be mangled into nonsense.
        assert _glob_for("*.tar.gz") == "*.tar.gz"


class TestSearchFiles:
    def test_finds_match(self, workspace):
        (workspace / "a.py").write_text("def hello():\n    pass\n", encoding="utf-8")
        out = search_files("hello", str(workspace))
        assert "a.py" in out

    def test_no_match_message(self, workspace):
        (workspace / "a.py").write_text("x = 1\n", encoding="utf-8")
        assert "No matches" in search_files("zzzz", str(workspace))

    def test_invalid_regex_reported(self, workspace):
        assert "Invalid regex" in search_files("(unclosed", str(workspace))

    def test_single_file_target(self, workspace):
        p = workspace / "a.py"
        p.write_text("needle here\n", encoding="utf-8")
        assert "needle" in search_files("needle", str(p))

    def test_missing_path(self, workspace):
        assert "Path not found" in search_files("x", str(workspace / "nope"))

    def test_empty_pattern(self, workspace):
        assert "Error" in search_files("", str(workspace))

    def test_include_filter(self, workspace):
        (workspace / "a.py").write_text("target\n", encoding="utf-8")
        (workspace / "b.txt").write_text("target\n", encoding="utf-8")
        out = search_files("target", str(workspace), include="*.py")
        assert "a.py" in out
        assert "b.txt" not in out


class TestFallbackGrep:
    def test_finds_match(self, workspace):
        (workspace / "a.py").write_text("alpha\n", encoding="utf-8")
        assert "a.py" in _fallback_grep("alpha", workspace)

    def test_case_insensitive(self, workspace):
        (workspace / "a.py").write_text("Alpha\n", encoding="utf-8")
        assert "a.py" in _fallback_grep("alpha", workspace)

    def test_include_prefixed_pattern(self, workspace):
        (workspace / "test_a.py").write_text("target\n", encoding="utf-8")
        (workspace / "main.py").write_text("target\n", encoding="utf-8")
        out = _fallback_grep("target", workspace, include="test_*.py")
        assert "test_a.py" in out
        assert "main.py" not in out

    def test_skips_excluded_dirs(self, workspace):
        vendor = workspace / "node_modules"
        vendor.mkdir()
        (vendor / "x.js").write_text("target\n", encoding="utf-8")
        assert "No matches" in _fallback_grep("target", workspace)

    def test_skips_binary_extensions(self, workspace):
        (workspace / "a.pyc").write_text("target\n", encoding="utf-8")
        assert "No matches" in _fallback_grep("target", workspace)

    def test_per_file_cap(self, workspace):
        (workspace / "a.py").write_text("hit\n" * 100, encoding="utf-8")
        out = _fallback_grep("hit", workspace)
        assert out.count("a.py:") <= 5

    def test_header_does_not_overstate(self, workspace):
        (workspace / "a.py").write_text("hit\n" * 3, encoding="utf-8")
        out = _fallback_grep("hit", workspace)
        assert "showing 3 match(es)" in out


class TestListFiles:
    def test_lists_files(self, workspace):
        (workspace / "a.py").write_text("x", encoding="utf-8")
        (workspace / "b.py").write_text("x", encoding="utf-8")
        out = list_files("*.py", str(workspace))
        assert "a.py" in out and "b.py" in out

    def test_no_match(self, workspace):
        assert "No files matching" in list_files("*.rs", str(workspace))

    def test_excludes_build_dirs(self, workspace):
        d = workspace / "__pycache__"
        d.mkdir()
        (d / "x.py").write_text("x", encoding="utf-8")
        assert "No files matching" in list_files("*.py", str(workspace))

    def test_path_scoped_pattern(self, workspace):
        src = workspace / "src"
        src.mkdir()
        (src / "main.py").write_text("x", encoding="utf-8")
        (workspace / "root.py").write_text("x", encoding="utf-8")
        out = list_files("src/*.py", str(workspace))
        assert "main.py" in out
        assert "root.py" not in out

    def test_missing_dir(self, workspace):
        assert "Path not found" in list_files("*", str(workspace / "nope"))

    def test_file_instead_of_dir(self, workspace):
        p = workspace / "a.py"
        p.write_text("x", encoding="utf-8")
        assert "Not a directory" in list_files("*", str(p))


class TestSuggestFiles:
    def test_exact_name_found(self, workspace):
        (workspace / "calc.py").write_text("x", encoding="utf-8")
        assert "calc.py" in suggest_files("calc.py", workspace)

    def test_finds_from_mangled_absolute_path(self, workspace):
        (workspace / "calc.py").write_text("x", encoding="utf-8")
        assert "calc.py" in suggest_files("C:/bogus dir/calc.py", workspace)

    def test_nested_file_found(self, workspace):
        src = workspace / "src"
        src.mkdir()
        (src / "app.py").write_text("x", encoding="utf-8")
        results = suggest_files("app.py", workspace)
        assert any("app.py" in r for r in results)

    def test_dot_target_ignored(self, workspace):
        assert suggest_files(".", workspace) == []

    def test_empty_target_ignored(self, workspace):
        assert suggest_files("", workspace) == []

    def test_missing_dir_is_safe(self, workspace):
        assert suggest_files("x.py", workspace / "nope") == []
