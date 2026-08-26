import os
from dataclasses import dataclass, field
from pathlib import Path


def _env_int(name: str, default: int, lo: int | None = None, hi: int | None = None) -> int:
    """Read an int from the environment without letting a typo kill startup.

    A bare int() on an unset-but-misspelled variable raised ValueError before
    any output had been produced, so the user saw a traceback instead of a
    usable message.
    """
    raw = os.environ.get(name)
    if raw is None or raw.strip() == "":
        return default
    try:
        value = int(raw.strip())
    except ValueError:
        print(f"  [config: {name}={raw!r} is not an integer, using {default}]")
        return default
    if lo is not None and value < lo:
        print(f"  [config: {name}={value} below minimum {lo}, using {lo}]")
        return lo
    if hi is not None and value > hi:
        print(f"  [config: {name}={value} above maximum {hi}, using {hi}]")
        return hi
    return value


def _env_float(name: str, default: float, lo: float | None = None, hi: float | None = None) -> float:
    raw = os.environ.get(name)
    if raw is None or raw.strip() == "":
        return default
    try:
        value = float(raw.strip())
    except ValueError:
        print(f"  [config: {name}={raw!r} is not a number, using {default}]")
        return default
    if lo is not None and value < lo:
        return lo
    if hi is not None and value > hi:
        return hi
    return value


def _env_choice(name: str, default: str, allowed: tuple[str, ...]) -> str:
    raw = os.environ.get(name)
    if raw is None or raw.strip() == "":
        return default
    value = raw.strip().lower()
    if value not in allowed:
        print(f"  [config: {name}={raw!r} not one of {allowed}, using {default}]")
        return default
    return value


def _env_path(name: str, default: Path) -> Path:
    raw = os.environ.get(name)
    if raw is None or raw.strip() == "":
        return default
    try:
        return Path(raw.strip()).resolve()
    except OSError:
        print(f"  [config: {name}={raw!r} is not a usable path, using {default}]")
        return default


@dataclass
class Config:
    lmstudio_host: str = "127.0.0.1"
    # Matches the llama.cpp invocation in the README. LM Studio's own default
    # is 1234; override with LMSTUDIO_PORT.
    lmstudio_port: int = 8080
    # llama.cpp ignores this field (the model is chosen when the server starts),
    # so a plain label works. Override with TINY_CODE_MODEL when the backend
    # does care, e.g. LM Studio or any multi-model gateway.
    model_name: str = "qwen3.5-2b"

    # The prompt must leave room for the reply (max_tokens) plus chat-template
    # overhead, otherwise llama.cpp rejects the request with HTTP 400
    # "exceeds the available context size".
    max_tokens: int = 4096
    server_context: int = 16384
    context_limit: int = 12000
    # Sized from observed runs rather than picked round: a healthy 2B task
    # finishes in 2-4 rounds, so this is roughly 3x p95. A cap is the airbag,
    # not the brake - at 50 a confused model had 46 rounds of budget left after
    # solving the task, which it spent breaking it again.
    max_tool_rounds: int = 14
    # Wall-clock ceiling per turn. An iteration cap alone does not bound time:
    # one observed round spent 629s inside a single runaway tool call.
    turn_budget_seconds: int = 600
    # Plans and summaries share this budget with the model's reasoning, so 512
    # leaves too little for the text itself and the plan gets cut mid-sentence.
    plan_tokens: int = 1536

    temperature: float = 0.6
    action_temperature: float = 0.1

    request_timeout: int = 90

    # Running freshly generated code is a real execution risk, so it is opt-in
    # rather than silently on. Syntax checking always happens regardless.
    verify_run: bool = False

    # Plans accumulate one file per run; keep the directory bounded.
    max_saved_plans: int = 50

    # Evaluated per instance: a bare default would freeze the directory that
    # happened to be current when this module was first imported.
    workspace: Path = field(default_factory=Path.cwd)

    permission_mode: str = "ask"

    def __post_init__(self):
        self.workspace = Path(self.workspace).resolve()
        # A prompt that cannot fit its own reply produces an HTTP 400 that
        # reads like a server fault, so clamp it here where the cause is clear.
        ceiling = self.server_context - self.max_tokens - 512
        if ceiling > 0 and self.context_limit > ceiling:
            print(
                f"  [config: context_limit {self.context_limit} leaves no room "
                f"for a {self.max_tokens}-token reply in a {self.server_context} "
                f"context, lowering to {ceiling}]"
            )
            self.context_limit = ceiling

    @property
    def base_url(self) -> str:
        return f"http://{self.lmstudio_host}:{self.lmstudio_port}"

    @classmethod
    def from_env(cls) -> "Config":
        return cls(
            lmstudio_host=os.environ.get("LMSTUDIO_HOST", cls.lmstudio_host).strip() or cls.lmstudio_host,
            lmstudio_port=_env_int("LMSTUDIO_PORT", cls.lmstudio_port, 1, 65535),
            model_name=os.environ.get("TINY_CODE_MODEL", cls.model_name).strip() or cls.model_name,
            permission_mode=_env_choice(
                "TINY_CODE_PERMISSION", cls.permission_mode, ("auto", "ask", "deny")
            ),
            temperature=_env_float("TINY_CODE_TEMPERATURE", cls.temperature, 0.0, 2.0),
            action_temperature=_env_float(
                "TINY_CODE_ACTION_TEMPERATURE", cls.action_temperature, 0.0, 2.0
            ),
            max_tool_rounds=_env_int("TINY_CODE_MAX_ROUNDS", cls.max_tool_rounds, 1, 500),
            turn_budget_seconds=_env_int(
                "TINY_CODE_TURN_BUDGET", cls.turn_budget_seconds, 0, 86400
            ),
            max_tokens=_env_int("TINY_CODE_MAX_TOKENS", cls.max_tokens, 128, 131072),
            server_context=_env_int("TINY_CODE_SERVER_CONTEXT", cls.server_context, 512, 1048576),
            context_limit=_env_int("TINY_CODE_CONTEXT_LIMIT", cls.context_limit, 512, 1048576),
            plan_tokens=_env_int("TINY_CODE_PLAN_TOKENS", cls.plan_tokens, 128, 32768),
            request_timeout=_env_int("TINY_CODE_TIMEOUT", cls.request_timeout, 5, 3600),
            verify_run=_env_choice(
                "TINY_CODE_VERIFY_RUN", "0", ("0", "1", "true", "false", "yes", "no")
            ) in ("1", "true", "yes"),
            max_saved_plans=_env_int("TINY_CODE_MAX_PLANS", cls.max_saved_plans, 0, 100000),
            workspace=_env_path("TINY_CODE_WORKSPACE", Path.cwd()),
        )
