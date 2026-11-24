package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/explorerTNT/TinyCode/internal/config"
	"github.com/explorerTNT/TinyCode/internal/i18n"
	"github.com/explorerTNT/TinyCode/internal/llm"
	"github.com/explorerTNT/TinyCode/internal/permissions"
	"github.com/explorerTNT/TinyCode/internal/tools"
	"github.com/sashabaranov/go-openai"
)

const (
	respondToolName     = "respond"
	toolResultCap       = 4000
	keepFullToolResults = 5
	maxCallsPerRound    = 3
	writeToolsFile      = "write_file"
	editToolsFile       = "edit_file"
	runBashTool         = "run_bash"
	webSearchTool       = "web_search"
	webFetchTool        = "web_fetch"
)

// Patterns of catastrophic, irreversible commands, rejected in every
// permission mode. This is a backstop below the permission prompt, not a
// sandbox: an ordinary delete or rewrite still goes through the prompt.
const (
	// A drive or filesystem root as a standalone argument of a PowerShell
	// command: psRoot matches the root itself, psRootArg a root that ends
	// the command.
	psRoot    = `[\s=(]["']?(?:[a-z]:[\\/]?\??|\\|/)["']?`
	psRootArg = psRoot + `\s*(?:$|[|;&)])`
	psRecurs  = `\s-(?:r|rec|recur|recurse)\b`
)

var criticBashDangerous = []*regexp.Regexp{
	// POSIX: wiping a root or home directory, formatting, fork bombs,
	// downloading and executing a script.
	regexp.MustCompile(`rm\s+(?:-[a-z]*r[a-z]*|--recursive|--force|\s)+\s*(?:/|~)(?:\s|\*|/\*|$)`),
	regexp.MustCompile(`format\s+[a-z]:`), regexp.MustCompile(`mkfs`),
	regexp.MustCompile(`:\(\)\{`), regexp.MustCompile(`dd\s+if=`),
	regexp.MustCompile(`curl\s+.*\|\s*(?:sh|bash)`), regexp.MustCompile(`wget\s+.*\|\s*(?:sh|bash)`),
	regexp.MustCompile(`del\s+/[fsq]`), regexp.MustCompile(`rmdir\s+/s`),
	// Windows: the recursive delete of a root, with the switch before or
	// after the path, and the cmd.exe spellings.
	regexp.MustCompile(`\b(?:remove-item|rm|ri|rmdir|rd)\b[^|;&\n]*` + psRecurs + `[^|;&\n]*` + psRootArg),
	regexp.MustCompile(`\b(?:remove-item|rm|ri|rmdir|rd)\b[^|;&\n]*` + psRoot + `[^|;&\n]*` + psRecurs),
	regexp.MustCompile(`\brm(?:\s+-[a-z]+)*` + psRootArg),
	regexp.MustCompile(`\b(?:rd|rmdir)\b[^|;&\n]*\s/s\b`),
	regexp.MustCompile(`\b(?:del|erase)\b[^|;&\n]*\s/[a-z]*s`),
	// Windows: disks, boot configuration, the registry, Defender, and the
	// download-then-execute chains.
	regexp.MustCompile(`\b(?:format-volume|clear-disk|initialize-disk|remove-partition|diskpart|bcdedit|stop-computer|restart-computer)\b`),
	regexp.MustCompile(`\breg\s+(?:delete|add)\b[^|;&\n]*hk(?:lm|cu)\\system`),
	regexp.MustCompile(`\|\s*(?:iex|invoke-expression)\b`),
	regexp.MustCompile(`\b(?:iex|invoke-expression)\b[^\n]*\b(?:irm|iwr|invoke-restmethod|invoke-webrequest)\b`),
	regexp.MustCompile(`\b(?:downloadstring|downloadfile|downloaddata|downloadchunk)\b`),
	regexp.MustCompile(`\b(?:set-mppreference|add-mppreference)\b[^\n|;&]*(?:disablerealtime|exclusion)`),
	regexp.MustCompile(`\b(?:vssadmin|wbadmin)\s+delete\b`),
}

