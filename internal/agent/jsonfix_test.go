package agent

import "testing"

func TestParseToolArgs(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		want      map[string]any
		truncated bool
	}{
		{"strict", `{"path": "a.py", "n": 2}`, map[string]any{"path": "a.py", "n": float64(2)}, false},
		{"trailing comma", `{"path": "a.py",}`, map[string]any{"path": "a.py"}, false},
		{"single quotes", `{'path': 'C:\Users\x.py'}`, map[string]any{"path": `C:\Users\x.py`}, false},
		{"raw line break", "{\"content\": \"def f():\\n    pass\"}", map[string]any{"content": "def f():\n    pass"}, false},
		{"existing escape kept", `{"content": "a\nb"}`, map[string]any{"content": "a\nb"}, false},
		{"apostrophe is not a quote", `{"text": "it's fine"}`, map[string]any{"text": "it's fine"}, false},
		// The repair had to complete the text: the generation was cut off.
		{"cut inside a string", `{"path": "a.py", "content": "def foo`, map[string]any{"path": "a.py", "content": "def foo"}, true},
		{"missing brace", `{"path": "a.py"`, map[string]any{"path": "a.py"}, true},
		{"cut before the value", `{"path": "a.py", "content": `, map[string]any{"path": "a.py", "content": ""}, true},
		{"cut after a comma", `{"a": 1,`, map[string]any{"a": float64(1)}, true},
	}
	for _, c := range cases {
		args, truncated, ok := parseToolArgs(c.in)
		if !ok {
			t.Errorf("%s: not parsed", c.name)
			continue
		}
		if truncated != c.truncated {
			t.Errorf("%s: truncated = %v, want %v", c.name, truncated, c.truncated)
		}
		for k, want := range c.want {
			if args[k] != want {
				t.Errorf("%s: %s = %#v, want %#v", c.name, k, args[k], want)
			}
		}
	}
}

func TestParseToolArgsRejects(t *testing.T) {
	for _, in := range []string{"", "   ", "not json", "[]", "null", `"string"`, "42", "{broken"} {
		if _, _, ok := parseToolArgs(in); ok {
			t.Errorf("parseToolArgs(%q) accepted", in)
		}
	}
}

// A failed strict parse must not leak half-decoded values into the repaired
// result.
func TestParseToolArgsRepairsWithoutLeftovers(t *testing.T) {
	args, truncated, ok := parseToolArgs(`{"path": "a.py", "start_line": 5`)
	if !ok || !truncated || len(args) != 2 || args["path"] != "a.py" || args["start_line"] != float64(5) {
		t.Fatalf("parseToolArgs = (%#v, %v, %v)", args, truncated, ok)
	}
}
