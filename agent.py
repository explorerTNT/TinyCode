import json
import inspect
import re
import sys
import os
import subprocess
import collections
import threading
import time
import openai
from pathlib import Path

import json_repair

from config import Config
from system_prompt import SYSTEM_PROMPT, PLAN_MODE_PROMPT
from tools import get_tools
from permissions import PermissionManager
from context import count_message_tokens
from tools.glob import suggest_files


def _safe_json_loads(text: str):
    if not text or not text.strip():
        return None
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        pass
    try:
        return json_repair.loads(text)
    except Exception:
        return None


# Substrings that make an auto-run verification unsafe to execute.
_VERIFY_UNSAFE = (
    "os.system", "subprocess", "shutil.rmtree", "os.remove",
    "os.rmdir", "os.unlink", "os.rename", "eval(", "exec(",
)

# Regex patterns a critic pass blocks before a bash command runs.
_CRITIC_BASH_DANGEROUS = (
    r"rm\s+-rf\s+/", r"rm\s+-r\s+/", r"rm\s+-rf\s+~", r"rm\s+-r\s+~",
    r"format\s+[a-z]:", r"mkfs", r":\(\)\{", r"dd\s+if=",
    r"curl\s+.*\|\s*(sh|bash)", r"wget\s+.*\|\s*(sh|bash)",
    r"del\s+/[fsq]", r"rmdir\s+/s",
)

_BINARY_EXTS = {
    ".exe", ".dll", ".so", ".png", ".jpg", ".jpeg", ".gif", ".pdf",
    ".zip", ".gz", ".tar", ".bin", ".pyc", ".obj", ".o", ".docx",
    ".xlsx", ".pptx", ".mp3", ".mp4", ".avi",
}

SESSION_DIR = Path.home() / ".tiny-code" / "sessions"
PLANS_DIR = Path.home() / ".tiny-code" / "plans"


def _build_tool_schema(fn):
    sig = inspect.signature(fn)
    doc = inspect.getdoc(fn) or ""
    desc = short_desc(doc)

    props = {}
    required = []

    type_map = {str: "string", int: "integer", float: "number", bool: "boolean"}
    descs = {
        "run_bash": {"command": "shell command", "timeout": "max seconds"},
        "read_file": {"path": "file path", "offset": "start line (1)", "limit": "max lines"},
        "write_file": {"path": "file path", "content": "file content"},
        "edit_file": {
            "path": "file path",
            "start_line": "first line to replace (from read_file) - preferred",
            "end_line": "last line to replace, inclusive",
            "new_string": "replacement text",
            "old_string": "fallback: exact text to find, if not using line numbers",
        },
        "search_files": {"pattern": "regex to search", "path": "search dir", "include": "glob filter"},
        "list_files": {"pattern": "glob pattern", "path": "search dir"},
        "ask_user": {"question": "the question to ask"},
        "web_search": {"query": "search terms", "max_results": "max results"},
        "web_fetch": {"url": "full URL", "timeout": "seconds"},
    }

    for name, param in sig.parameters.items():
        if name == "config":
            continue
        ptype = type_map.get(param.annotation, "string")
        pdesc = descs.get(fn.__name__, {}).get(name, name)
        props[name] = {"type": ptype, "description": pdesc}
        if param.default is inspect.Parameter.empty:
            required.append(name)

    return {
        "type": "function",
        "function": {
            "name": fn.__name__,
            "description": desc,
            "parameters": {
                "type": "object",
                "properties": props,
                "required": required,
            },
        },
    }


def short_desc(doc: str) -> str:
    first = doc.split("\n")[0].rstrip(".") if doc else ""
    return first[:80]


def _is_xml_tool_call(content: str) -> bool:
    s = content.strip()
    return s.startswith("<tool_call>") and s.endswith("</tool_call>")


def _strip_xml_toolcalls(text: str) -> str:
    import re
    text = re.sub(r"<tool_call>.*?</tool_call>", "", text, flags=re.DOTALL)
    text = re.sub(r"<function>.*?</function>", "", text, flags=re.DOTALL)
    text = re.sub(r"<invoke>.*?</invoke>", "", text, flags=re.DOTALL)
    # A cut-off generation leaves orphan closing tags ("</parameter>
    # </function> </tool_call>") that are not part of any pair. Left alone they
    # are printed as if the model had answered, and stored as real content.
    text = re.sub(
        r"</?(?:tool_call|function|invoke|parameter|parameters|arguments)\b[^>]*>",
        "",
        text,
    )
    return text.strip()


def _parse_xml_toolcalls(content: str) -> list | None:
    import re
    calls = []
    for m in re.finditer(r"<tool_call>(.*?)</tool_call>", content, re.DOTALL):
        text = m.group(1).strip()
        brace_depth = 0
        start = -1
        for i, ch in enumerate(text):
            if ch == "{":
                if brace_depth == 0:
                    start = i
                brace_depth += 1
            elif ch == "}":
                brace_depth -= 1
                if brace_depth == 0 and start >= 0:
                    try:
                        parsed = json.loads(text[start:i + 1])
                        if parsed is None:
                            raise json.JSONDecodeError("null", "", 0)
                        name = parsed.get("name", "")
                        args = parsed.get("arguments", parsed.get("parameters", {}))
                        calls.append({
                            "id": f"xml_call_{len(calls)}",
                            "type": "function",
                            "function": {"name": name, "arguments": json.dumps(args)},
                        })
                    except json.JSONDecodeError:
                        pass
                    start = -1
    return calls if calls else None


class Spinner:
    def __init__(self, message="жду ответа модели…"):
        self.message = message
        self.running = False
        self.thread = None

    def start(self, message=None):
        if message:
            self.message = message
        self.running = True
        self.thread = threading.Thread(target=self._spin, daemon=True)
        self.thread.start()

    def stop(self):
        self.running = False
        if self.thread:
            self.thread.join(0.3)
        sys.stdout.write("\r" + " " * 40 + "\r")
        sys.stdout.flush()

    def _spin(self):
        chars = "-/|\\-/|\\"
        i = 0
        while self.running:
            sys.stdout.write(f"\r{chars[i % len(chars)]} {self.message}")
            sys.stdout.flush()
            time.sleep(0.1)
            i += 1


