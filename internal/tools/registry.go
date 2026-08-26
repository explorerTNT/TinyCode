package tools

// AskFunc asks the user a question and returns the raw answer.
type AskFunc func(question string) string

// NoteFunc is the reporter used for side notes (e.g. edit_file diagnostics).
type NoteFunc func(format string, args ...any)

// BuildTools constructs the nine agent tools bound to the given workspace.
func BuildTools(workspace string, ask AskFunc, note NoteFunc) []*Tool {
	SetNote(note)

	return []*Tool{
		{
			Name:        "read_file",
			Description: "Read a file from the filesystem and return its contents",
			Props: []Prop{
				{Name: "path", Type: "string", Description: "file path", Required: true},
				{Name: "offset", Type: "integer", Description: "start line (1)"},
				{Name: "limit", Type: "integer", Description: "max lines"},
			},
			Run: func(args map[string]any) string {
				return ReadFile(workspace, strArg(args, "path"), intArg(args, "offset", 1), intArg(args, "limit", 2000))
			},
		},
		{
			Name:        "write_file",
			Description: "Create a new file or overwrite an existing file with the given content",
			Props: []Prop{
				{Name: "path", Type: "string", Description: "file path", Required: true},
				{Name: "content", Type: "string", Description: "file content", Required: true},
			},
			Run: func(args map[string]any) string {
				return WriteFile(workspace, strArg(args, "path"), strArg(args, "content"))
			},
		},
		{
			Name:        "edit_file",
			Description: "Replace text in a file. Prefer start_line/end_line - read_file shows those numbers, so they need no guessing and always hit the right place",
			Props: []Prop{
				{Name: "path", Type: "string", Description: "file path", Required: true},
				{Name: "old_string", Type: "string", Description: "fallback: exact text to find, if not using line numbers"},
				{Name: "new_string", Type: "string", Description: "replacement text"},
				{Name: "start_line", Type: "integer", Description: "first line to replace (from read_file) - preferred"},
				{Name: "end_line", Type: "integer", Description: "last line to replace, inclusive"},
			},
			Run: func(args map[string]any) string {
				return EditFile(workspace,
					strArg(args, "path"),
					strArg(args, "old_string"),
					strArg(args, "new_string"),
					args["start_line"], args["end_line"])
			},
		},
		{
			Name:        "search_files",
			Description: "Search file contents using a regular expression pattern (grep-like)",
			Props: []Prop{
				{Name: "pattern", Type: "string", Description: "regex to search", Required: true},
				{Name: "path", Type: "string", Description: "search dir"},
				{Name: "include", Type: "string", Description: "glob filter"},
			},
			Run: func(args map[string]any) string {
				return SearchFiles(workspace, strArg(args, "pattern"), strArg(args, "path"), strArg(args, "include"))
			},
		},
		{
			Name:        "list_files",
			Description: "List files matching a glob pattern. Returns files sorted by modification time",
			Props: []Prop{
				{Name: "pattern", Type: "string", Description: "glob pattern"},
				{Name: "path", Type: "string", Description: "search dir"},
			},
			Run: func(args map[string]any) string {
				pattern := strArg(args, "pattern")
				if pattern == "" {
					pattern = "*"
				}
				path := strArg(args, "path")
				if path == "" {
					path = "."
				}
				return ListFiles(workspace, pattern, path)
			},
		},
		{
			Name:        "run_bash",
			Description: "Run a shell command on the user's machine and return the output",
			Props: []Prop{
				{Name: "command", Type: "string", Description: "shell command", Required: true},
				{Name: "timeout", Type: "integer", Description: "max seconds"},
			},
			Run: func(args map[string]any) string {
				return RunBash(workspace, strArg(args, "command"), intArg(args, "timeout", 30))
			},
		},
		{
			Name:        "ask_user",
			Description: "Ask the user a question and get their text response",
			Props: []Prop{
				{Name: "question", Type: "string", Description: "the question to ask", Required: true},
			},
			Run: func(args map[string]any) string {
				return AskUser(strArg(args, "question"), ask)
			},
		},
		{
			Name:        "web_search",
			Description: "Search the web for information. Use this to find answers, docs, or current info",
			Props: []Prop{
				{Name: "query", Type: "string", Description: "search terms", Required: true},
				{Name: "max_results", Type: "integer", Description: "max results"},
			},
			Run: func(args map[string]any) string {
				return WebSearch(strArg(args, "query"), intArg(args, "max_results", 5))
			},
		},
		{
			Name:        "web_fetch",
			Description: "Fetch and read content from a URL. Use this to read docs, articles, web pages",
			Props: []Prop{
				{Name: "url", Type: "string", Description: "full URL", Required: true},
				{Name: "timeout", Type: "integer", Description: "seconds"},
			},
			Run: func(args map[string]any) string {
				return WebFetch(strArg(args, "url"), intArg(args, "timeout", 15))
			},
		},
	}
}
