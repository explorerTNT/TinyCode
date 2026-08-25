"""Textual TUI for TinyCode.

The agent still writes to ``sys.stdout`` and calls ``input()`` as in the
console version. Inside the TUI we redirect both: stdout lines land in a
scrolling :class:`RichLog` (with fenced code blocks syntax-highlighted), live
status updates (carriage-return lines) drive the side panel, and blocked
``input()`` calls are satisfied by the bottom input field. This keeps
``agent.py`` completely untouched.
"""
from __future__ import annotations

import builtins
import io
import os
import queue
import sys
import time
from pathlib import Path

from rich.syntax import Syntax
from rich.text import Text

from textual.app import App, ComposeResult
from textual.containers import Horizontal, Vertical
from textual.widgets import Footer, Header, Input, RichLog, Static, Tree

from tools.glob import EXCLUDE_DIRS

DARK = True
SYNTAX_THEME = "ansi_dark" if DARK else "ansi_light"


class TUIWriter(io.TextIOBase):
    """stdout replacement: routes agent output to widgets.

    Lines with a bare ``\\r`` (no newline) are live status updates — they
    overwrite the same console line, so here they drive the status panel.
    Everything else is appended to the log, with fenced code blocks rendered
    through Rich's syntax highlighter.
    """

    def __init__(self, app: "TinyCodeTUI"):
        self.app = app
        self._buf = ""
        self._in_code = False
        self._code_lang = "text"

    def write(self, s: str) -> int:
        if not s:
            return 0
        if "\r" in s and "\n" not in s:
            text = s.rsplit("\r", 1)[-1].rstrip()
            self.app.update_status(text)
            return len(s)
        if "\r" in s:
            s = s.replace("\r", "")
        self._buf += s
        while "\n" in self._buf:
            line, self._buf = self._buf.split("\n", 1)
            self.app.append_line(self._render(line))
        return len(s)

    def flush(self) -> None:
        if self._buf:
            self.app.append_line(self._render(self._buf))
            self._buf = ""

    def _render(self, line: str):
        stripped = line.strip()
        if stripped.startswith("```"):
            if self._in_code:
                self._in_code = False
            else:
                self._in_code = True
                self._code_lang = stripped[3:].strip() or "text"
            return None
        if self._in_code:
            lang = self._code_lang if self._code_lang != "text" else "python"
            return Syntax(line, lang, word_wrap=True, background_color="default", theme=SYNTAX_THEME)
        return Text(line)