_DONE_MARKER = re.compile(r"^\s*[*_`#\s]*task\s+complete[*_`.!\s]*$", re.IGNORECASE | re.MULTILINE)

_DONE_PHRASES = re.compile(
    r"(задача\s+выполнена|задание\s+выполнено|вс[ёе]\s+готово|вс[ёе]\s+сделано"
    r"|task\s+is\s+complete|implementation\s+is\s+complete|all\s+steps\s+are\s+done)",
    re.IGNORECASE,
)

_NEGATION = re.compile(r"\b(не|not|н[ие]т|cannot|can't|failed|ошибка|error)\b", re.IGNORECASE)


def _looks_done(text: str) -> bool:
    if not text:
        return False
    if _DONE_MARKER.search(text):
        return True
    tail = text[-300:]
    if _DONE_PHRASES.search(tail) and not _NEGATION.search(tail):
        return True
    return False


TOOL_RESULT_CAP = 4000
KEEP_FULL_TOOL_RESULTS = 5


def _clear_old_tool_results(messages: list) -> list:
    """Drop raw tool output beyond the last N calls, keeping the fact of the call.

    Anthropic calls this the lightest-touch compaction: once a tool result is
    deep in history, the agent rarely needs the raw bytes again.
    """
    tool_idx = [i for i, m in enumerate(messages) if m.get("role") == "tool"]
    stale = set(tool_idx[:-KEEP_FULL_TOOL_RESULTS]) if len(tool_idx) > KEEP_FULL_TOOL_RESULTS else set()
    if not stale:
        return messages

    result = []
    for i, m in enumerate(messages):
        if i in stale:
            m = dict(m)
            m["content"] = "[earlier tool result cleared to save context]"
        result.append(m)
    return result


RESPOND_TOOL = "respond"

RESPOND_SCHEMA = {
    "type": "function",
    "function": {
        "name": RESPOND_TOOL,
        "description": (
            "Send your final answer to the user. Call this when the task is done "
            "or when you need to reply with text instead of doing more work."
        ),
        "parameters": {
            "type": "object",
            "properties": {
                "message": {
                    "type": "string",
                    "description": "the reply to show the user",
                }
            },
            "required": ["message"],
            "additionalProperties": False,
        },
    },
}


def _drop_orphan_tools(messages: list) -> list:
    known_ids = set()
    for m in messages:
        for tc in m.get("tool_calls") or []:
            if tc.get("id"):
                known_ids.add(tc["id"])

    result = []
    for m in messages:
        if m.get("role") == "tool" and m.get("tool_call_id") not in known_ids:
            continue
        result.append(m)

    while result and result[0].get("role") == "tool":
        result.pop(0)
    return result


class ContextManager:
    def __init__(self, max_tokens: int = 24000, reserve: int = 4096):
        self.max_tokens = max_tokens
        self.reserve = reserve
        self.estimated = 0

    def push(self, msg: dict) -> bool:
        tokens = count_message_tokens(msg)
        self.estimated += tokens
        return self.estimated < self.max_tokens - self.reserve

    def reset(self):
        self.estimated = 0

    def trim(self, messages: list, system_idx: int = 0) -> list:
        if self.estimated < self.max_tokens - self.reserve:
            return messages
        if not messages:
            return messages

        system = messages[system_idx]
        rest = messages[system_idx + 1:]

        budget = self.max_tokens - self.reserve
        total = count_message_tokens(system)
        picked = []
        dropped = 0

        for m in reversed(rest):
            t = count_message_tokens(m)
            if total + t > budget:
                dropped += 1
                continue
            picked.append(m)
            total += t

        picked.reverse()
        picked = _drop_orphan_tools(picked)

        kept = [system]
        if dropped:
            kept.append({"role": "system", "content": "[earlier context trimmed]"})
        kept.extend(picked)

        self.estimated = sum(count_message_tokens(m) for m in kept)
        return kept


HELP_TEXT = """
Commands:
  /session              Session save/load/resume management
  /clear                Clear conversation, start fresh
  /new                  Start a new session (clear context)
  /plan [desc]          Enter plan mode (analyze first, then act)
  /compact              Summarize and shrink context
  /exit                 End session

  ! <command>           Run a bash command directly

  Tab to autocomplete file paths. Ctrl+C to interrupt.
"""


class SessionManager:
    def __init__(self, workspace: Path):
        self.workspace = workspace
        SESSION_DIR.mkdir(parents=True, exist_ok=True)
        PLANS_DIR.mkdir(parents=True, exist_ok=True)

    def _safe_path(self, name: str) -> Path:
        safe = "".join(c if c.isalnum() or c in "-_" else "_" for c in name).strip()
        return SESSION_DIR / f"{safe or 'session'}.json"

    def save(self, name: str, messages: list, plan_mode: bool, model: str, workspace: str):
        path = self._safe_path(name)
        data = {
            "name": name,
            "model": model,
            "workspace": workspace,
            "plan_mode": plan_mode,
            "messages": messages,
            "timestamp": time.time(),
        }
        path.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
        return path

    def load(self, name: str) -> dict | None:
        path = self._safe_path(name)
        if not path.exists():
            alt = SESSION_DIR / f"{name}.json"
            if not alt.exists():
                return None
            path = alt
        return json.loads(path.read_text(encoding="utf-8"))

    def list(self) -> list[dict]:
        results = []
        for f in sorted(SESSION_DIR.glob("*.json"), key=os.path.getmtime, reverse=True):
            try:
                data = json.loads(f.read_text(encoding="utf-8"))
                results.append({
                    "name": data.get("name", f.stem),
                    "model": data.get("model", "?"),
                    "messages": len(data.get("messages", [])),
                    "time": time.strftime("%b %d %H:%M", time.localtime(data.get("timestamp", 0))),
                })
            except Exception:
                pass
        return results

    def last_session(self) -> str | None:
        sessions = self.list()
        return sessions[0]["name"] if sessions else None


