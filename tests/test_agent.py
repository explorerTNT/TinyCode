import os
from pathlib import Path

import pytest

import agent as agent_mod
from agent import (
    ContextManager,
    _clear_old_tool_results,
    _count_plan_steps,
    _drop_orphan_tools,
    _looks_done,
    _parse_xml_toolcalls,
    _safe_json_loads,
    _short_args,
    _strip_xml_toolcalls,
    _summarize_traceback,
    _tokenize_cmd,
    _unsafe_to_run,
)


class TestLooksDone:
    @pytest.mark.parametrize("text", [
        "TASK COMPLETE",
        "**TASK COMPLETE**",
        "работа окончена. задача выполнена.",
        "The implementation is complete.",
    ])
    def test_positive(self, text):
        assert _looks_done(text)

    @pytest.mark.parametrize("text", [
        "",
        "still working on it",
        "задача не выполнена",
        "I could not finish; the task is complete only partially",
    ])
    def test_negative(self, text):
        assert not _looks_done(text)

    def test_negation_scoped_to_its_own_sentence(self):
        # An unrelated "ошибок нет" elsewhere in the paragraph used to
        # suppress a genuine completion, because the check scanned a
        # fixed-size window rather than the claiming sentence.
        assert _looks_done("Тестов нет. Всё готово.")

    def test_continuation_blocks_completion(self):
        assert not _looks_done("Всё готово. Теперь нужно добавить тесты.")

    def test_claim_with_following_work_rejected(self):
        assert not _looks_done("The task is complete. Next I will refactor.")


class TestUnsafeToRun:
    @pytest.mark.parametrize("src", [
        "import subprocess\nsubprocess.run(['x'])",
        "import shutil\nshutil.rmtree('/')",
        "import os\nos.system('dir')",
        "os.remove('x')",
        "eval('1+1')",
        "exec('x=1')",
        "__import__('os').unlink('x')",
        "open('f','w').write('x')",
        "import socket",
        "from shutil import rmtree",
        "getattr(os, 'remove')('x')",
        "x.__class__.__bases__",
    ])
    def test_dangerous_code_refused(self, src):
        assert _unsafe_to_run(src) is not None

    @pytest.mark.parametrize("src", [
        "print('hello')",
        "def add(a, b):\n    return a + b\nprint(add(1, 2))",
        "import math\nprint(math.pi)",
        "for i in range(3):\n    print(i)",
    ])
    def test_benign_code_allowed(self, src):
        assert _unsafe_to_run(src) is None

    def test_unparseable_is_unsafe(self):
        assert _unsafe_to_run("def f(:") is not None

    def test_obfuscated_import_caught(self):
        # The old substring scan was defeated by anything that did not spell
        # the module name literally in an import statement.
        assert _unsafe_to_run("m = __import__('subprocess')") is not None


class TestEnforceWorkspace:
    def test_relative_path_allowed(self, make_agent):
        a = make_agent()
        args = {"path": "calc.py"}
        assert a._enforce_workspace("read_file", args) is None

    def test_traversal_refused(self, make_agent):
        a = make_agent()
        assert a._enforce_workspace("write_file", {"path": "../evil.txt"}) is not None

    def test_absolute_outside_refused(self, make_agent, tmp_path):
        a = make_agent()
        result = a._enforce_workspace("write_file", {"path": str(tmp_path / "out.txt")})
        assert result is not None

    def test_mangled_path_repaired(self, make_agent, workspace, capsys):
        (workspace / "calc.py").write_text("x = 1\n", encoding="utf-8")
        a = make_agent()
        args = {"path": "C:/bogus dir/calc.py"}
        assert a._enforce_workspace("read_file", args) is None
        assert args["path"] == "calc.py"

    def test_bash_escape_refused(self, make_agent):
        # The central hole: run_bash has no `path` argument, so the old guard
        # returned None immediately and every shell command bypassed it.
        a = make_agent()
        args = {"command": "Set-Content C:\\Windows\\evil.txt 'x'"}
        assert a._enforce_workspace("run_bash", args) is not None

    def test_bash_destructive_refused(self, make_agent):
        a = make_agent()
        assert a._enforce_workspace("run_bash", {"command": "Remove-Item -Recurse -Force ."}) is not None

    def test_bash_normal_command_allowed_and_pinned_to_workspace(self, make_agent, workspace):
        a = make_agent()
        args = {"command": "python main.py"}
        assert a._enforce_workspace("run_bash", args) is None
        assert Path(args["cwd"]).resolve() == workspace.resolve()

    def test_cwd_argument_is_checked(self, make_agent, tmp_path):
        a = make_agent()
        assert a._enforce_workspace("read_file", {"cwd": str(tmp_path)}) is not None


