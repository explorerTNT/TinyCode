package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
)

// Default returns the configuration with default values.
func Default() *Config {
	workspace, _ := os.Getwd()
	return &Config{
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
			Temperature:       0.6,
			ActionTemperature: 0.1,
			PermissionMode:    "ask",
			Workspace:         workspace,
		},
	}
}

// Load returns the configuration from the config file (creating it with default
// values if it does not exist), then overlays non-empty environment variables
// on top of the loaded values.
func Load() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	var cfg *Config
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		cfg = Default()
		if err := cfg.Save(); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, fmt.Errorf("stat config %s: %w", path, err)
	} else {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}

		cfg = &Config{}
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
	}

	if err := applyEnv(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
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
			if field.IsNil() {
				field.Set(reflect.New(field.Type().Elem()))
			}
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

// Save writes the configuration to the config file, creating directories as needed.
func (c *Config) Save() error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}
