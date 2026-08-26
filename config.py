from dataclasses import dataclass, field
from pathlib import Path


@dataclass
class Config:
    lmstudio_host: str = "127.0.0.1"
    lmstudio_port: int = 8081
    # llama.cpp ignores this field (the model is chosen when the server starts),
    # so a plain label works. Override with TINY_CODE_MODEL when the backend
    # does care, e.g. LM Studio or any multi-model gateway.
    model_name: str = "gemma-4-E2B-it-Q4_K_M.gguf"

    # The server runs with -c 16384. The prompt must leave room for the reply
    # (max_tokens) plus chat-template overhead, otherwise llama.cpp rejects the
    # request with HTTP 400 "exceeds the available context size".
    max_tokens: int = 4096
    server_context: int = 16384
    context_limit: int = 12000
    max_tool_rounds: int = 50
    # Plans and summaries share this budget with the model's reasoning, so 512
    # leaves too little for the text itself and the plan gets cut mid-sentence.
    plan_tokens: int = 1536

    temperature: float = 0.6
    action_temperature: float = 0.1

    # Evaluated per instance: a bare default would freeze the directory that
    # happened to be current when this module was first imported.
    workspace: Path = field(default_factory=Path.cwd)

    permission_mode: str = "ask"

    @property
    def base_url(self) -> str:
        return f"http://{self.lmstudio_host}:{self.lmstudio_port}"

    @classmethod
    def from_env(cls) -> "Config":
        import os

        return cls(
            lmstudio_host=os.environ.get("LMSTUDIO_HOST", cls.lmstudio_host),
            lmstudio_port=int(os.environ.get("LMSTUDIO_PORT", str(cls.lmstudio_port))),
            model_name=os.environ.get("TINY_CODE_MODEL", cls.model_name),
            permission_mode=os.environ.get("TINY_CODE_PERMISSION", cls.permission_mode),
            temperature=float(os.environ.get("TINY_CODE_TEMPERATURE", str(cls.temperature))),
            action_temperature=float(
                os.environ.get("TINY_CODE_ACTION_TEMPERATURE", str(cls.action_temperature))
            ),
            max_tool_rounds=int(os.environ.get("TINY_CODE_MAX_ROUNDS", str(cls.max_tool_rounds))),
        )

