package tools

import (
	"strconv"
)

// Prop describes a single JSON-schema parameter for a tool.
type Prop struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

// Tool is a callable tool exposed to the model. Run receives the raw parsed
// JSON argument object and returns the string result fed back to the model.
type Tool struct {
	Name        string
	Description string
	Props       []Prop
	Run         func(args map[string]any) string
}

// Schema returns the JSON-schema object (type/properties/required) for the tool.
func (t *Tool) Schema() map[string]any {
	props := map[string]any{}
	var required []string
	for _, p := range t.Props {
		m := map[string]any{"type": p.Type, "description": p.Description}
		props[p.Name] = m
		if p.Required {
			required = append(required, p.Name)
		}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// strArg extracts a string argument, coercing the numbers/bools small models
// often emit for fields the schema already typed correctly.
func strArg(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		return ""
	}
}

// intArg extracts an integer argument with a default fallback.
func intArg(args map[string]any, key string, def int) int {
	v, ok := args[key]
	if !ok || v == nil {
		return def
	}
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case string:
		n, err := strconv.Atoi(x)
		if err != nil {
			return def
		}
		return n
	default:
		return def
	}
}
