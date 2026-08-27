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

	"github.com/explorerTNT/TinyCode/internal/config"
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
)

var criticBashDangerous = []*regexp.Regexp{
	regexp.MustCompile(`rm\s+-rf\s+/`), regexp.MustCompile(`rm\s+-r\s+/`),
	regexp.MustCompile(`rm\s+-rf\s+~`), regexp.MustCompile(`rm\s+-r\s+~`),
	regexp.MustCompile(`format\s+[a-z]:`), regexp.MustCompile(`mkfs`),
	regexp.MustCompile(`:\(\)\{`), regexp.MustCompile(`dd\s+if=`),
	regexp.MustCompile(`curl\s+.*\|\s*(sh|bash)`), regexp.MustCompile(`wget\s+.*\|\s*(sh|bash)`),
	regexp.MustCompile(`del\s+/[fsq]`), regexp.MustCompile(`rmdir\s+/s`),
}

var agentBinaryExts = map[string]bool{
	".exe": true, ".dll": true, ".so": true, ".png": true, ".jpg": true,
	".jpeg": true, ".gif": true, ".pdf": true, ".zip": true, ".gz": true,
	".tar": true, ".bin": true, ".pyc": true, ".obj": true, ".o": true,
	".docx": true, ".xlsx": true, ".pptx": true, ".mp3": true, ".mp4": true,
	".avi": true,
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

	aborted atomic.Bool
	mu      sync.Mutex
	cancel  context.CancelFunc
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
		io.Println("--- AI asks: " + question + " ---")
		ans, err := io.Input("> ")
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
		client:    llm.NewClient(baseURL),
		io:        io,
		workspace: workspace,
	}

	a.permissions = permissions.New(permissions.ParseMode(cfg.TN.PermissionMode), io.Input)
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

// TokenCounts returns approximate prompt and completion token counts.
func (a *Agent) TokenCounts() (int, int) {
	prompt, completion := 0, 0
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
	a.messages = data.Messages
	a.planMode = data.PlanMode
	a.ctx.reset()
	a.ctx.pushAll(a.messages)
	a.io.Println(fmt.Sprintf("  [resumed session: %s (%d messages)]\n", data.Name, len(a.messages)))
	return true
}

func (a *Agent) envState() string {
	shell := "sh"
	if runtime.GOOS == "windows" {
		shell = "powershell"
	}
	lines := []string{
		"you are already inside the project directory",
		"use bare relative names like calc.py or src/calc.py, never a full C:\\... path",
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
	a.messages = append(a.messages, m)
	a.ctx.push(m)
}

func (a *Agent) clearMessages() {
	a.messages = nil
	a.ctx.reset()
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
			a.io.Println(fmt.Sprintf("  [path corrected: %q -> %q]", raw, basename))
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
				a.io.Println(fmt.Sprintf("  [path corrected: %q -> %q]", raw, exact[0]))
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
				return fmt.Sprintf("Error: command rejected by critic pass — matches a destructive pattern (%q). Rewrite it to be safe or use a narrower, non-destructive command.", pat.String())
			}
		}
		return ""
	}
	if name == writeToolsFile || name == editToolsFile {
		path := strArg(args, "path")
		if agentBinaryExts[strings.ToLower(filepath.Ext(path))] {
			return fmt.Sprintf("Error: '%s' looks like a binary file. Writing text to it will corrupt the file. Use a text-based format or a binary-safe tool.", path)
		}
	}
	return ""
}

func strArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

// executeTool runs a single tool call, returning the string result.
func (a *Agent) executeTool(name string, args map[string]any) string {
	fn, ok := a.toolMap[name]
	if !ok {
		return fmt.Sprintf("Error: Unknown tool '%s'", name)
	}

	if a.planMode && (name == writeToolsFile || name == editToolsFile || name == runBashTool) {
		return fmt.Sprintf("Error: `%s` is disabled in PLAN MODE and will keep failing. Do not call it again. Describe this step in the plan instead, and output the finished numbered plan as text - the code is written only after the plan is approved.", name)
	}

	if err := a.enforceWorkspace(args); err != "" {
		return err
	}

	if name == runBashTool && runtime.GOOS == "windows" {
		original := strArg(args, "command")
		cleaned := tools.SanitizeCmd(original)
		if cleaned != original {
			a.io.Println(fmt.Sprintf("  [command normalized: %s]", cleaned))
			args["command"] = cleaned
		}
		quoted := tools.QuoteExistingPaths(a.workspace, strArg(args, "command"))
		if quoted != strArg(args, "command") {
			a.io.Println(fmt.Sprintf("  [path quoted: %s]", quoted))
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

	if name == runBashTool {
		a.io.Println("  [команда выполняется…]")
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