class TinyCodeTUI(App):
    CSS = """
    Screen { background: $surface; }
    #body { height: 1fr; }
    #log { width: 3fr; height: 100%; border: round $accent; }
    #side { width: 1fr; height: 100%; }
    #status { height: auto; border: round $accent; padding: 1; margin-bottom: 1; }
    #files { height: 1fr; border: round $accent; }
    #input { margin: 0 1; }
    """

    BINDINGS = [
        ("ctrl+c", "quit", "Выход"),
        ("f", "refresh_files", "Файлы"),
    ]

    def __init__(self, config, prompt=None):
        super().__init__()
        self.config = config
        self.prompt = prompt
        self._input_q: "queue.Queue[str]" = queue.Queue()
        self._orig_stdout = None
        self._orig_input = None
        self.agent = None
        self._last_ctrl_c = 0.0

    def compose(self) -> ComposeResult:
        yield Header()
        yield Horizontal(
            RichLog(id="log", markup=False, wrap=True),
            Vertical(
                Static(id="status"),
                Tree(self.config.workspace.name, id="files"),
                id="side",
            ),
            id="body",
        )
        yield Input(id="input", placeholder="Спросите агента…  Enter — отправить · Esc — остановить модель · Ctrl+C — копировать выделение (×2 — выход)")
        yield Footer()

    def on_mount(self) -> None:
        self.theme = "textual-dark"
        side = self.query_one("#status", Static)
        side.update(Text(
            f"tiny-code\n"
            f"модель: {self.config.model_name}\n"
            f"workspace: {self.config.workspace}\n"
            f"perms: {self.config.permission_mode}"
        ))
        self._build_tree()
        self.query_one("#input", Input).disabled = True
        self._orig_stdout = sys.stdout
        self._orig_input = builtins.input
        sys.stdout = TUIWriter(self)
        builtins.input = self._tui_input
        self.run_worker(self._run_agent, thread=True, group="agent")

    def on_unmount(self) -> None:
        self._restore()

    def action_refresh_files(self) -> None:
        self._build_tree()

    def on_key(self, event) -> None:
        if event.key == "escape":
            event.prevent_default()
            # Abort only while the agent is busy (input disabled). While the
            # user is typing, Esc does nothing special.
            inp = self.query_one("#input", Input)
            if inp.disabled:
                agent = getattr(self, "agent", None)
                if agent is not None:
                    agent.abort()
                    self.append_line(Text("[Esc] генерация остановлена — введите новый запрос или продолжите."))
        elif event.key == "ctrl+c":
            event.prevent_default()
            self._handle_ctrl_c()

    def _handle_ctrl_c(self) -> None:
        try:
            log = self.query_one("#log", RichLog)
            sel_obj = log.text_selection
            if sel_obj is None:
                sel = ""
            else:
                got = log.get_selection(sel_obj)
                sel = (got[0] if got else "").strip()
        except Exception:
            sel = ""
        if sel:
            try:
                self.copy_to_clipboard(sel)
                self.append_line(Text(f"[Ctrl+C] скопировано в буфер: {len(sel)} симв."))
            except Exception:
                self.append_line(Text("[Ctrl+C] буфер недоступен, не удалось скопировать."))
            return
        now = time.monotonic()
        if now - self._last_ctrl_c < 1.5:
            self.exit()
        else:
            self._last_ctrl_c = now
            self.append_line(Text("[Ctrl+C] ещё раз для выхода (или выделите текст мышью, чтобы скопировать)."))

    def _restore(self) -> None:
        if self._orig_stdout is not None:
            sys.stdout = self._orig_stdout
        if self._orig_input is not None:
            builtins.input = self._orig_input

    # ---- redirected stdout -> widgets (must run on the main thread) ----
    def append_line(self, renderable) -> None:
        if renderable is None:
            return
        try:
            self.query_one("#log", RichLog).write(renderable)
        except Exception:
            pass

    def update_status(self, text: str) -> None:
        try:
            self.query_one("#status", Static).update(Text(f"статус\n{text}"))
        except Exception:
            pass

    # ---- file tree ----
    def _build_tree(self) -> None:
        try:
            tree = self.query_one("#files", Tree)
        except Exception:
            return
        tree.clear()
        tree.root.label = Text(str(self.config.workspace.name))
        self._node_count = 0
        self._add_nodes(tree.root, self.config.workspace.resolve(), depth=0)
        tree.root.expand()

    def _add_nodes(self, parent, path: Path, depth: int) -> None:
        if depth > 6 or self._node_count > 600:
            return
        try:
            entries = sorted(path.iterdir(), key=lambda p: (p.is_file(), p.name.lower()))
        except OSError:
            return
        for e in entries:
            if e.name.startswith("."):
                continue
            if e.is_dir():
                if e.name in EXCLUDE_DIRS:
                    continue
                node = parent.add(Text(e.name + "/"))
                self._node_count += 1
                self._add_nodes(node, e, depth + 1)
            else:
                parent.add_leaf(Text(e.name))
                self._node_count += 1

    # ---- redirected input ----
    def _tui_input(self, prompt: str = "") -> str:
        self.call_from_thread(self._enable_input, prompt)
        return self._input_q.get()

    def _enable_input(self, prompt: str) -> None:
        inp = self.query_one("#input", Input)
        if prompt:
            inp.placeholder = prompt
        inp.disabled = False
        inp.focus()

    def on_input_submitted(self, event: Input.Submitted) -> None:
        value = event.value.strip()
        inp = self.query_one("#input", Input)
        inp.value = ""
        inp.disabled = True
        self._input_q.put(value)

    # ---- agent runner (worker thread) ----
    def _run_agent(self) -> None:
        try:
            from agent import TinyCodeAgent

            agent = TinyCodeAgent(self.config)
            self.agent = agent
            if self.prompt:
                agent.run_once(self.prompt)
            else:
                agent.run()
        except Exception as e:  # noqa: BLE001
            self.call_from_thread(self.append_line, Text(f"[red]FATAL: {e}[/red]"))
        finally:
            self._restore()
            self.call_from_thread(self.exit)


def run_tui(config, prompt=None) -> None:
    TinyCodeTUI(config, prompt).run()