// Agent is the TinyCode coding agent.
type Agent struct {
	config      *config.Config
	permissions *permissions.Service
	client      *openai.Client
	io          IO
	workspace   string

	tools       []*tools.Tool
	toolMap     map[string]*tools.Tool
	toolSchemas []openai.Tool

	messages []Message
	llmCalls int
	ctx      *ContextManager

	planMode bool
	lastPlan string
	sessions *SessionManager

	usageMu         sync.Mutex
	usagePrompt     int
	usageCompletion int
	usageCalls      int
	lastTurn        atomic.Pointer[TurnResult]
	aborted         atomic.Bool
	mu              sync.Mutex
	msgMu           sync.RWMutex
	cancel          context.CancelFunc
}

// New builds an agent bound to the config and IO.
func New(cfg *config.Config, io IO) (*Agent, error) {
	workspace := cfg.TN.Workspace
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}

	baseURL := fmt.Sprintf("http://%s:%d/v1", cfg.LM.Host, cfg.LM.Port)

	ask := func(question string) string {
		io.Println("")
		io.Println(i18n.T("agent.asks", question))
		ans, err := io.Input(i18n.T("agent.asks_prompt"))
		if err != nil {
			return "User cancelled the input"
		}
		return strings.TrimSpace(ans)
	}
	note := func(format string, args ...any) {
		io.Println(fmt.Sprintf(format, args...))
	}

	a := &Agent{
		config:    cfg,
		client:    llm.NewClient(baseURL, time.Duration(cfg.TN.StallTimeout)*time.Second),
		io:        io,
		workspace: workspace,
	}

	a.permissions = permissions.New(permissions.ParseMode(cfg.TN.PermissionMode), workspace, io.Input)
	a.tools = tools.BuildTools(workspace, ask, note)
	a.toolMap = make(map[string]*tools.Tool, len(a.tools))
	for _, t := range a.tools {
		a.toolMap[t.Name] = t
	}
	a.toolSchemas = buildToolSchemas(a.tools)
	a.ctx = newContextManager(cfg.TN.ContextLimit)

	sm, err := newSessionManager(workspace)
	if err != nil {
		return nil, err
	}
	a.sessions = sm

	return a, nil
}

// Workspace returns the resolved workspace path.
func (a *Agent) Workspace() string { return a.workspace }

// IO returns the agent's output/input.
func (a *Agent) IO() IO { return a.io }

// PermissionMode returns the current permission mode.
func (a *Agent) PermissionMode() string { return a.permissions.Mode().String() }

// ModelName returns the configured model name.
func (a *Agent) ModelName() string { return a.config.LM.Name }

// LastTurn returns the outcome of the most recent turn.
func (a *Agent) LastTurn() TurnResult {
	if res := a.lastTurn.Load(); res != nil {
		return *res
	}
	return TurnResult{}
}

// TokenCounts returns the prompt and completion tokens the model server
// reported so far. Without server usage it falls back to an estimate built
// from the message history.
func (a *Agent) TokenCounts() (int, int) {
	a.usageMu.Lock()
	prompt, completion, calls := a.usagePrompt, a.usageCompletion, a.usageCalls
	a.usageMu.Unlock()
	if calls > 0 {
		return prompt, completion
	}

	prompt, completion = 0, 0
	a.msgMu.RLock()
	defer a.msgMu.RUnlock()
	for _, m := range a.messages {
		n := countMessageTokens(m)
		switch m.Role {
		case "user", "system":
			prompt += n
		case "assistant":
			completion += n
		}
	}
	return prompt, completion
}

func (a *Agent) addUsage(prompt, completion int) {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	a.usagePrompt += prompt
	a.usageCompletion += completion
	a.usageCalls++
}

// stallSeconds is the configured silence limit of a model stream.
func (a *Agent) stallSeconds() int {
	if a.config.TN.StallTimeout > 0 {
		return a.config.TN.StallTimeout
	}
	return 0
}

// Abort interrupts the running generation/tool loop.
func (a *Agent) Abort() {
	a.aborted.Store(true)
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	a.mu.Unlock()
}

