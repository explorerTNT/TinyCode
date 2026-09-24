package config

// Language Model Config
type LMConfig struct {
	// Language Model provider host
	Host string `json:"host" env:"TINYCODE_HOST"`
	// Language Model provider port
	Port int `json:"port" env:"TINYCODE_PORT"`
	// Language Model provider model name
	Name string `json:"name" env:"TINYCODE_NAME"`
}

// Tiny Code Config
type TNConfig struct {
	MaxTokens         int     `json:"max_tokens"         env:"TINYCODE_MAX_TOKENS"`
	ServerContext     int     `json:"server_context"     env:"TINYCODE_SERVER_CONTEXT"`
	ContextLimit      int     `json:"context_limit"      env:"TINYCODE_CONTEXT_LIMIT"`
	MaxToolRounds     int     `json:"max_tool_rounds"    env:"TINYCODE_MAX_TOOL_ROUNDS"`
	PlanTokens        int     `json:"plan_tokens"        env:"TINYCODE_PLAN_TOKENS"`
	BtwMaxTokens      int     `json:"btw_max_tokens"     env:"TINYCODE_BTW_MAX_TOKENS"`
	Temperature       float64 `json:"temperature"        env:"TINYCODE_TEMPERATURE"`
	ActionTemperature float64 `json:"action_temperature" env:"TINYCODE_ACTION_TEMPERATURE"`
	PermissionMode    string  `json:"permission_mode"    env:"TINYCODE_PERMISSION_MODE"`
	Workspace         string  `json:"workspace"          env:"TINYCODE_WORKSPACE"`
}

type Config struct {
	// Interface language: "en" or "ru".
	Language string `json:"language" env:"TINYCODE_LANG"`
	// Language Model Config
	LM *LMConfig `json:"language_model"`
	// Tiny Code Config
	TN *TNConfig `json:"tiny_code"`
	// Version is the running build version, injected by main (not persisted).
	Version string `json:"-"`
	// UpdateCheck enables the startup update notice in the TUI (injected by main).
	UpdateCheck bool `json:"-"`
}
