"""Guards against a model that will not stop, and against redoing finished work.

These cover the failure observed in a real run: the model solved the task in
round 2, then in round 3 spent 629 seconds emitting 1197 tokens of malformed
code into a write_file argument. Nothing stopped it, because the repetition and
length checks only ever ran on `content` - and `tool_choice="required"` makes
the model answer with a tool call instead.
"""
import time
import types

import pytest

from agent import MAX_TOOL_ARG_CHARS, _args_repeating


def make_chunk(*, content=None, tool_args=None, tool_name=None, finish=None, index=0):
    """Build a minimal object shaped like an OpenAI streaming chunk."""
    function = None
    tool_calls = None
    if tool_args is not None or tool_name is not None:
        function = types.SimpleNamespace(name=tool_name, arguments=tool_args)
        tool_calls = [types.SimpleNamespace(index=index, id=f"call_{index}", function=function)]
    delta = types.SimpleNamespace(
        content=content, tool_calls=tool_calls, reasoning_content=None
    )
    choice = types.SimpleNamespace(index=0, delta=delta, finish_reason=finish)
    return types.SimpleNamespace(choices=[choice], usage=None)


class TestArgsRepeating:
    def test_short_args_ignored(self):
        assert not _args_repeating({0: {"args": "small"}})

    def test_clean_long_args_pass(self):
        body = "".join(f'line {i} unique content here\\n' for i in range(60))
        assert not _args_repeating({0: {"args": body}})

    def test_repeating_block_detected(self):
        block = "print('the same thing over and over again') " * 40
        assert _args_repeating({0: {"args": block}})

    def test_missing_args_key_is_safe(self):
        assert not _args_repeating({0: {}})


class TestStreamGuards:
    def test_overlong_arguments_cut(self, make_agent):
        a = make_agent()
        # One oversized chunk stands in for a model that keeps going.
        chunks = [
            make_chunk(tool_name="write_file", tool_args="x" * (MAX_TOOL_ARG_CHARS + 100)),
            make_chunk(finish="tool_calls"),
        ]
        msg = a._process_stream(iter(chunks), silent=True)
        assert msg is not None
        assert msg.get("runaway") == "overlong"

    def test_stream_stops_early_on_overlong(self, make_agent):
        a = make_agent()
        consumed = []

        def gen():
            for i in range(500):
                consumed.append(i)
                yield make_chunk(tool_name="write_file", tool_args="y" * 500)

        a._process_stream(gen(), silent=True)
        # The whole point is not draining the generator: a real stream keeps
        # costing wall-clock time for every chunk produced.
        assert len(consumed) < 60

    def test_repeating_arguments_cut(self, make_agent):
        a = make_agent()

        def gen():
            yield make_chunk(tool_name="write_file", tool_args='{"content": "')
            for _ in range(40):
                yield make_chunk(tool_args="print('same line repeated') ")
            yield make_chunk(finish="tool_calls")

        msg = a._process_stream(gen(), silent=True)
        assert msg is not None
        assert msg.get("runaway") == "repetition"

    def test_normal_tool_call_unaffected(self, make_agent):
        a = make_agent()
        chunks = [
            make_chunk(tool_name="write_file", tool_args='{"path": "a.py", '),
            make_chunk(tool_args='"content": "x = 1\\n"}'),
            make_chunk(finish="tool_calls"),
        ]
        msg = a._process_stream(iter(chunks), silent=True)
        assert msg is not None
        assert "runaway" not in msg
        assert msg["tool_calls"][0]["function"]["name"] == "write_file"

    def test_plain_text_reply_unaffected(self, make_agent):
        a = make_agent()
        chunks = [make_chunk(content="a short answer"), make_chunk(finish="stop")]
        msg = a._process_stream(iter(chunks), silent=True)
        assert msg["content"] == "a short answer"
        assert "runaway" not in msg