class TinyCodeAgent:
    def __init__(self, config: Config):
        self.config = config
        self.permissions = PermissionManager(mode=config.permission_mode)
        self.client = openai.OpenAI(
            base_url=f"{config.base_url}/v1", api_key="not-needed"
        )
        self.tools_list = get_tools(config)
        self.tool_map = {t.__name__: t for t in self.tools_list}
        self.tool_schemas = [_build_tool_schema(t) for t in self.tools_list]
        self.tool_schemas.append(RESPOND_SCHEMA)
        self.messages = []
        self._llm_calls = 0
        self.ctx = ContextManager(max_tokens=config.context_limit)
        self.plan_mode = False
        self._last_plan = ""
        self.sessions = SessionManager(config.workspace)
        self.aborted = False
        self._current_stream = None

    def _env_state(self) -> str:
        """Small models do not check state before acting, so state is given to them.

        BFCL error analysis shows even frontier models skip verifying the
        environment (e.g. `mkdir alex` while already inside `alex`).
        """
        ws = Path(self.config.workspace).resolve()
        # The absolute path is deliberately NOT shown: a 2B model copies it into
        # every tool call and mangles it ("ai_sa sandbox"). Bare names work.
        lines = [
            "you are already inside the project directory",
            "use bare relative names like calc.py or src/calc.py, never a full C:\\... path",
            f"os: Windows | shell: {'powershell' if sys.platform == 'win32' else 'sh'}",
        ]
        try:
            entries = sorted(
                p.name + ("/" if p.is_dir() else "")
                for p in ws.iterdir()
                if not p.name.startswith(".")
            )
            listing = ", ".join(entries[:25]) if entries else "(empty)"
            if len(entries) > 25:
                listing += f", ... (+{len(entries) - 25} more)"
        except OSError:
            listing = "(unreadable)"
        lines.append(f"files here: {listing}")
        return "ENVIRONMENT STATE\n" + "\n".join(lines)

    def _add_msg(self, msg):
        self.messages.append(msg)
        self.ctx.push(msg)

    def _clear_messages(self):
        self.messages.clear()
        self.ctx.reset()

    def _normalize_messages(self, messages: list) -> list:
        # Qwen best practice: history keeps only final output, never thinking.
        messages = [
            {k: v for k, v in m.items() if k not in ("reasoning_content", "cut")}
            for m in messages
        ]
        normalized = []
        for m in messages:
            if m.get("role") == "assistant" and normalized and normalized[-1].get("role") == "assistant":
                prev = normalized[-1]
                prev_content = prev.get("content", "")
                cur_content = m.get("content", "")
                if prev_content and cur_content:
                    prev["content"] = prev_content.rstrip() + "\n\n" + cur_content
                elif cur_content:
                    prev["content"] = cur_content
                if m.get("tool_calls"):
                    prev.setdefault("tool_calls", []).extend(m["tool_calls"])
            else:
                normalized.append(dict(m))

        if normalized and normalized[-1].get("role") == "assistant" and not normalized[-1].get("tool_calls"):
            normalized.append({"role": "user", "content": "Continue."})
        return normalized

    def _call_llm(self, spinner_message="жду ответа модели…", silent=False, force_prompt=None, max_tokens=None, use_tools=True, no_thinking=False):
        kwargs = {
            "model": self.config.model_name,
            "messages": self._normalize_messages(
                _clear_old_tool_results(self.ctx.trim(self.messages))
            ),
            "max_tokens": max_tokens or self.config.max_tokens,
            "temperature": self.config.temperature,
            "stream": True,
            "stream_options": {"include_usage": True},
            "timeout": 90,
        }

        if no_thinking:
            # A reasoning model can burn the whole budget and return nothing.
            # The per-request `reasoning_budget` field is ignored by llama.cpp,
            # but the chat template switch does work, so use that to force an
            # answer out on the retry instead of wasting another round.
            kwargs["extra_body"] = {"chat_template_kwargs": {"enable_thinking": False}}

        if use_tools:
            kwargs["tools"] = self.tool_schemas
            # Structured output wants near-greedy decoding; prose does not.
            kwargs["temperature"] = self.config.action_temperature
            # `respond` is always available, so forcing a call can never dead-end.
            # This makes an empty content response impossible.
            kwargs["tool_choice"] = "required"

        if use_tools:
            # Qwen's template rejects a system message anywhere but the front,
            # so state is merged into the leading system message instead.
            msgs = kwargs["messages"]
            if msgs and msgs[0].get("role") == "system":
                head = dict(msgs[0])
                head["content"] = f"{head.get('content', '')}\n\n{self._env_state()}"
                kwargs["messages"] = [head] + msgs[1:]
            else:
                kwargs["messages"] = [{"role": "system", "content": self._env_state()}] + msgs

        if force_prompt:
            kwargs["messages"] = kwargs["messages"] + [{"role": "user", "content": force_prompt}]

        spinner = Spinner(spinner_message)
        spinner.start()

        try:
            stream = self.client.chat.completions.create(**kwargs)
            self._current_stream = stream
            spinner.stop()
            if not silent:
                self._llm_calls += 1
                sys.stdout.write(f"\r  [#{self._llm_calls} модель думает…]" + " " * 10)
                sys.stdout.flush()
            try:
                result = self._process_stream(stream, silent)
            finally:
                try:
                    stream.close()
                except Exception:
                    pass
                self._current_stream = None
            return result
        except (openai.APITimeoutError, TimeoutError):
            spinner.stop()
            print("\n  [Model stalled (90s timeout). Forcing continue.]")
            return None
        except openai.APIConnectionError:
            spinner.stop()
            print(f"\n  [Error: Can't connect to the model server at {self.config.base_url}]")
            print(f"  [Make sure the server is running on port {self.config.lmstudio_port}]\n")
            return None
        except openai.RateLimitError:
            spinner.stop()
            print("\n  [Error: Rate limited. Wait and try again.]\n")
            return None
        except Exception as e:
            spinner.stop()
            print(f"\n  [Error: {e}]\n")
            return None

    def abort(self):
        """Interrupt the running model generation / tool loop (UI triggered)."""
        self.aborted = True
        stream = getattr(self, "_current_stream", None)
        if stream is not None:
            try:
                stream.close()
            except Exception:
                pass

    def _process_stream(self, stream, silent=False):
        start = time.time()
        content = ""
        reasoning = ""
        tool_calls = collections.defaultdict(lambda: {"name": "", "args": "", "id": ""})
        finish_reason = None
        repeated = False

        def _status(label, n):
            sys.stdout.write(f"\r  [#{self._llm_calls} {label}: {n} ток • {time.time() - start:.0f}с]" + " " * 10)
            sys.stdout.flush()

        usage = None
        for chunk in stream:
            if self.aborted:
                break
            if getattr(chunk, "usage", None) is not None:
                usage = chunk.usage
            if not chunk.choices:
                continue

            delta = chunk.choices[0].delta
            finish = chunk.choices[0].finish_reason
            if finish:
                finish_reason = finish

            if not delta:
                continue

            rc = getattr(delta, "reasoning_content", None)
            if rc:
                reasoning += rc
                if not silent:
                    _status("модель думает", len(reasoning))

            if delta.content:
                content += delta.content
                if not silent:
                    _status("модель отвечает", len(content))
                if len(content) > 600:
                    tail = content[-200:].lower()
                    prev = content[: -200]
                    if prev.rfind(tail) >= max(0, len(prev) - 400):
                        repeated = True
                        break

            if delta.tool_calls:
                if not silent:
                    sys.stdout.write(f"\r  [#{self._llm_calls} модель готовит вызов инструмента…]" + " " * 10)
                    sys.stdout.flush()
                for tc in delta.tool_calls:
                    idx = tc.index if tc.index is not None else len(tool_calls)
                    if tc.id:
                        tool_calls[idx]["id"] = tc.id
                    if tc.function:
                        if tc.function.name:
                            tool_calls[idx]["name"] += tc.function.name
                        if tc.function.arguments:
                            tool_calls[idx]["args"] += tc.function.arguments

        if self.aborted:
            return None

        if not silent:
            elapsed = time.time() - start
            comp = getattr(usage, "completion_tokens", None) if usage else None
            if comp is not None:
                tok = f"{comp} ток"
            else:
                chars = len(reasoning) + len(content)
                tok = f"{chars} симв"
            if tool_calls:
                summary = f"\r  [#{self._llm_calls} вызов инструмента • {tok} • {elapsed:.0f}с]"
            elif len(reasoning) + len(content):
                summary = f"\r  [#{self._llm_calls} модель • {tok} • {elapsed:.0f}с]"
            else:
                summary = f"\r  [#{self._llm_calls} пусто • {elapsed:.0f}с]"
            sys.stdout.write(summary + "\n")
            sys.stdout.flush()

        if repeated and not tool_calls:
            if not silent:
                print("\n  [repetition detected, response cut]")
            return {"role": "assistant", "content": content, "cut": "repetition"}

        if tool_calls:
            result = self._build_from_stream(content, tool_calls, finish_reason)
            if result:
                return result

        if not tool_calls and content:
            parsed = _parse_xml_toolcalls(content)
            if parsed:
                msg = {"role": "assistant", "content": content, "tool_calls": parsed}
                self._add_msg(msg)
                return msg

        if content:
            msg = {"role": "assistant", "content": content}
            self._add_msg(msg)
            return msg

        if reasoning:
            return {"role": "assistant", "content": "", "reasoning_content": reasoning}

        return None

    def _build_from_stream(self, content: str, tool_calls: dict, finish: str):
        built_calls = []
        truncated = False
        for idx, tc in sorted(tool_calls.items()):
            raw_args = tc["args"] or ""
            parsed = _safe_json_loads(raw_args) if raw_args else {}
            # json_repair silently closes a JSON string that the model never
            # finished, turning a cut-off generation into a "successful" write
            # of truncated content. Detect that and flag it instead.
            if raw_args and finish == "length":
                truncated = True
            elif raw_args:
                try:
                    json.loads(raw_args)
                except json.JSONDecodeError:
                    truncated = True
            built_calls.append({
                "id": tc["id"] or f"call_{idx}",
                "type": "function",
                "function": {"name": tc["name"], "arguments": json.dumps(parsed or {})},
            })

        if not built_calls:
            return None

        msg = {"role": "assistant", "content": content, "tool_calls": built_calls}
        if truncated:
            msg["truncated"] = True
        self._add_msg(msg)
        return msg

    WRITE_TOOLS = {"write_file", "edit_file", "run_bash"}

    def _enforce_workspace(self, name: str, args: dict) -> str | None:
        ws = self.config.workspace
        if not ws:
            return None
        ws = Path(ws).resolve()

        raw = args.get("path")
        if raw is None:
            return None

        raw = str(raw)
        try:
            candidate = Path(raw)
            resolved = candidate.resolve() if candidate.is_absolute() else (ws / candidate).resolve()
            resolved.relative_to(ws)
            return None
        except (ValueError, OSError):
            pass

        # Small models mangle long absolute paths ("ai_sa sandbox\calc.py").
        # If the file name alone is unambiguous and exists in the workspace,
        # repair it instead of refusing (a hard error sends it into a retry loop).
        name = Path(raw.replace("/", "\\")).name
        if name and name not in (".", ".."):
            repaired = (ws / name).resolve()
            try:
                repaired.relative_to(ws)
            except ValueError:
                repaired = None
            if repaired is not None and repaired.exists():
                args["path"] = name
                print(f"  [path corrected: {raw!r} -> {name!r}]")
                return None

        candidates = suggest_files(raw, ws)
        if candidates:
            # The model copies a user-provided absolute path and mangles the
            # leading segments, but almost always keeps the file name intact.
            # If exactly one workspace file has that name, repair silently
            # instead of failing (a hard error starts a wrong-path guessing loop).
            if name and name not in (".", ".."):
                exact = [c for c in candidates if Path(c).name.lower() == name.lower()]
                if len(exact) == 1:
                    args["path"] = exact[0]
                    print(f"  [path corrected: {raw!r} -> {exact[0]!r}]")
                    return None
            return (
                f"Error: '{raw}' is outside the project directory. "
                f"Did you mean:\n" + "\n".join(f"  {c}" for c in candidates)
            )
            return (
                f"Error: '{raw}' is outside the project directory. "
                "Use a bare relative name like calc.py instead."
            )

    def _critic_check(self, name: str, args: dict) -> str | None:
        """Cheap pre-execution sanity check for obviously bad tool calls.

        A 2B model sometimes emits self-destructive commands or writes text
        into binary files. Catching that here (before the tool runs) is far
        cheaper than discovering a corrupted workspace afterward.
        """
        if name == "run_bash":
            cmd = str(args.get("command", "")).lower()
            for pat in _CRITIC_BASH_DANGEROUS:
                if re.search(pat, cmd):
                    return (
                        f"Error: command rejected by critic pass — matches a "
                        f"destructive pattern ({pat!r}). Rewrite it to be safe "
                        "or use a narrower, non-destructive command."
                    )
            return None
        if name in ("write_file", "edit_file"):
            path = str(args.get("path", ""))
            if Path(path).suffix.lower() in _BINARY_EXTS:
                return (
                    f"Error: '{path}' looks like a binary file. Writing text to "
                    "it will corrupt the file. Use a text-based format or a "
                    "binary-safe tool."
                )
        return None

    def _verify_file(self, path_str: str) -> str | None:
        """Best-effort post-write check. Returns an error string to feed back
        to the model, or None when the file looks healthy.

        2B models frequently emit files that do not even parse. A cheap
        ``py_compile`` catches the bulk of those mistakes without executing
        anything. Runnable scripts (those with a ``__main__`` guard) get a
        guarded runtime pass so import/runtime errors surface too.
        """
        try:
            p = Path(path_str)
            if not p.is_absolute():
                p = (Path(self.config.workspace) / p).resolve()
            if not p.exists() or p.suffix != ".py":
                return None
        except (OSError, ValueError):
            return None

        # 1) Syntax check — always safe, catches most 2B failures.
        try:
            proc = subprocess.run(
                [sys.executable, "-m", "py_compile", str(p)],
                capture_output=True, text=True, timeout=25,
                cwd=str(self.config.workspace),
            )
        except subprocess.TimeoutExpired:
            return None
        if proc.returncode != 0:
            err_lines = [l for l in (proc.stderr or proc.stdout).splitlines() if l.strip()][-6:]
            return (
                "AUTO-VERIFY FAILED (syntax): the file you just wrote does not "
                "compile. Fix the error below:\n" + "\n".join(err_lines)
            )

        # 2) Guarded runtime check for runnable scripts only.
        try:
            src = p.read_text(encoding="utf-8", errors="replace")
        except OSError:
            return None
        if "__main__" not in src:
            return None
        if any(tok in src for tok in _VERIFY_UNSAFE):
            return None

        try:
            proc = subprocess.run(
                [sys.executable, str(p)],
                capture_output=True, text=True, timeout=15,
                cwd=str(self.config.workspace),
            )
        except subprocess.TimeoutExpired:
            return "AUTO-VERIFY FAILED (runtime): script ran past the 15s limit — check for an infinite loop."
        if proc.returncode != 0:
            out = [l for l in (proc.stderr or proc.stdout).splitlines() if l.strip()][-8:]
            return (
                "AUTO-VERIFY FAILED (runtime): the script crashed on execution. "
                "Fix the error below:\n" + "\n".join(out)
            )
        return None

    def _execute_tool(self, name: str, args: dict) -> str:
        if name not in self.tool_map:
            return f"Error: Unknown tool '{name}'"

        if self.plan_mode and name in self.WRITE_TOOLS:
            return "Error: Write/bash tools are disabled in PLAN MODE. Only read-only tools allowed."

        err = self._enforce_workspace(name, args)
        if err:
            return err

        fn = self.tool_map[name]

        if name == "run_bash":
            if sys.platform == "win32":
                from tools.bash import _sanitize_cmd

                original = args.get("command", "")
                cleaned = _sanitize_cmd(original)
                if cleaned != original:
                    print(f"  [command normalized: {cleaned}]")
                    args["command"] = cleaned
            if not self.permissions.check_bash(args.get("command", "")):
                return "Error: Command rejected by user"

        if name in ("write_file", "edit_file"):
            if not self.permissions.check_write(args.get("path", "")):
                return "Error: Write rejected by user"

        if name == "run_bash":
            print("  [команда выполняется…]", flush=True)

        critic = self._critic_check(name, args)
        if critic:
            return critic

        orig_cwd = os.getcwd()
        try:
            if self.config.workspace:
                os.chdir(self.config.workspace)
            result = str(fn(**args))
        except Exception as e:
            return f"Error executing {name}: {e}"
        finally:
            os.chdir(orig_cwd)

        if name in ("write_file", "edit_file") and not result.startswith("Error:"):
            verify = self._verify_file(args.get("path", ""))
            if verify:
                print("  [auto-verify: ошибки в файле, возвращаю модели для фикса]")
                result = f"{result}\n\n{verify}"
        return result

    def _process_turn(self, max_rounds: int = None, silent=False):
        if max_rounds is None:
            max_rounds = self.config.max_tool_rounds
        recent_tools = []
        recent_errors = []
        reasoning_rounds = 0
        last_content_norm = None
        repeat_count = 0
        did_work = False
        text_only_rounds = 0
        self.aborted = False
        for rnd in range(max_rounds):
            if self.aborted:
                print("\n  [прервано пользователем]\n")
                return
            is_last = rnd == max_rounds - 1
            msg = self._call_llm(silent=silent, use_tools=not is_last)
            if self.aborted:
                print("\n  [прервано пользователем]\n")
                return
            if msg is None:
                msg = self._call_llm(silent=silent, force_prompt="Continue with the next step.")
                if msg is None:
                    print("\n  [model did not respond, stopping]\n")
                    return

            # A reasoning model can spend the whole budget thinking and return
            # nothing usable. Salvage the round by retrying with thinking off
            # (llama.cpp ignores per-request reasoning_budget, but the chat
            # template switch works), instead of spending a round on a plea.
            if not msg.get("tool_calls") and not msg.get("content"):
                print(f"  [{rnd+1}/{max_rounds}] (empty reply, retrying without thinking)")
                retry = self._call_llm(
                    silent=silent,
                    use_tools=not is_last,
                    no_thinking=True,
                    force_prompt=(
                        "Your previous response was empty. "
                        "Output the next tool call now, without thinking."
                    ),
                )
                if retry is not None and (retry.get("tool_calls") or retry.get("content")):
                    msg = retry

            tc_list = msg.get("tool_calls")
            content = msg.get("content", "")

            if tc_list:
                reasoning_rounds = 0
                last_content_norm = None
                repeat_count = 0
                text_only_rounds = 0
                for tc in tc_list:
                    try:
                        name = tc["function"]["name"]
                        raw_args = tc["function"]["arguments"]
                        args = _safe_json_loads(raw_args) or {}
                        if name == RESPOND_TOOL:
                            answer = str(args.get("message", "")).strip()
                            if answer:
                                print(f"  {answer}\n")
                            else:
                                print()
                            return
                        print(f"  [{rnd+1}/{max_rounds} tool: {name}({_short_args(args)})]", flush=True)
                        if msg.get("truncated") and name in ("write_file", "edit_file"):
                            result = (
                                "Error: your tool call was cut off mid-generation, so the "
                                "content is incomplete and was NOT written. Write the file in "
                                "smaller pieces: create it with the first part, then append the "
                                "rest with edit_file."
                            )
                        else:
                            result = self._execute_tool(name, args)
                    except json.JSONDecodeError:
                        result = f"Error: Invalid JSON in tool arguments"
                    except Exception as e:
                        result = f"Error: {e}"

                    if result.startswith("Error:"):
                        print(f"  !!! {result}")
                        recent_errors.append(f"{name}: {result[:200]}")
                        if len(recent_errors) > 8:
                            recent_errors.pop(0)
                    else:
                        did_work = True
                        preview = result[:120].replace("\n", " ")
                        print(f"  -> result ({len(result)}c): {preview}")

                    if len(result) > TOOL_RESULT_CAP:
                        result = (
                            result[:TOOL_RESULT_CAP]
                            + f"\n\n[truncated at {TOOL_RESULT_CAP} chars. "
                            "Use a narrower search or read a specific line range.]"
                        )
                    self._add_msg({"role": "tool", "tool_call_id": tc["id"], "content": result})

                    try:
                        arg_key = json.dumps(args, sort_keys=True, ensure_ascii=False, default=str)
                    except Exception:
                        arg_key = str(args)
                    recent_tools.append((name, arg_key))
                    if len(recent_tools) > 6:
                        recent_tools.pop(0)
                    same = [t for t in recent_tools if t == (name, arg_key)]
                    if len(same) >= 3:
                        print("  [repeating same tool, recovery prompt]")
                        seen = []
                        for e in recent_errors[-5:]:
                            if e not in seen:
                                seen.append(e)
                        err_block = "\n".join(f"- {e[:300]}" for e in seen[-4:])
                        self._add_msg({
                            "role": "user",
                            "content": (
                                "You keep calling the same tool with the same arguments "
                                "and it keeps failing. Errors you got:\n"
                                f"{err_block}\n"
                                "Do NOT repeat the same call. Re-examine the situation, "
                                "change your approach, or use a different tool."
                            ),
                        })
                        recent_tools.clear()
                        recent_errors.clear()
            elif content:
                if msg.get("cut") == "repetition":
                    print("  [task finished: model repeated the answer]\n")
                    return
                clean_content = _strip_xml_toolcalls(content)
                if clean_content:
                    preview = clean_content[:300].replace("\n", " ")
                    print(f"  [{rnd+1}/{max_rounds}] {preview}")
                    msg["content"] = clean_content

                    norm = " ".join(clean_content.lower().split())
                    if norm and norm == last_content_norm:
                        repeat_count += 1
                    else:
                        repeat_count = 0
                        last_content_norm = norm
                    if repeat_count >= 2:
                        print("  [model repeating same answer, stopping]\n")
                        return

                if _looks_done(clean_content or content):
                    print()
                    return

                if did_work and text_only_rounds >= 1:
                    print("  [model answered in text twice, treating as done]\n")
                    return

                text_only_rounds += 1
                self._add_msg({
                    "role": "user",
                    "content": (
                        "If the task is fully done, reply with exactly TASK COMPLETE on its own line. "
                        "Otherwise call the next tool now. Do not repeat your previous message."
                    ),
                })
            else:
                # The no-thinking retry above already failed for this round.
                reasoning_rounds += 1
                if reasoning_rounds > 2:
                    print("\n  [model stuck in reasoning loop, stopping]\n")
                    return

        print("\n  [max rounds reached, summarizing work...]")
        msg = self._call_llm(
            silent=True,
            max_tokens=512,
            force_prompt=(
                "You reached the tool-use limit. Stop using tools. "
                "Write a short summary of what was accomplished and what remains to be done."
            ),
        )
        if msg and msg.get("content"):
            print(msg["content"].strip())
        print()

    def _show_plan(self, plan: str):
        """Store, print and persist a finished plan."""
        self._last_plan = plan
        steps = _count_plan_steps(plan)
        if steps:
            print(f"  [plan has {steps} steps, allocating up to {self.config.max_tool_rounds} rounds]")
        print("\n" + "=" * 50)
        print("  PLAN")
        print("=" * 50)
        for line in plan.strip().split("\n"):
            print(f"  {line}")
        print("=" * 50)

        plan_path = PLANS_DIR / f"plan_{int(time.time())}.md"
        plan_path.write_text(plan, encoding="utf-8")

    def _process_plan_turn(self):
        print("  [analyzing and creating plan...]")
        self.aborted = False
        for rnd in range(3):
            if self.aborted:
                print("\n  [прервано пользователем]\n")
                return
            msg = self._call_llm(silent=True, max_tokens=self.config.plan_tokens)
            if self.aborted:
                print("\n  [прервано пользователем]\n")
                return
            if msg is None:
                msg = self._call_llm(silent=True, force_prompt="Output your plan now. No more analysis needed.", max_tokens=self.config.plan_tokens)
                if msg is None:
                    print("  [model did not respond]\n")
                    return

            tc_list = msg.get("tool_calls")
            if not tc_list and not (msg.get("content") or "").strip():
                # Reasoning ate the whole reply. Retry with thinking off rather
                # than accepting an empty string as a finished plan.
                retry = self._call_llm(
                    silent=True,
                    max_tokens=self.config.plan_tokens,
                    no_thinking=True,
                    force_prompt="Output your numbered plan now. Do not think, just write it.",
                )
                if retry is not None and (retry.get("content") or "").strip():
                    msg = retry
                    tc_list = msg.get("tool_calls")
                else:
                    continue

            if not tc_list:
                plan = msg.get("content", "")
                self._show_plan(_strip_xml_toolcalls(plan))
                return

            for tc in tc_list:
                try:
                    name = tc["function"]["name"]
                    args = json.loads(tc["function"]["arguments"])
                    # `respond` is offered to the model in every mode, but it
                    # only means "I am done" - in plan mode that means the plan
                    # itself is ready, not that a tool should run.
                    if name == RESPOND_TOOL:
                        plan = _strip_xml_toolcalls(str(args.get("message", "")))
                        if plan.strip():
                            self._show_plan(plan)
                            return
                        result = "Error: empty plan. Write the numbered plan as the message."
                        print(f"  !!! {result}")
                        self._add_msg({"role": "tool", "tool_call_id": tc["id"], "content": result})
                        continue
                    print(f"  [{rnd+1}/25 tool: {name}({_short_args(args)})]", flush=True)
                    result = self._execute_tool(name, args)
                except json.JSONDecodeError:
                    result = f"Error: Invalid JSON in tool arguments"
                except Exception as e:
                    result = f"Error: {e}"

                if result.startswith("Error:"):
                    print(f"  !!! {result}")
                self._add_msg({"role": "tool", "tool_call_id": tc["id"], "content": result})

        msg = self._call_llm(silent=True, force_prompt="Stop using tools. Output your numbered plan now.", max_tokens=self.config.plan_tokens)
        plan = _strip_xml_toolcalls((msg or {}).get("content", "") or "")
        if not plan.strip():
            msg = self._call_llm(
                silent=True,
                max_tokens=self.config.plan_tokens,
                no_thinking=True,
                force_prompt="Stop using tools. Write your numbered plan now, without thinking.",
            )
            plan = _strip_xml_toolcalls((msg or {}).get("content", "") or "")

        if plan.strip():
            self._show_plan(plan)
        else:
            self._last_plan = ""
            print("  [model did not create a plan]\n")

    def _compact_messages(self):
        if len(self.messages) <= 2:
            print("  [nothing to compact]\n")
            return

        history = "\n".join(
            f"{m.get('role', '?')}: {str(m.get('content', ''))[:400]}"
            for m in self.messages[1:]
        )[:6000]

        summary_prompt = (
            "Summarize the conversation below. Keep the key facts needed to continue the work: "
            "the current task, files read/written, decisions made, and next steps. "
            "Output ONLY the summary, no tool calls, max ~200 words.\n\n"
            f"--- CONVERSATION ---\n{history}"
        )

        msg = self._call_llm(silent=True, max_tokens=512, force_prompt=summary_prompt, use_tools=False)
        summary = msg.get("content", "").strip() if msg else ""

        if not summary:
            summary = str(self.messages[-1].get("content") or "")[:2000]

        self._clear_messages()
        self._add_msg({"role": "system", "content": SYSTEM_PROMPT})
        self._add_msg({
            "role": "user",
            "content": f"Summary of the previous conversation:\n{summary}",
        })
        print("  [context compacted: model summary]\n")

    def _handle_session_command(self, cmd: str) -> bool:
        parts = cmd.strip().split(maxsplit=1)
        sub = parts[0] if parts else ""

        if sub == "/sessions":
            sessions = self.sessions.list()
            if not sessions:
                print("  [no saved sessions]\n")
            else:
                print(f"\n  {'Name':<20} {'Model':<30} {'Msgs':<6} {'Time'}")
                print(f"  {'─'*60}")
                for s in sessions:
                    print(f"  {s['name']:<20} {s['model']:<30} {s['messages']:<6} {s['time']}")
                print()
            return True

        if sub == "/session":
            args = parts[1] if len(parts) > 1 else ""

            if args.startswith("save"):
                name = args[4:].strip() or f"session_{int(time.time())}"
                path = self.sessions.save(
                    name, self.messages, self.plan_mode,
                    self.config.model_name, str(self.config.workspace),
                )
                print(f"  [session saved: {path.name}]\n")
                return True

            if args.startswith("load") or args.startswith("resume"):
                name = args[5:].strip() if len(args) > 5 else ""
                if not name:
                    name = self.sessions.last_session()
                    if not name:
                        print("  [no sessions to resume]\n")
                        return True
                data = self.sessions.load(name)
                if not data:
                    search = name
                    for s in self.sessions.list():
                        if search in s["name"]:
                            data = self.sessions.load(s["name"])
                            break
                if not data:
                    print(f"  [session '{name}' not found]\n")
                    return True
                self.messages = data.get("messages", [])
                self.plan_mode = data.get("plan_mode", False)
                print(f"  [resumed session: {data.get('name', name)} ({len(self.messages)} messages)]\n")
                return True

            if args.startswith("list"):
                return self._handle_session_command("/sessions")

            print("  Usage: /session save [name] | /session load [name] | /session list\n")
            return True

        return False

    def _handle_command(self, cmd: str) -> bool:
        c = cmd.strip()

        if c in ("/exit",):
            print("bye!")
            sys.exit(0)

        elif c == "/help":
            print(HELP_TEXT)
            return True

        elif c == "/clear":
            self._clear_messages()
            self._add_msg({"role": "system", "content": SYSTEM_PROMPT})
            self.plan_mode = False
            self._last_plan = ""
            print("  [context cleared, fresh start]\n")
            return True

        elif c == "/new":
            try:
                self.sessions.save(
                    f"session_{int(time.time())}", self.messages,
                    self.plan_mode, self.config.model_name, str(self.config.workspace),
                )
            except Exception:
                pass
            self._clear_messages()
            self._add_msg({"role": "system", "content": SYSTEM_PROMPT})
            self.plan_mode = False
            self._last_plan = ""
            print("  [new session — previous saved, context cleared]\n")
            return True

        elif c.startswith("/plan"):
            self.plan_mode = True
            desc = c[5:].strip()
            self._clear_messages()
            self._add_msg({"role": "system", "content": PLAN_MODE_PROMPT})
            if desc:
                self._add_msg({"role": "user", "content": desc})
            print()
            return True

        elif c == "/compact":
            self._compact_messages()
            return True

        elif c.startswith("!"):
            cmd_text = c[1:].strip()
            result = self.tool_map["run_bash"](command=cmd_text)
            print(result)
            return True

        return False

    def _maybe_git_hint(self, user_input: str) -> str:
        """A 2B model ignores the system prompt and searches files for commit
        messages, which never works: commit text lives in git history. If the
        user asks about a commit, inject a concrete git command into the prompt."""
        low = user_input.lower()
        if not any(k in low for k in ("commit", "коммит", "что было сделано", "что сделали", "изменения в")):
            return user_input
        return user_input + (
            "\n\nNote: this question is about git history. Use run_bash, e.g. "
            "`git log --oneline -30` then `git show <hash>`. "
            "Commit messages are NOT in the files; do not use search_files for them."
        )

    def run(self):
        self._add_msg({"role": "system", "content": SYSTEM_PROMPT})

        print(f"  tiny-code \u2014 model: {self.config.model_name}")
        print(f"  workspace: {self.config.workspace.resolve()}")
        print(f"  permissions: {self.config.permission_mode}")
        print(f"  Type /help for commands. Ctrl+C to exit.\n")

        while True:
            try:
                user_input = input(">>> ").strip()
                if not user_input:
                    continue
            except (EOFError, KeyboardInterrupt):
                print("\nbye!")
                break

            if user_input.lower() in ("exit", "quit"):
                print("bye!")
                break

            if self._handle_session_command(user_input):
                continue

            if self._handle_command(user_input):
                if self.plan_mode and user_input.startswith("/plan"):
                    self._process_plan_turn()
                    self._handle_plan_approval()
                continue

            if user_input.startswith("/"):
                print(f"  [неизвестная команда: {user_input} — введите /help]")
                continue

            try:
                if self.plan_mode:
                    self._add_msg({"role": "user", "content": user_input})
                    self._process_plan_turn()
                    self._handle_plan_approval()
                else:
                    self._add_msg({"role": "user", "content": self._maybe_git_hint(user_input)})
                    print()
                    self._process_turn()
                    print()
            except KeyboardInterrupt:
                # Interrupt the current task, not the whole session.
                print("\n  [interrupted, session kept]\n")

    def _handle_plan_approval(self):
        if not (self._last_plan or "").strip():
            print("  [no plan to approve - try /plan again]\n")
            self.plan_mode = False
            return

        print("\n  [Plan ready. Approve and execute? (y/n/edit)] ", end="", flush=True)
        try:
            resp = input().strip().lower()
        except (EOFError, KeyboardInterrupt):
            resp = "n"

        if resp == "y":
            plan = self._last_plan
            self._clear_messages()
            self._add_msg({"role": "system", "content": SYSTEM_PROMPT})
            self._add_msg({
                "role": "user",
                "content": (
                    "Execute this plan step by step:\n\n"
                    f"{plan}\n\n"
                    "Work through each step. Read files before editing. "
                    "Show progress as you go."
                ),
            })
            self.plan_mode = False
            print()
            self._process_turn(silent=False)
            print()
        elif resp == "edit":
            print("  [Edit the plan and say 'continue']\n")
            self.plan_mode = True
        else:
            print("  [Plan rejected. Type /plan again or give feedback.]\n")
            self.plan_mode = False
            self._last_plan = ""

    def run_once(self, prompt: str):
        self._add_msg({"role": "system", "content": SYSTEM_PROMPT})
        self._add_msg({"role": "user", "content": prompt})
        print()
        self._process_turn()
        print()


