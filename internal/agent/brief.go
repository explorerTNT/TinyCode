package agent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/explorerTNT/TinyCode/internal/config"
	"github.com/explorerTNT/TinyCode/internal/tools"
)

// maxInstructionsChars caps an instructions file: the system prompt travels
// with every request, and a small model follows a short brief better than a
// long one.
const maxInstructionsChars = 3000

// instructionFiles are project instruction files, most preferred first.
var instructionFiles = []string{"AGENTS.md", "TINYCODE.md", "CLAUDE.md"}

// brief is what the agent knows about the project beyond the conversation:
// the instruction files and the map of the code base.
type brief struct {
	project string
	global  string
	repoMap string
}

func loadBrief(workspace string, repoMapChars int) brief {
	var b brief
	for _, name := range instructionFiles {
		if b.project = readInstructions(filepath.Join(workspace, name)); b.project != "" {
			break
		}
	}
	if dir, err := config.ConfigDir(); err == nil {
		b.global = readInstructions(filepath.Join(dir, "AGENTS.md"))
	}
	b.repoMap = tools.RepoMapText(workspace, repoMapChars)
	return b
}

// readInstructions returns the text of an instructions file, or "" when it
// does not exist or is empty.
func readInstructions(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 64<<10 {
		return ""
	}
	text := strings.TrimSpace(strings.TrimPrefix(string(data), "\uFEFF"))
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if text == "" {
		return ""
	}
	if len(text) > maxInstructionsChars {
		text = truncateUTF8(text, maxInstructionsChars) +
			"\n[... the file is longer than 3000 characters and was cut here]"
	}
	return text
}

// prompt builds the system prompt on top of base: the agent's rules, then
// the project map and the instruction files.
func (b brief) prompt(base string) string {
	var sb strings.Builder
	sb.WriteString(base)
	if b.repoMap != "" {
		sb.WriteString("\n\nPROJECT MAP (layout and names of the project as of the start of the request):\n")
		sb.WriteString(b.repoMap)
	}
	if b.project != "" {
		sb.WriteString("\n\nPROJECT INSTRUCTIONS (from the project's instruction file; follow them):\n")
		sb.WriteString(b.project)
	}
	if b.global != "" {
		sb.WriteString("\n\nUSER PREFERENCES (from the user's global AGENTS.md; follow them):\n")
		sb.WriteString(b.global)
	}
	return sb.String()
}

func (a *Agent) systemPrompt() string { return a.brief.prompt(SYSTEM_PROMPT) }

func (a *Agent) planPrompt() string { return a.brief.prompt(PLAN_MODE_PROMPT) }

// refreshBrief reloads the instruction files and the project map and syncs
// the first message with them. It runs when a new request arrives, so an
// edit to the instruction file applies from the next request on, while the
// system prompt stays byte-identical within a turn and the server's prompt
// cache keeps hitting.
func (a *Agent) refreshBrief() {
	a.brief = loadBrief(a.workspace, a.config.TN.RepoMapChars)
	want := a.systemPrompt()
	if a.planMode {
		want = a.planPrompt()
	}
	a.msgMu.Lock()
	defer a.msgMu.Unlock()
	if len(a.messages) == 0 {
		a.messages = []Message{{Role: "system", Content: want}}
		return
	}
	if a.messages[0].Role == "system" {
		a.messages[0].Content = want
	}
}
