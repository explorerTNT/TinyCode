"""Textual TUI for TinyCode.

The agent still writes to ``sys.stdout`` and calls ``input()`` as in the
console version. Inside the TUI we redirect both: stdout lines land in a
scrolling log (with fenced code blocks syntax-highlighted), live status
updates (carriage-return lines) drive the side panel, and blocked ``input()``
calls are satisfied by the bottom input field. This keeps ``agent.py``
completely untouched.

The log is a :class:`VerticalScroll` of :class:`Static` widgets rather than a
``RichLog``: RichLog renders its lines without offset metadata, so Textual
cannot select or copy any text out of it.
"""
from __future__ import annotations

import builtins
import io
import queue
import sys
import threading
import time
from pathlib import Path

from rich.syntax import Syntax
from rich.text import Text

from textual.app import App, ComposeResult
from textual.binding import Binding
from textual.containers import Horizontal, Vertical, VerticalScroll
from textual.widgets import Footer, Header, Input, Static, Tree

from tools.glob import EXCLUDE_DIRS

DARK = True
SYNTAX_THEME = "ansi_dark" if DARK else "ansi_light"

# Sentinel pushed into the input queue on shutdown to release a blocked
# input() call in the agent thread.
_QUIT = object()

# Upper bound on log widgets kept alive; older lines are discarded.
MAX_LOG_LINES = 2000


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
        self._lock = threading.Lock()

    # Some libraries inspect these before writing; TextIOBase reports None /
    # raises, which is enough to break them.
    @property
    def encoding(self) -> str:
        return "utf-8"

    @property
    def errors(self) -> str:
        return "replace"

    def writable(self) -> bool:
        return True

    def isatty(self) -> bool:
        return False

    def write(self, s: str) -> int:
        if not s:
            return 0
        # write() is called from the agent worker thread. Textual widgets are
        # not thread-safe, so only the buffering happens here; the widget calls
        # are marshalled onto the UI thread by app.append_line/update_status.
        with self._lock:
            if "\r" in s and "\n" not in s:
                text = s.rsplit("\r", 1)[-1].rstrip()
                self.app.update_status(text)
                return len(s)
            if "\r" in s:
                s = s.replace("\r", "")
            self._buf += s
            pending = []
            while "\n" in self._buf:
                line, self._buf = self._buf.split("\n", 1)
                pending.append(self._render(line))
        for renderable in pending:
            self.app.append_line(renderable)
        return len(s)

    def flush(self) -> None:
        with self._lock:
            if not self._buf:
                return
            renderable = self._render(self._buf)
            self._buf = ""
        self.app.append_line(renderable)

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
    #log > Static { height: auto; }
    #side { width: 1fr; height: 100%; }
    #status { height: auto; border: round $accent; padding: 1; margin-bottom: 1; }
    #files { height: 1fr; border: round $accent; }
    #input { margin: 0 1; }
    """

    # Textual binds ctrl+c itself (system binding -> action_help_quit) and puts
    # the terminal in raw mode, so no SIGINT is ever delivered. priority=True is
    # what actually takes it over.
    BINDINGS = [
        Binding("ctrl+c", "interrupt", "Копировать/выход", priority=True, show=False),
        Binding("escape", "abort_generation", "Стоп", priority=True, show=False),
        Binding("f2", "refresh_files", "Файлы"),
    ]

    def __init__(self, config, prompt=None, on_agent_ready=None):
        super().__init__()
        self.config = config
        self.prompt = prompt
        self.on_agent_ready = on_agent_ready
        self._input_q: "queue.Queue[str]" = queue.Queue()
        self._orig_stdout = None
        self._orig_input = None
        self.agent = None
        self._last_ctrl_c = 0.0
        self._awaiting_input = False
        self._ui_thread_id = threading.get_ident()

    def compose(self) -> ComposeResult:
        yield Header()
        yield Horizontal(
            # RichLog cannot be selected with the mouse (its lines carry no
            # offset metadata), so the log is a scrollable stack of Static
            # widgets instead - those support selection and copying.
            VerticalScroll(id="log"),
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
        self._ui_thread_id = threading.get_ident()
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
        threading.Thread(target=self._run_agent, daemon=True).start()

    def on_unmount(self) -> None:
        self._restore()

    def action_refresh_files(self) -> None:
        self._build_tree()

    def action_abort_generation(self) -> None:
        # Only meaningful while the agent is busy; while the user is typing,
        # Esc should not interrupt anything.
        if self._awaiting_input:
            return
        agent = self.agent
        if agent is not None:
            agent.abort()
            self.append_line(Text("[Esc] генерация остановлена — введите новый запрос или продолжите."))

    def action_interrupt(self) -> None:
        # Ask the screen rather than a single widget: the selection may span
        # several Static lines in the log.
        try:
            sel = (self.screen.get_selected_text() or "").strip()
        except Exception:
            sel = ""
        if sel:
            try:
                self.copy_to_clipboard(sel)
                self.append_line(Text(f"[Ctrl+C] скопировано в буфер: {len(sel)} симв."))
                self.screen.clear_selection()
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
    def _on_ui_thread(self) -> bool:
        return threading.get_ident() == self._ui_thread_id

    def _dispatch(self, fn, *args) -> None:
        """Run a widget update on the UI thread, wherever the caller lives.

        The agent runs in a worker thread and writes through the redirected
        stdout, so touching widgets directly from there races with Textual's
        own rendering and drops output.
        """
        if self._on_ui_thread():
            fn(*args)
            return
        try:
            self.call_from_thread(fn, *args)
        except Exception:
            # The app is shutting down (or not started yet); dropping late
            # output is better than crashing the agent thread.
            pass

    def append_line(self, renderable) -> None:
        if renderable is None:
            return
        self._dispatch(self._write_log, renderable)

    def _write_log(self, renderable) -> None:
        try:
            log = self.query_one("#log", VerticalScroll)
        except Exception:
            return
        try:
            line = Static(renderable)
            log.mount(line)
            # Keep the widget count bounded: an unbounded log makes every
            # relayout slower until the UI crawls.
            children = log.children
            if len(children) > MAX_LOG_LINES:
                for stale in children[: len(children) - MAX_LOG_LINES]:
                    stale.remove()
            # Only follow the tail when the user has not scrolled up to read
            # something, otherwise the view jumps away mid-selection.
            if log.scroll_offset.y >= log.max_scroll_y - 2:
                log.scroll_end(animate=False)
        except Exception:
            pass

    def update_status(self, text: str) -> None:
        self._dispatch(self._write_status, text)

    def _write_status(self, text: str) -> None:
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
        self._dispatch(self._enable_input, prompt)
        value = self._input_q.get()
        if value is _QUIT:
            # The app is gone. Raising EOFError mimics a closed stdin, which
            # agent.run() already treats as "end the session" - without it the
            # worker thread blocks forever and the process never exits.
            raise EOFError("TUI closed")
        return value

    def _enable_input(self, prompt: str) -> None:
        self._awaiting_input = True
        inp = self.query_one("#input", Input)
        if prompt:
            inp.placeholder = prompt
        inp.disabled = False
        inp.focus()

    def on_input_submitted(self, event: Input.Submitted) -> None:
        value = event.value.strip()
        if value:
            self.append_line(Text(f">>> {value}", style="bold cyan"))
        inp = self.query_one("#input", Input)
        inp.value = ""
        inp.disabled = True
        self._awaiting_input = False
        self._input_q.put(value)

    def exit(self, *args, **kwargs):
        # Unblock a worker thread parked in input() before tearing the app
        # down, otherwise the process hangs on a non-daemon queue wait.
        self._input_q.put(_QUIT)
        return super().exit(*args, **kwargs)

    # ---- agent runner (worker thread) ----
    def _run_agent(self) -> None:
        try:
            from agent import TinyCodeAgent

            agent = TinyCodeAgent(self.config)
            self.agent = agent
            if self.on_agent_ready is not None:
                self.on_agent_ready(agent)
            if self.prompt:
                agent.run_once(self.prompt)
            else:
                agent.run()
        except (EOFError, SystemExit):
            pass
        except Exception as e:  # noqa: BLE001
            import traceback

            detail = traceback.format_exc(limit=6)
            self.append_line(Text(f"FATAL: {e}\n{detail}", style="bold red"))
        finally:
            self._restore()
            self._dispatch(self.exit)


def run_tui(config, prompt=None, on_agent_ready=None) -> None:
    TinyCodeTUI(config, prompt, on_agent_ready).run()