def _count_plan_steps(plan: str) -> int:
    count = 0
    for line in plan.split("\n"):
        line = line.strip()
        if not line:
            continue
        if line[0].isdigit() and len(line) > 1 and line[1] in ". ):-":
            count += 1
        elif line.startswith(("-", "*", "•")):
            count += 1
    return count


def _short_args(args: dict, max_len: int = 60) -> str:
    parts = []
    for k, v in args.items():
        s = str(v) if not isinstance(v, (dict, list)) else json.dumps(v, ensure_ascii=False)
        if len(s) > 30:
            s = s[:27] + "..."
        parts.append(f"{k}={s}")
    result = ", ".join(parts)
    if len(result) > max_len:
        result = result[: max_len - 3] + "..."
    return result


def main():
    import argparse

    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    if hasattr(sys.stderr, "reconfigure"):
        sys.stderr.reconfigure(encoding="utf-8", errors="replace")

    parser = argparse.ArgumentParser(description="tiny-code — lightweight local AI coding agent")
    parser.add_argument("prompt", nargs="*", help="Optional prompt to run directly")
    parser.add_argument("--model", help="Override model name")
    parser.add_argument("--workspace", help="Workspace directory")
    parser.add_argument("--permission", choices=["auto", "ask", "deny"], help="Permission mode")
    parser.add_argument("--resume", nargs="?", const=True, help="Resume last or named session")

    args = parser.parse_args()
    config = Config.from_env()

    if args.model:
        config.model_name = args.model
    if args.workspace:
        config.workspace = Path(args.workspace).resolve()
    if args.permission:
        config.permission_mode = args.permission

    if not args.prompt:
        try:
            import textual  # noqa: F401
            from tui import run_tui
        except ImportError:
            pass
        else:
            run_tui(config)
            return

    agent = TinyCodeAgent(config)

    if args.resume:
        sm = SessionManager(config.workspace)
        if isinstance(args.resume, str) and args.resume:
            data = sm.load(args.resume)
        else:
            name = sm.last_session()
            data = sm.load(name) if name else None
        if data:
            agent.messages = data.get("messages", [])
            agent.plan_mode = data.get("plan_mode", False)
            print(f"  [resumed session: {data.get('name', '?')} ({len(agent.messages)} messages)]\n")
        else:
            print("  [no session to resume]\n")

    if args.prompt:
        agent.run_once(" ".join(args.prompt))
    else:
        agent.run()


if __name__ == "__main__":
    main()
