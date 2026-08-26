import builtins

import pytest

from permissions import PermissionManager


class TestModes:
    def test_auto_allows_everything(self):
        pm = PermissionManager(mode="auto")
        assert pm.check_bash("Remove-Item x")
        assert pm.check_write("f.txt")

    def test_deny_blocks_everything(self):
        pm = PermissionManager(mode="deny")
        assert not pm.check_bash("echo hi")
        assert not pm.check_write("f.txt")

    def test_deny_blocks_readonly_too(self):
        assert not PermissionManager(mode="deny").check_bash("git status")


class TestReadonlyAutoApproval:
    @pytest.mark.parametrize("cmd", [
        "git status", "git diff", "git log", "pwd", "whoami",
        "python --version", "pip list",
    ])
    def test_readonly_approved_without_prompt(self, cmd, monkeypatch):
        monkeypatch.setattr(builtins, "input", lambda *a: pytest.fail("prompted"))
        assert PermissionManager(mode="ask").check_bash(cmd)

    @pytest.mark.parametrize("cmd", [
        "git status > out.txt",
        "git status | Remove-Item",
        "git status; rm -rf .",
        "git status && del x",
        "echo `whoami`",
        "echo $(whoami)",
    ])
    def test_shell_metacharacters_block_auto_approval(self, cmd, monkeypatch):
        # A redirect or a chained command turns a "readonly" head into an
        # arbitrary write.
        monkeypatch.setattr(builtins, "input", lambda *a: "n")
        assert not PermissionManager(mode="ask").check_bash(cmd)

    def test_write_command_prompts(self, monkeypatch):
        monkeypatch.setattr(builtins, "input", lambda *a: "n")
        assert not PermissionManager(mode="ask").check_bash("Remove-Item x")


class TestPrompting:
    def test_yes_approves(self, monkeypatch):
        monkeypatch.setattr(builtins, "input", lambda *a: "y")
        assert PermissionManager(mode="ask").check_write("f.txt")

    def test_no_rejects(self, monkeypatch):
        monkeypatch.setattr(builtins, "input", lambda *a: "n")
        assert not PermissionManager(mode="ask").check_write("f.txt")

    def test_always_switches_to_auto(self, monkeypatch):
        monkeypatch.setattr(builtins, "input", lambda *a: "a")
        pm = PermissionManager(mode="ask")
        assert pm.check_write("f.txt")
        assert pm.mode == "auto"

    def test_eof_rejects(self, monkeypatch):
        def raise_eof(*a):
            raise EOFError

        monkeypatch.setattr(builtins, "input", raise_eof)
        assert not PermissionManager(mode="ask").check_write("f.txt")

    def test_keyboard_interrupt_rejects(self, monkeypatch):
        def raise_int(*a):
            raise KeyboardInterrupt

        monkeypatch.setattr(builtins, "input", raise_int)
        assert not PermissionManager(mode="ask").check_bash("Remove-Item x")
