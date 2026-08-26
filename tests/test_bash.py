import sys

import pytest

from tools.bash import (
    _check_linux_hints,
    _sanitize_cmd,
    _squeeze_spaces_outside_quotes,
    _sub_outside_quotes,
    run_bash,
)


class TestSanitize:
    def test_python3_rewritten(self):
        assert _sanitize_cmd("python3 main.py") == "python main.py"

    def test_versioned_python_rewritten(self):
        assert _sanitize_cmd("python3.11 main.py") == "python main.py"

    def test_pip3_rewritten(self):
        assert _sanitize_cmd("pip3 install x") == "pip install x"

    def test_dev_null_stripped(self):
        assert "/dev/null" not in _sanitize_cmd("cmd 2> /dev/null")

    def test_noop_cd_stripped(self):
        assert _sanitize_cmd("cd . && python x.py") == "python x.py"

    def test_real_cd_preserved(self):
        # Stripping every leading `cd X &&` silently ran the command in the
        # wrong directory.
        assert _sanitize_cmd("cd build && cmake ..").startswith("cd build")

    def test_quoted_python3_untouched(self):
        # Rewriting inside quotes corrupts the user's own data.
        out = _sanitize_cmd('git commit -m "drop python3 support"')
        assert "python3 support" in out

    def test_quoted_spacing_preserved(self):
        out = _sanitize_cmd('git commit -m "fix:  spacing"')
        assert "fix:  spacing" in out


class TestSubOutsideQuotes:
    def test_replaces_outside(self):
        assert _sub_outside_quotes(r"\bfoo\b", "bar", "foo x") == "bar x"

    def test_skips_inside_double_quotes(self):
        assert _sub_outside_quotes(r"\bfoo\b", "bar", '"foo" x') == '"foo" x'

    def test_skips_inside_single_quotes(self):
        assert _sub_outside_quotes(r"\bfoo\b", "bar", "'foo' x") == "'foo' x"

    def test_mixed(self):
        assert _sub_outside_quotes(r"\bfoo\b", "bar", 'foo "foo"') == 'bar "foo"'


class TestSqueezeSpaces:
    def test_collapses_outside_quotes(self):
        assert _squeeze_spaces_outside_quotes("a    b") == "a b"

    def test_preserves_inside_quotes(self):
        assert _squeeze_spaces_outside_quotes('a "b    c"') == 'a "b    c"'


class TestLinuxHints:
    @pytest.mark.parametrize("cmd", ["which python", "uname -a", "rm file", "grep x y"])
    def test_linux_command_detected(self, cmd):
        assert _check_linux_hints(cmd) is not None

    @pytest.mark.parametrize("cmd", [
        "python app.py --find x",
        "git grep foo",
        "Get-ChildItem",
    ])
    def test_legitimate_command_allowed(self, cmd):
        assert _check_linux_hints(cmd) is None

    def test_bin_path_detected(self):
        assert _check_linux_hints("/bin/bash -c ls") is not None


class TestRunBash:
    def test_empty_command(self):
        assert "Error" in run_bash("")

    def test_bad_cwd(self, workspace):
        assert "Error" in run_bash("echo hi", cwd=str(workspace / "nope"))

    @pytest.mark.skipif(sys.platform != "win32", reason="PowerShell only")
    def test_runs_in_given_cwd(self, workspace):
        (workspace / "marker.txt").write_text("x", encoding="utf-8")
        out = run_bash("Get-ChildItem -Name", cwd=str(workspace))
        assert "marker.txt" in out

    @pytest.mark.skipif(sys.platform != "win32", reason="PowerShell only")
    def test_cwd_isolated_between_calls(self, workspace, tmp_path):
        other = tmp_path / "other"
        other.mkdir()
        (other / "other.txt").write_text("x", encoding="utf-8")
        (workspace / "ws.txt").write_text("x", encoding="utf-8")
        assert "other.txt" in run_bash("Get-ChildItem -Name", cwd=str(other))
        # The process working directory must not have been mutated.
        assert "ws.txt" in run_bash("Get-ChildItem -Name", cwd=str(workspace))

    @pytest.mark.skipif(sys.platform != "win32", reason="PowerShell only")
    def test_exit_code_reported(self, workspace):
        out = run_bash("exit 3", cwd=str(workspace))
        assert "Exit code: 3" in out

    @pytest.mark.skipif(sys.platform != "win32", reason="PowerShell only")
    def test_timeout(self, workspace):
        out = run_bash("Start-Sleep -Seconds 10", timeout=1, cwd=str(workspace))
        assert "timed out" in out

    @pytest.mark.skipif(sys.platform != "win32", reason="PowerShell only")
    def test_stdin_closed_does_not_hang(self, workspace):
        # Without stdin=DEVNULL a command that reads input blocks until the
        # timeout with no clue why.
        out = run_bash("$x = Read-Host; Write-Output done", timeout=8, cwd=str(workspace))
        assert "timed out" not in out
