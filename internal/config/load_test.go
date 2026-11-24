package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateConfig points the config dir at a temp home and clears every
// environment override, so a test sees only what it writes itself.
func isolateConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	for _, key := range []string{
		"TINYCODE_HOST", "TINYCODE_PORT", "TINYCODE_NAME", "TINYCODE_PERMISSION_MODE",
		"TINYCODE_WORKSPACE", "TINYCODE_MAX_TOKENS", "TINYCODE_SERVER_CONTEXT",
		"TINYCODE_CONTEXT_LIMIT", "TINYCODE_MAX_TOOL_ROUNDS", "TINYCODE_PLAN_TOKENS",
		"TINYCODE_BTW_MAX_TOKENS", "TINYCODE_STALL_TIMEOUT", "TINYCODE_TEMPERATURE",
		"TINYCODE_ACTION_TEMPERATURE", "TINYCODE_LANG",
	} {
		t.Setenv(key, "")
	}
	return filepath.Join(home, ".tinycode", "config.json")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadCreatesDefaultConfig(t *testing.T) {
	path := isolateConfig(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file was not created: %v", err)
	}
	if cfg.LM.Port != 1234 || cfg.TN.ContextLimit != 26000 || cfg.TN.StallTimeout != 120 {
		t.Fatalf("unexpected defaults: %+v %+v", cfg.LM, cfg.TN)
	}
}

// A config written by an older version lacks newer fields; they must fall
// back to defaults instead of silently becoming zero.
func TestLoadFillsMissingFields(t *testing.T) {
	path := isolateConfig(t)
	writeFile(t, path, `{
		"language": "ru",
		"language_model": {"port": 8080},
		"tiny_code": {"permission_mode": "auto", "workspace": "C:/stale"}
	}`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Language != "ru" || cfg.LM.Port != 8080 || cfg.TN.PermissionMode != "auto" {
		t.Fatalf("values from the file were lost: %+v %+v", cfg.LM, cfg.TN)
	}
	if cfg.LM.Host != "127.0.0.1" || cfg.LM.Name != "qwen/qwen3.5-9b" {
		t.Fatalf("missing model fields not defaulted: %+v", cfg.LM)
	}
	if cfg.TN.MaxTokens != 4096 || cfg.TN.ContextLimit != 26000 || cfg.TN.MaxToolRounds != 50 ||
		cfg.TN.PlanTokens != 1536 || cfg.TN.BtwMaxTokens != 1024 || cfg.TN.StallTimeout != 120 ||
		cfg.TN.Temperature != 0.6 || cfg.TN.ActionTemperature != 0.1 {
		t.Fatalf("missing fields not defaulted: %+v", cfg.TN)
	}
	if cfg.TN.Workspace != "" {
		t.Fatalf("workspace must not be read from the file, got %q", cfg.TN.Workspace)
	}
	if cfg.TN.RepoMapChars != 4000 || cfg.TN.DisableSyntaxCheck {
		t.Fatalf("new fields not defaulted: %+v", cfg.TN)
	}
}

func TestLoadBooleanEnv(t *testing.T) {
	isolateConfig(t)
	for raw, want := range map[string]bool{"true": true, "1": true, "false": false, "0": false} {
		t.Setenv("TINYCODE_DISABLE_SYNTAX_CHECK", raw)
		cfg, err := Load()
		if err != nil || cfg.TN.DisableSyntaxCheck != want {
			t.Fatalf("TINYCODE_DISABLE_SYNTAX_CHECK=%q -> (%v, %v), want %v", raw, cfg.TN.DisableSyntaxCheck, err, want)
		}
	}
	t.Setenv("TINYCODE_DISABLE_SYNTAX_CHECK", "maybe")
	if _, err := Load(); err == nil {
		t.Fatal("a value that is not a boolean must be rejected")
	}
	t.Setenv("TINYCODE_DISABLE_SYNTAX_CHECK", "")
	t.Setenv("TINYCODE_REPO_MAP_CHARS", "0")
	if cfg, err := Load(); err != nil || cfg.TN.RepoMapChars != 0 {
		t.Fatalf("a zero map size (map off) must survive: (%d, %v)", cfg.TN.RepoMapChars, err)
	}
}

func TestLoadReplacesBrokenValues(t *testing.T) {
	path := isolateConfig(t)
	writeFile(t, path, `{
		"language_model": {"host": "", "port": -5, "name": ""},
		"tiny_code": {"context_limit": 0, "max_tokens": -1, "permission_mode": "yolo", "temperature": 9}
	}`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LM.Host != "127.0.0.1" || cfg.LM.Port != 1234 || cfg.LM.Name != "qwen/qwen3.5-9b" {
		t.Fatalf("broken model settings not repaired: %+v", cfg.LM)
	}
	if cfg.TN.ContextLimit != 26000 || cfg.TN.MaxTokens != 4096 || cfg.TN.Temperature != 0.6 {
		t.Fatalf("broken numeric settings not repaired: %+v", cfg.TN)
	}
	if cfg.TN.PermissionMode != "ask" {
		t.Fatalf("unknown permission mode = %q, want ask", cfg.TN.PermissionMode)
	}
}

func TestLoadHandlesNullSections(t *testing.T) {
	path := isolateConfig(t)
	writeFile(t, path, `{"language_model": null, "tiny_code": null}`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LM == nil || cfg.TN == nil || cfg.LM.Port != 1234 || cfg.TN.ContextLimit != 26000 {
		t.Fatalf("null sections not defaulted: %+v", cfg)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	isolateConfig(t)
	t.Setenv("TINYCODE_CONTEXT_LIMIT", "8000")
	t.Setenv("TINYCODE_STALL_TIMEOUT", "0")
	t.Setenv("TINYCODE_WORKSPACE", `C:\work`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TN.ContextLimit != 8000 || cfg.TN.Workspace != `C:\work` {
		t.Fatalf("env overrides ignored: %+v", cfg.TN)
	}
	if cfg.TN.StallTimeout != 0 {
		t.Fatalf("stall timeout 0 (disabled) must survive normalization, got %d", cfg.TN.StallTimeout)
	}

	t.Setenv("TINYCODE_MAX_TOKENS", "many")
	if _, err := Load(); err == nil {
		t.Fatal("expected an error for a non-numeric env value")
	}
}

func TestSaveNeverPersistsWorkspace(t *testing.T) {
	path := isolateConfig(t)
	cfg := Default()
	cfg.TN.Workspace = `C:\somewhere`
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"language_model"`) || strings.Contains(string(data), "somewhere") {
		t.Fatalf("unexpected saved config:\n%s", data)
	}
}
