import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parent.parent
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))


@pytest.fixture
def workspace(tmp_path):
    """An isolated project directory standing in for the agent's workspace."""
    ws = tmp_path / "ws"
    ws.mkdir()
    return ws


@pytest.fixture
def make_agent(workspace, monkeypatch):
    """Build a TinyCodeAgent without contacting a model server.

    The OpenAI client is constructed in __init__, which is harmless (no
    connection is made until a request), but session and plan directories are
    redirected into tmp_path so a test run never touches ~/.tiny-code.
    """
    import agent as agent_mod
    from config import Config

    def _factory(**overrides):
        sessions = workspace.parent / "sessions"
        plans = workspace.parent / "plans"
        sessions.mkdir(exist_ok=True)
        plans.mkdir(exist_ok=True)
        monkeypatch.setattr(agent_mod, "SESSION_DIR", sessions)
        monkeypatch.setattr(agent_mod, "PLANS_DIR", plans)

        params = {"workspace": workspace, "permission_mode": "auto"}
        params.update(overrides)
        return agent_mod.TinyCodeAgent(Config(**params))

    return _factory