// ResumeSession restores a named session (or the most recent one when name is
// empty). Returns false when no matching session exists.
func (a *Agent) ResumeSession(name string) bool {
	if name == "" {
		name = a.sessions.lastSession()
	}
	if name == "" {
		return false
	}
	data, err := a.sessions.load(name)
	if err != nil {
		return false
	}
	a.msgMu.Lock()
	a.messages = data.Messages
	a.planMode = data.PlanMode
	a.msgMu.Unlock()
	a.io.Println(i18n.T("session.resumed", data.Name, len(a.messages)))
	return true
}

func (a *Agent) envState() string {
	shell := "sh"
	root := "/home/user/..."
	if runtime.GOOS == "windows" {
		shell = "powershell"
		root = "C:\\..."
	}
	lines := []string{
		"you are already inside the project directory",
		"use bare relative names like calc.py or src/calc.py, never a full " + root + " path",
		"os: " + runtime.GOOS + " | shell: " + shell,
	}

	listing := "(empty)"
	if entries, err := os.ReadDir(a.workspace); err == nil {
		var names []string
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) > 0 {
			n := len(names)
			if n > 25 {
				n = 25
			}
			listing = strings.Join(names[:n], ", ")
			if len(names) > 25 {
				listing += fmt.Sprintf(", ... (+%d more)", len(names)-25)
			}
		}
	}
	lines = append(lines, "files here: "+listing)
	return "ENVIRONMENT STATE\n" + strings.Join(lines, "\n")
}

func (a *Agent) addMsg(m Message) {
	a.msgMu.Lock()
	defer a.msgMu.Unlock()
	a.messages = append(a.messages, m)
}

// addNudge appends a user-role message written by the agent itself to steer
// the model. It is marked so context trimming can tell it from a request.
func (a *Agent) addNudge(content string) {
	a.addMsg(Message{Role: "user", Content: content, Nudge: true})
}

func (a *Agent) clearMessages() {
	a.msgMu.Lock()
	defer a.msgMu.Unlock()
	a.messages = nil
}

// snapshotMessages returns a deep copy of the current message history without
// holding the lock across any further work.
func (a *Agent) snapshotMessages() []Message {
	a.msgMu.RLock()
	defer a.msgMu.RUnlock()
	out := make([]Message, len(a.messages))
	for i, m := range a.messages {
		out[i] = m
		if len(m.ToolCalls) > 0 {
			out[i].ToolCalls = append([]ToolCall{}, m.ToolCalls...)
		}
	}
	return out
}

// maybeGitHint injects a concrete git command when the user asks about history.
func maybeGitHint(userInput string) string {
	low := strings.ToLower(userInput)
	keywords := []string{"commit", "коммит", "что было сделано", "что сделали", "изменения в"}
	found := false
	for _, k := range keywords {
		if strings.Contains(low, k) {
			found = true
			break
		}
	}
	if !found {
		return userInput
	}
	return userInput +
		"\n\nNote: this question is about git history. Use run_bash, e.g. " +
		"`git log --oneline -30` then `git show <hash>`. " +
		"Commit messages are NOT in the files; do not use search_files for them."
}

// ---- workspace enforcement ----

func (a *Agent) enforceWorkspace(args map[string]any) string {
	if a.workspace == "" {
		return ""
	}
	raw, ok := args["path"].(string)
	if !ok || raw == "" {
		return ""
	}

	if p, ok := resolveUnderWorkspace(a.workspace, raw); ok {
		_ = p
		return ""
	}

	basename := filepath.Base(strings.ReplaceAll(raw, "\\", "/"))
	if basename != "" && basename != "." && basename != ".." {
		repaired := filepath.Join(a.workspace, basename)
		if insideWorkspace(a.workspace, repaired) && fileExists(repaired) {
			args["path"] = basename
			a.io.Println(i18n.T("agent.path_corrected", raw, basename))
			return ""
		}
	}

	candidates := tools.SuggestFiles(a.workspace, raw, 5)
	if len(candidates) > 0 {
		if basename != "" && basename != "." && basename != ".." {
			var exact []string
			for _, c := range candidates {
				if strings.EqualFold(filepath.Base(c), basename) {
					exact = append(exact, c)
				}
			}
			if len(exact) == 1 {
				args["path"] = exact[0]
				a.io.Println(i18n.T("agent.path_corrected", raw, exact[0]))
				return ""
			}
		}
		return fmt.Sprintf("Error: '%s' is outside the project directory. Did you mean:\n  %s",
			raw, strings.Join(candidates, "\n  "))
	}

	return fmt.Sprintf("Error: '%s' is outside the project directory. Use a bare relative name like calc.py instead.", raw)
}