class TestCriticCheck:
    def test_binary_write_refused(self, make_agent):
        a = make_agent()
        assert a._critic_check("write_file", {"path": "app.exe"}) is not None

    def test_text_write_allowed(self, make_agent):
        a = make_agent()
        assert a._critic_check("write_file", {"path": "app.py"}) is None

    def test_powershell_recursive_delete_refused(self, make_agent):
        # The old critic only knew bash spellings, so the native Windows form
        # this agent actually produces went straight through.
        a = make_agent()
        assert a._critic_check("run_bash", {"command": "Remove-Item -Recurse -Force ."}) is not None


class TestNoChdir:
    def test_process_cwd_unchanged(self, make_agent, workspace):
        # os.chdir mutates state shared with the TUI render thread.
        before = os.getcwd()
        a = make_agent()
        (workspace / "f.txt").write_text("hi\n", encoding="utf-8")
        a._execute_tool("read_file", {"path": "f.txt"})
        assert os.getcwd() == before

    def test_relative_path_resolves_against_workspace(self, make_agent, workspace):
        a = make_agent()
        (workspace / "f.txt").write_text("marker-content\n", encoding="utf-8")
        out = a._execute_tool("read_file", {"path": "f.txt"})
        assert "marker-content" in out

    def test_write_lands_in_workspace(self, make_agent, workspace):
        a = make_agent()
        a._execute_tool("write_file", {"path": "made.txt", "content": "x"})
        assert (workspace / "made.txt").exists()

    def test_default_path_means_workspace_not_launch_dir(self, make_agent, workspace):
        # These tools default to path=".". Without chdir that used to resolve
        # to the directory the agent was launched from, listing files from
        # outside the project entirely.
        a = make_agent()
        (workspace / "only.py").write_text("x = 1\n", encoding="utf-8")
        out = a._execute_tool("list_files", {"pattern": "*.py"})
        assert "only.py" in out
        assert "1 files matching" in out

    def test_omitted_search_path_stays_in_workspace(self, make_agent, workspace):
        a = make_agent()
        (workspace / "a.py").write_text("uniquetoken\n", encoding="utf-8")
        out = a._execute_tool("search_files", {"pattern": "uniquetoken"})
        assert "a.py" in out


class TestVerifyFile:
    def test_syntax_error_reported(self, make_agent, workspace):
        a = make_agent()
        p = workspace / "bad.py"
        p.write_text("def f(:\n", encoding="utf-8")
        assert "AUTO-VERIFY FAILED" in (a._verify_file("bad.py") or "")

    def test_valid_file_passes(self, make_agent, workspace):
        a = make_agent()
        p = workspace / "ok.py"
        p.write_text("x = 1\n", encoding="utf-8")
        assert a._verify_file("ok.py") is None

    def test_run_disabled_by_default(self, make_agent, workspace):
        # Executing model-written code is opt-in; a crashing script must not
        # be run when verify_run is off.
        a = make_agent()
        p = workspace / "crash.py"
        p.write_text("if __name__ == '__main__':\n    raise SystemExit(1)\n", encoding="utf-8")
        assert a._verify_file("crash.py") is None

    def test_deny_mode_never_runs(self, make_agent, workspace):
        a = make_agent(permission_mode="deny", verify_run=True)
        p = workspace / "crash.py"
        p.write_text("if __name__ == '__main__':\n    raise SystemExit(1)\n", encoding="utf-8")
        assert a._verify_file("crash.py") is None

    def test_unsafe_code_not_executed(self, make_agent, workspace, capsys):
        a = make_agent(verify_run=True)
        p = workspace / "danger.py"
        p.write_text(
            "import shutil\nif __name__ == '__main__':\n    print('x')\n", encoding="utf-8"
        )
        assert a._verify_file("danger.py") is None
        assert "not running" in capsys.readouterr().out

    def test_outside_workspace_ignored(self, make_agent, tmp_path):
        a = make_agent()
        outside = tmp_path / "x.py"
        outside.write_text("def f(:\n", encoding="utf-8")
        assert a._verify_file(str(outside)) is None