class TestContentGuards:
    def test_overlong_prose_cut(self, make_agent):
        from agent import MAX_CONTENT_CHARS

        a = make_agent()
        chunks = [
            make_chunk(content="word " * ((MAX_CONTENT_CHARS // 5) + 50)),
            make_chunk(finish="stop"),
        ]
        msg = a._process_stream(iter(chunks), silent=True)
        assert msg.get("cut") == "repetition"

    def test_long_prose_stream_stops_early(self, make_agent):
        a = make_agent()
        consumed = []

        def gen():
            for i in range(400):
                consumed.append(i)
                yield make_chunk(content=f"sentence number {i} with distinct words ")

        a._process_stream(gen(), silent=True)
        assert len(consumed) < 150

    def test_short_prose_survives(self, make_agent):
        a = make_agent()
        chunks = [make_chunk(content="Done: created the file."), make_chunk(finish="stop")]
        msg = a._process_stream(iter(chunks), silent=True)
        assert msg.get("cut") is None


class TestRewriteRefusal:
    def _prove(self, agent, workspace, name="calc.py"):
        (workspace / name).write_text("print('ok')\n", encoding="utf-8")
        agent._execute_tool("write_file", {"path": name, "content": "print('ok')\n"})
        agent._execute_tool("run_bash", {"command": f"python {name}"})
        return name

    def test_proven_file_recorded(self, make_agent, workspace):
        a = make_agent()
        name = self._prove(a, workspace)
        assert name in a._proven_files

    def test_rewriting_proven_file_refused(self, make_agent, workspace):
        a = make_agent()
        name = self._prove(a, workspace)
        before = (workspace / name).read_text(encoding="utf-8")
        result = a._execute_tool("write_file", {"path": name, "content": "garbage = broken\n"})
        assert result.startswith("Error")
        assert (workspace / name).read_text(encoding="utf-8") == before

    def test_refusal_points_at_respond(self, make_agent, workspace):
        a = make_agent()
        name = self._prove(a, workspace)
        result = a._execute_tool("write_file", {"path": name, "content": "x = 1\n"})
        assert "respond" in result

    def test_repeated_attempts_escalate(self, make_agent, workspace):
        a = make_agent()
        name = self._prove(a, workspace)
        for _ in range(3):
            result = a._execute_tool("write_file", {"path": name, "content": "x = 1\n"})
        assert "Stop" in result

    def test_targeted_edit_still_allowed(self, make_agent, workspace):
        # A real fix must remain possible; only whole-file overwrites are refused.
        a = make_agent()
        name = self._prove(a, workspace)
        result = a._execute_tool(
            "edit_file", {"path": name, "start_line": 1, "new_string": "print('fixed')"}
        )
        assert not result.startswith("Error")
        assert "fixed" in (workspace / name).read_text(encoding="utf-8")

    def test_unproven_file_can_be_rewritten(self, make_agent, workspace):
        a = make_agent()
        a._execute_tool("write_file", {"path": "draft.py", "content": "x = 1\n"})
        result = a._execute_tool("write_file", {"path": "draft.py", "content": "x = 2\n"})
        assert not result.startswith("Error")

    def test_failed_run_does_not_prove(self, make_agent, workspace):
        a = make_agent()
        (workspace / "boom.py").write_text("raise SystemExit(3)\n", encoding="utf-8")
        a._execute_tool("write_file", {"path": "boom.py", "content": "raise SystemExit(3)\n"})
        a._execute_tool("run_bash", {"command": "python boom.py"})
        assert "boom.py" not in a._proven_files
        result = a._execute_tool("write_file", {"path": "boom.py", "content": "print('fix')\n"})
        assert not result.startswith("Error")

    def test_new_turn_clears_protection(self, make_agent, workspace):
        a = make_agent()
        name = self._prove(a, workspace)
        a._reset_progress()
        result = a._execute_tool("write_file", {"path": name, "content": "print('next task')\n"})
        assert not result.startswith("Error")

    def test_proven_state_reaches_the_model(self, make_agent, workspace):
        a = make_agent()
        self._prove(a, workspace)
        state = a._env_state()
        assert "REFUSED" in state
        assert "respond" in state


class TestProgressLedger:
    def test_successful_write_recorded(self, make_agent, workspace):
        a = make_agent()
        a._execute_tool("write_file", {"path": "calc.py", "content": "x = 1\n"})
        assert any("calc.py" in e for e in a._progress_lines())
        assert "calc.py" in a._verified_files

    def test_broken_write_not_marked_verified(self, make_agent, workspace):
        a = make_agent()
        # write_file refuses invalid Python outright, so the file never lands.
        a._execute_tool("write_file", {"path": "bad.py", "content": "def f(:\n"})
        assert "bad.py" not in a._verified_files

    def test_bash_result_recorded(self, make_agent, workspace):
        a = make_agent()
        (workspace / "hi.py").write_text("print('hello')\n", encoding="utf-8")
        a._execute_tool("run_bash", {"command": "python hi.py"})
        assert any("ran `" in e for e in a._progress_lines())

    def test_failed_bash_marked_failed(self, make_agent, workspace):
        a = make_agent()
        (workspace / "boom.py").write_text("raise SystemExit(3)\n", encoding="utf-8")
        a._execute_tool("run_bash", {"command": "python boom.py"})
        assert any("FAILED" in e for e in a._progress_lines())

    def test_ledger_reaches_the_model(self, make_agent, workspace):
        a = make_agent()
        a._execute_tool("write_file", {"path": "calc.py", "content": "x = 1\n"})
        state = a._env_state()
        assert "ALREADY DONE THIS TURN" in state
        assert "calc.py" in state
        # Written but not yet executed: the model is steered toward running it
        # rather than rewriting it.
        assert "Run them instead of rewriting" in state

    def test_clean_state_has_no_ledger(self, make_agent):
        assert "ALREADY DONE THIS TURN" not in make_agent()._env_state()

    def test_no_duplicate_entries(self, make_agent, workspace):
        a = make_agent()
        for _ in range(3):
            a._execute_tool("write_file", {"path": "calc.py", "content": "x = 1\n"})
        entries = [e for e in a._progress_lines() if "calc.py" in e]
        assert len(entries) == 1

    def test_ledger_is_bounded(self, make_agent, workspace):
        a = make_agent()
        for i in range(20):
            a._execute_tool("write_file", {"path": f"f{i}.py", "content": "x = 1\n"})
        assert len(a._progress_lines()) <= 8

    def test_reset_clears_state(self, make_agent, workspace):
        a = make_agent()
        a._execute_tool("write_file", {"path": "calc.py", "content": "x = 1\n"})
        a._reset_progress()
        assert a._progress_lines() == []
        assert a._verified_files == set()

    def test_failed_verify_asks_for_a_fix(self, make_agent, workspace):
        a = make_agent()
        p = workspace / "mod.py"
        p.write_text("x = 1\n", encoding="utf-8")
        # edit_file has no syntax gate, so a broken edit does land on disk.
        a._execute_tool("edit_file", {"path": "mod.py", "start_line": 1, "new_string": "def f(:"})
        assert "mod.py" not in a._verified_files
        assert any("fix it" in e for e in a._progress_lines())


class TestBudgets:
    def test_round_cap_sized_for_small_models(self):
        from config import Config
        # 50 left a confused model 46 rounds of budget after finishing.
        assert Config().max_tool_rounds <= 20

    def test_time_budget_present(self):
        from config import Config
        assert Config().turn_budget_seconds > 0

    def test_time_budget_configurable(self, monkeypatch):
        from config import Config
        monkeypatch.setenv("TINY_CODE_TURN_BUDGET", "30")
        assert Config.from_env().turn_budget_seconds == 30

    def test_time_budget_can_be_disabled(self, monkeypatch):
        from config import Config
        monkeypatch.setenv("TINY_CODE_TURN_BUDGET", "0")
        assert Config.from_env().turn_budget_seconds == 0

    def test_budget_stops_the_loop(self, make_agent, monkeypatch, capsys):
        a = make_agent(turn_budget_seconds=60)
        calls = {"n": 0}

        clock = {"t": 1000.0}
        monkeypatch.setattr(time, "time", lambda: clock["t"])

        # Each reply is distinct, so the repeated-answer guard cannot fire and
        # the time budget is the only thing that can end this loop.
        def advancing_reply(*args, **kwargs):
            calls["n"] += 1
            clock["t"] += 25.0  # each model call burns 25s of the 60s budget
            return {"role": "assistant", "content": f"step number {calls['n']}"}

        monkeypatch.setattr(a, "_call_llm", advancing_reply)
        a._process_turn(max_rounds=50)

        assert "time budget" in capsys.readouterr().out
        # 60s budget at 25s per call: stops after ~3 rounds, not 50.
        assert calls["n"] <= 4

    def test_summary_lists_completed_work(self, make_agent, workspace, capsys):
        a = make_agent()
        a._execute_tool("write_file", {"path": "calc.py", "content": "x = 1\n"})
        a._summarize_progress()
        assert "calc.py" in capsys.readouterr().out
