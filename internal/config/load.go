package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

// Default returns the configuration with default values.
// Workspace is intentionally left empty: it defaults to the current working
// directory at runtime and is never persisted to the config file.
func Default() *Config {
	return &Config{
		Language: "en",
		LM: &LMConfig{
			Host: "127.0.0.1",
			Port: 1234,
			Name: "qwen/qwen3.5-9b",
		},
		TN: &TNConfig{
			MaxTokens:         4096,
			ServerContext:     32768,
			ContextLimit:      26000,
			MaxToolRounds:     50,
			PlanTokens:        1536,
			BtwMaxTokens:      1024,
			StallTimeout:      120,
			Temperature:       0.6,
			ActionTemperature: 0.1,
			PermissionMode:    "ask",
		},
	}
}

// Load returns the configuration from the config file (creating it with
// default values if it does not exist), then overlays non-empty environment
// variables on top. Fields missing from the file keep their defaults, so a
// config written by an older version stays usable.
func Load() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	cfg := Default()
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := cfg.Save(); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, fmt.Errorf("read config %s: %w", path, err)
	default:
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
		if cfg.LM == nil {
			cfg.LM = Default().LM
		}
		if cfg.TN == nil {
			cfg.TN = Default().TN
		}
	}

	// The workspace is resolved at runtime from the flag, the environment or
	// the current directory, never from the file.
	cfg.TN.Workspace = ""

	if err := applyEnv(cfg); err != nil {
		return nil, err
	}
	cfg.normalize()
	return cfg, nil
}

// normalize replaces values that would break the agent (missing, empty or
// non-positive) with defaults.
func (c *Config) normalize() {
	def := Default()
	if c.Language == "" {
		c.Language = def.Language
	}
	if c.LM.Host == "" {
		c.LM.Host = def.LM.Host
	}
	if c.LM.Port <= 0 || c.LM.Port > 65535 {
		c.LM.Port = def.LM.Port
	}
	if c.LM.Name == "" {
		c.LM.Name = def.LM.Name
	}
	tn, dt := c.TN, def.TN
	for _, f := range []struct {
		dst *int
		def int
	}{
		{&tn.MaxTokens, dt.MaxTokens},
		{&tn.ServerContext, dt.ServerContext},
		{&tn.ContextLimit, dt.ContextLimit},
		{&tn.MaxToolRounds, dt.MaxToolRounds},
		{&tn.PlanTokens, dt.PlanTokens},
		{&tn.BtwMaxTokens, dt.BtwMaxTokens},
	} {
		if *f.dst <= 0 {
			*f.dst = f.def
		}
	}
	// A zero stall timeout means "disabled"; anything below makes no sense.
	if tn.StallTimeout < 0 {
		tn.StallTimeout = dt.StallTimeout
	}
	for _, f := range []struct {
		dst *float64
		def float64
	}{
		{&tn.Temperature, dt.Temperature},
		{&tn.ActionTemperature, dt.ActionTemperature},
	} {
		if *f.dst < 0 || *f.dst > 2 {
			*f.dst = f.def
		}
	}
	switch strings.ToLower(strings.TrimSpace(tn.PermissionMode)) {
	case "auto", "ask", "deny":
		tn.PermissionMode = strings.ToLower(strings.TrimSpace(tn.PermissionMode))
	default:
		tn.PermissionMode = dt.PermissionMode
	}
}

// applyEnv walks the config struct and overrides each field tagged with `env:`
// when the corresponding environment variable is set and non-empty.
func applyEnv(v any) error {
	return applyEnvValue(reflect.ValueOf(v))
}

func applyEnvValue(v reflect.Value) error {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}

	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		// Recurse into nested structs.
		if field.Kind() == reflect.Struct {
			if err := applyEnvValue(field); err != nil {
				return err
			}
			continue
		}
		if field.Kind() == reflect.Pointer && field.Type().Elem().Kind() == reflect.Struct {
			if err := applyEnvValue(field); err != nil {
				return err
			}
			continue
		}

		envKey := fieldType.Tag.Get("env")
		if envKey == "" {
			continue
		}

		raw, ok := os.LookupEnv(envKey)
		if !ok || raw == "" {
			continue
		}

		if err := setField(field, raw); err != nil {
			return fmt.Errorf("env %s: %w", envKey, err)
		}
	}
	return nil
}

func setField(field reflect.Value, raw string) error {
	switch field.Kind() {
	case reflect.String:
		field.SetString(raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetInt(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(raw, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetFloat(f)
	default:
		return fmt.Errorf("unsupported type %s", field.Kind())
	}
	return nil
}

// Save writes the configuration to the config file, creating directories as
// needed. Workspace is never persisted: it is always resolved at runtime from
// the current directory, CLI flags, or environment variables.
func (c *Config) Save() error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	saved := *c
	if c.TN != nil {
		tnCopy := *c.TN
		tnCopy.Workspace = ""
		saved.TN = &tnCopy
	}

	data, err := json.MarshalIndent(&saved, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}