class TestQuoteExistingPaths:
    def test_spaced_path_quoted(self, make_agent, workspace):
        a = make_agent()
        d = workspace / "src"
        d.mkdir()
        (d / "Cookie Clicker.py").write_text("x", encoding="utf-8")
        out = a._quote_existing_paths("python src/Cookie Clicker.py")
        assert '"src/Cookie Clicker.py"' in out

    def test_ordinary_args_untouched(self, make_agent):
        a = make_agent()
        cmd = "git commit -m hello world"
        assert a._quote_existing_paths(cmd) == cmd

    def test_single_token_not_quoted(self, make_agent, workspace):
        a = make_agent()
        (workspace / "main.py").write_text("x", encoding="utf-8")
        assert a._quote_existing_paths("python main.py") == "python main.py"


class TestTokenizeCmd:
    def test_plain(self):
        assert _tokenize_cmd("a b c") == [("a", False), ("b", False), ("c", False)]

    def test_double_quoted_kept_whole(self):
        assert _tokenize_cmd('a "b c"') == [("a", False), ('"b c"', True)]

    def test_single_quoted_kept_whole(self):
        assert _tokenize_cmd("a 'b c'") == [("a", False), ("'b c'", True)]


class TestContextManager:
    def test_short_history_untouched(self):
        cm = ContextManager(max_tokens=10000, reserve=1000)
        msgs = [{"role": "system", "content": "s"}, {"role": "user", "content": "u"}]
        assert cm.trim(msgs) == msgs

    def test_long_history_trimmed_under_budget(self):
        cm = ContextManager(max_tokens=2000, reserve=200)
        msgs = [{"role": "system", "content": "sys"}]
        msgs += [{"role": "user", "content": "x" * 500} for _ in range(20)]
        out = cm.trim(msgs)
        assert len(out) < len(msgs)
        assert out[0]["role"] == "system"

    def test_system_message_always_kept(self):
        cm = ContextManager(max_tokens=600, reserve=100)
        msgs = [{"role": "system", "content": "keepme"}]
        msgs += [{"role": "user", "content": "y" * 2000} for _ in range(10)]
        assert cm.trim(msgs)[0]["content"] == "keepme"

    def test_trim_is_idempotent(self):
        cm = ContextManager(max_tokens=1500, reserve=200)
        msgs = [{"role": "system", "content": "s"}]
        msgs += [{"role": "user", "content": "z" * 300} for _ in range(15)]
        once = cm.trim(msgs)
        assert cm.trim(once) == once

    def test_empty(self):
        assert ContextManager().trim([]) == []


class TestDropOrphanTools:
    def test_orphan_removed(self):
        msgs = [
            {"role": "user", "content": "hi"},
            {"role": "tool", "tool_call_id": "ghost", "content": "x"},
        ]
        assert all(m.get("role") != "tool" for m in _drop_orphan_tools(msgs))

    def test_paired_tool_kept(self):
        msgs = [
            {"role": "assistant", "tool_calls": [{"id": "a1"}]},
            {"role": "tool", "tool_call_id": "a1", "content": "ok"},
        ]
        assert len(_drop_orphan_tools(msgs)) == 2

    def test_leading_tool_dropped(self):
        msgs = [{"role": "tool", "tool_call_id": "a1", "content": "x"}]
        assert _drop_orphan_tools(msgs) == []


class TestClearOldToolResults:
    def test_recent_results_kept(self):
        msgs = [{"role": "tool", "tool_call_id": str(i), "content": "data"} for i in range(3)]
        assert all(m["content"] == "data" for m in _clear_old_tool_results(msgs))

    def test_old_results_cleared(self):
        msgs = [{"role": "tool", "tool_call_id": str(i), "content": "data"} for i in range(10)]
        out = _clear_old_tool_results(msgs)
        assert out[0]["content"] != "data"
        assert out[-1]["content"] == "data"

    def test_original_not_mutated(self):
        msgs = [{"role": "tool", "tool_call_id": str(i), "content": "data"} for i in range(10)]
        _clear_old_tool_results(msgs)
        assert msgs[0]["content"] == "data"


class TestXmlToolCalls:
    def test_parses_tool_call(self):
        content = '<tool_call>{"name": "read_file", "arguments": {"path": "a.py"}}</tool_call>'
        calls = _parse_xml_toolcalls(content)
        assert calls and calls[0]["function"]["name"] == "read_file"

    def test_no_call_returns_none(self):
        assert _parse_xml_toolcalls("just text") is None

    def test_strip_removes_tags(self):
        assert _strip_xml_toolcalls("<tool_call>{}</tool_call>hello") == "hello"

    def test_strip_removes_orphan_closing_tags(self):
        # A cut-off generation leaves unpaired tags that were otherwise
        # printed as if the model had answered.
        assert _strip_xml_toolcalls("answer</parameter></function>") == "answer"


