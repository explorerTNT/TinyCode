from pathlib import Path

from config import Config


class TestDefaults:
    def test_port_matches_documented_llama_cpp(self):
        assert Config().lmstudio_port == 8080

    def test_base_url(self):
        c = Config(lmstudio_host="1.2.3.4", lmstudio_port=9999)
        assert c.base_url == "http://1.2.3.4:9999"

    def test_workspace_resolved(self, workspace):
        assert Config(workspace=workspace).workspace.is_absolute()

    def test_verify_run_off_by_default(self):
        assert Config().verify_run is False


class TestFromEnv:
    def test_reads_values(self, monkeypatch):
        monkeypatch.setenv("LMSTUDIO_PORT", "1234")
        monkeypatch.setenv("TINY_CODE_MODEL", "my-model")
        monkeypatch.setenv("TINY_CODE_PERMISSION", "auto")
        c = Config.from_env()
        assert c.lmstudio_port == 1234
        assert c.model_name == "my-model"
        assert c.permission_mode == "auto"

    def test_bad_int_falls_back(self, monkeypatch, capsys):
        # A typo used to raise ValueError before anything had been printed,
        # so the user got a traceback instead of a usable message.
        monkeypatch.setenv("LMSTUDIO_PORT", "not-a-port")
        c = Config.from_env()
        assert c.lmstudio_port == 8080
        assert "not an integer" in capsys.readouterr().out

    def test_bad_float_falls_back(self, monkeypatch):
        monkeypatch.setenv("TINY_CODE_TEMPERATURE", "hot")
        assert Config.from_env().temperature == 0.6

    def test_out_of_range_clamped(self, monkeypatch):
        monkeypatch.setenv("LMSTUDIO_PORT", "999999")
        assert Config.from_env().lmstudio_port == 65535

    def test_bad_choice_falls_back(self, monkeypatch):
        monkeypatch.setenv("TINY_CODE_PERMISSION", "yolo")
        assert Config.from_env().permission_mode == "ask"

    def test_empty_value_uses_default(self, monkeypatch):
        monkeypatch.setenv("TINY_CODE_MODEL", "")
        assert Config.from_env().model_name == Config.model_name

    def test_workspace_from_env(self, monkeypatch, workspace):
        monkeypatch.setenv("TINY_CODE_WORKSPACE", str(workspace))
        assert Config.from_env().workspace == workspace.resolve()

    def test_verify_run_opt_in(self, monkeypatch):
        monkeypatch.setenv("TINY_CODE_VERIFY_RUN", "1")
        assert Config.from_env().verify_run is True

    def test_timeout_configurable(self, monkeypatch):
        monkeypatch.setenv("TINY_CODE_TIMEOUT", "30")
        assert Config.from_env().request_timeout == 30


class TestContextClamp:
    def test_oversized_context_lowered(self, capsys):
        # A prompt budget that leaves no room for the reply produces an HTTP
        # 400 that reads like a server fault.
        c = Config(server_context=8192, max_tokens=4096, context_limit=8000)
        assert c.context_limit <= 8192 - 4096
        assert "lowering" in capsys.readouterr().out

    def test_reasonable_context_untouched(self):
        c = Config(server_context=16384, max_tokens=4096, context_limit=8000)
        assert c.context_limit == 8000