func resolveUnderWorkspace(ws, raw string) (string, bool) {
	var abs string
	if filepath.IsAbs(raw) {
		abs = filepath.Clean(raw)
	} else {
		abs = filepath.Clean(filepath.Join(ws, raw))
	}
	return abs, insideWorkspace(ws, abs)
}

func insideWorkspace(ws, p string) bool {
	rel, err := filepath.Rel(ws, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (a *Agent) criticCheck(name string, args map[string]any) string {
	if name == runBashTool {
		cmd := strings.ToLower(strArg(args, "command"))
		for _, pat := range criticBashDangerous {
			if pat.MatchString(cmd) {
				return fmt.Sprintf("Error: command rejected by critic pass: it matches a destructive pattern (%q). Rewrite it to be safe or use a narrower, non-destructive command.", pat.String())
			}
		}
		return ""
	}
	if name == writeToolsFile || name == editToolsFile {
		path := strArg(args, "path")
		if tools.IsBinaryExt(path) {
			return fmt.Sprintf("Error: '%s' looks like a binary file. Writing text to it will corrupt the file. Use a text-based format or a binary-safe tool.", path)
		}
	}
	return ""
}

func strArg(args map[string]any, key string) string {
	return tools.StrArg(args, key)
}

// executeTool runs a single tool call, returning the string result.
func (a *Agent) executeTool(name string, args map[string]any) string {
	fn, ok := a.toolMap[name]
	if !ok {
		return fmt.Sprintf("Error: Unknown tool '%s'", name)
	}

	if a.planMode && writeTools[name] {
		return fmt.Sprintf("Error: `%s` is disabled in PLAN MODE and will keep failing. Do not call it again. Describe this step in the plan instead, and output the finished numbered plan as text - the code is written only after the plan is approved.", name)
	}

	if err := a.enforceWorkspace(args); err != "" {
		return err
	}

	if name == runBashTool && runtime.GOOS == "windows" {
		original := strArg(args, "command")
		cleaned := tools.SanitizeCmd(original)
		if cleaned != original {
			a.io.Println(i18n.T("agent.command_normalized", cleaned))
			args["command"] = cleaned
		}
		quoted := tools.QuoteExistingPaths(a.workspace, strArg(args, "command"))
		if quoted != strArg(args, "command") {
			a.io.Println(i18n.T("agent.path_quoted", quoted))
			args["command"] = quoted
		}
	}

	if critic := a.criticCheck(name, args); critic != "" {
		return critic
	}

	if name == runBashTool {
		if !a.permissions.CheckBash(strArg(args, "command")) {
			return "Error: Command rejected by user"
		}
	}

	if name == writeToolsFile || name == editToolsFile {
		if !a.permissions.CheckWrite(strArg(args, "path")) {
			return "Error: Write rejected by user"
		}
	}

	if name == webSearchTool || name == webFetchTool {
		target := strArg(args, "url")
		if name == webSearchTool {
			target = strArg(args, "query")
		}
		if !a.permissions.CheckWeb(target) {
			return "Error: Web request rejected by user"
		}
	}

	if name == runBashTool {
		a.io.Println(i18n.T("agent.running_cmd"))
	}

	return a.runTool(fn, args)
}

func (a *Agent) runTool(fn *tools.Tool, args map[string]any) (result string) {
	defer func() {
		if r := recover(); r != nil {
			result = fmt.Sprintf("Error executing %s: %v", fn.Name, r)
		}
	}()
	return fn.Run(args)
}