class TestSafeJsonLoads:
    def test_valid(self):
        assert _safe_json_loads('{"a": 1}') == {"a": 1}

    def test_repairable(self):
        assert _safe_json_loads('{"a": 1,}') == {"a": 1}

    def test_empty_returns_none(self):
        assert _safe_json_loads("") is None

    def test_garbage_returns_none_or_empty(self):
        assert _safe_json_loads("!!!") in (None, "", {}, [])


class TestHelpers:
    def test_count_plan_steps_numbered(self):
        assert _count_plan_steps("1. a\n2. b\n3. c") == 3

    def test_count_plan_steps_bulleted(self):
        assert _count_plan_steps("- a\n- b") == 2

    def test_short_args_truncates(self):
        out = _short_args({"content": "x" * 200})
        assert len(out) <= 63

    def test_summarize_traceback_collapses_recursion(self):
        lines = ["Traceback (most recent call last):"]
        lines += ['  File "x.py", line 1, in f'] * 100
        lines.append("RecursionError: maximum recursion depth exceeded")
        out = _summarize_traceback("\n".join(lines))
        assert len(out) < 30
        assert any("repeated many times" in l for l in out)

    def test_summarize_short_traceback_intact(self):
        text = 'Traceback:\n  File "x.py", line 1, in f\nValueError: bad'
        assert "ValueError: bad" in "\n".join(_summarize_traceback(text))


class TestSessionManager:
    def test_similar_names_do_not_collide(self, make_agent):
        # "my session" and "my_session" both sanitised to the same filename,
        # so saving one silently overwrote the other.
        a = make_agent()
        sm = a.sessions
        p1 = sm.save("my session", [{"role": "user", "content": "one"}], False, "m", "w")
        p2 = sm.save("my_session", [{"role": "user", "content": "two"}], False, "m", "w")
        assert p1 != p2
        assert sm.load("my session")["messages"][0]["content"] == "one"
        assert sm.load("my_session")["messages"][0]["content"] == "two"

    def test_round_trip(self, make_agent):
        a = make_agent()
        a.sessions.save("s1", [{"role": "user", "content": "hi"}], True, "model-x", "ws")
        data = a.sessions.load("s1")
        assert data["plan_mode"] is True
        assert data["model"] == "model-x"

    def test_missing_session(self, make_agent):
        assert make_agent().sessions.load("nope") is None

    def test_token_cache_not_persisted(self, make_agent):
        a = make_agent()
        msg = {"role": "user", "content": "hi"}
        a.ctx.push(msg)
        a.sessions.save("s2", [msg], False, "m", "w")
        assert "_tok_cache" not in a.sessions.load("s2")["messages"][0]

    def test_list_reports_saved(self, make_agent):
        a = make_agent()
        a.sessions.save("alpha", [], False, "m", "w")
        assert any(s["name"] == "alpha" for s in a.sessions.list())


class TestNormalizeMessages:
    def test_internal_fields_stripped(self, make_agent):
        a = make_agent()
        msgs = [{"role": "assistant", "content": "x", "reasoning_content": "r",
                 "cut": "repetition", "truncated": True, "_tok_cache": ((1,), 2)}]
        out = a._normalize_messages(msgs)
        for field in ("reasoning_content", "cut", "truncated", "_tok_cache"):
            assert field not in out[0]

    def test_history_not_mutated_by_merge(self, make_agent):
        # dict(m) is shallow, so extending tool_calls in place appended to the
        # real history on every LLM call.
        a = make_agent()
        original = [
            {"role": "assistant", "content": "a", "tool_calls": [{"id": "1"}]},
            {"role": "assistant", "content": "b", "tool_calls": [{"id": "2"}]},
        ]
        a._normalize_messages(original)
        assert len(original[0]["tool_calls"]) == 1

    def test_trailing_assistant_gets_continue(self, make_agent):
        a = make_agent()
        out = a._normalize_messages([{"role": "assistant", "content": "done"}])
        assert out[-1]["role"] == "user"


class TestPlanPruning:
    def test_old_plans_removed(self, make_agent):
        a = make_agent(max_saved_plans=3)
        for i in range(6):
            (agent_mod.PLANS_DIR / f"plan_{1000 + i}.md").write_text("x", encoding="utf-8")
        a._prune_plans()
        assert len(list(agent_mod.PLANS_DIR.glob("plan_*.md"))) == 3
