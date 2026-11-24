package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/explorerTNT/TinyCode/internal/i18n"
)

var (
	doneMarker  = regexp.MustCompile(`(?im)^\s*[*_` + "`" + `#\s]*task\s+complete[*_` + "`" + `.!\s]*$`)
	donePhrases = regexp.MustCompile(`(?i)(задача\s+выполнена|задание\s+выполнено|вс[ёе]\s+готово|вс[ёе]\s+сделано|task\s+is\s+complete|implementation\s+is\s+complete|all\s+steps\s+are\s+done)`)
	// RE2's \b only sees ASCII word characters, so next to Cyrillic it never
	// fires and a Russian "не готово" passed as done. The boundaries are
	// spelled out with Unicode classes instead.
	negation = regexp.MustCompile(`(?i)(^|[^\p{L}\p{N}])(не|нет|not|cannot|can't|fail\p{L}*|ошибк\p{L}*|error[s]?)([^\p{L}\p{N}]|$)`)
)

// Outcomes of an agent turn, for callers that measure the agent.
const (
	OutcomeRespond     = "respond"     // the model called respond
	OutcomeDone        = "done"        // the model announced the task is complete
	OutcomeRepeated    = "repeated"    // the model kept repeating itself
	OutcomeStuck       = "stuck"       // reasoning without any output
	OutcomeMaxRounds   = "max_rounds"  // the tool-round budget ran out
	OutcomeNoResponse  = "no_response" // the model server gave nothing usable
	OutcomeInterrupted = "interrupted" // the user stopped the turn
)

// TurnResult summarises the last processTurn call.
type TurnResult struct {
	Outcome    string
	Rounds     int
	ToolCalls  int
	ToolErrors int
	Tools      map[string]int // successful calls per tool
}

func looksDone(text string) bool {
	if text == "" {
		return false
	}
	if doneMarker.MatchString(text) {
		return true
	}
	tail := text
	if r := []rune(text); len(r) > 300 {
		tail = string(r[len(r)-300:])
	}
	return donePhrases.MatchString(tail) && !negation.MatchString(tail)
}

func shortArgs(args map[string]any, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 60
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		v := args[k]
		var s string
		switch x := v.(type) {
		case map[string]any, []any:
			b, _ := json.Marshal(x)
			s = string(b)
		default:
			s = fmt.Sprint(v)
		}
		if len(s) > 30 {
			s = s[:27] + "..."
		}
		parts = append(parts, k+"="+s)
	}
	result := strings.Join(parts, ", ")
	if len(result) > maxLen {
		result = result[:maxLen-3] + "..."
	}
	return result
}

func countPlanSteps(plan string) int {
	count := 0
	for _, line := range strings.Split(plan, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 1 && line[0] >= '0' && line[0] <= '9' && strings.ContainsRune(". ):-", rune(line[1])) {
			count++
		} else if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "•") {
			count++
		}
	}
	return count
}

// processTurn runs the tool-use loop for a single user request.
func (a *Agent) processTurn(maxRounds int, silent bool) {
	if maxRounds <= 0 {
		maxRounds = a.config.TN.MaxToolRounds
	}
	var recentTools []recentTool
	var recentErrors []string
	reasoningRounds := 0
	lastContentNorm := ""
	repeatCount := 0
	didWork := false
	textOnlyRounds := 0
	a.aborted.Store(false)

	res := &TurnResult{Outcome: OutcomeMaxRounds, Tools: map[string]int{}}
	defer func() { a.lastTurn.Store(res) }()

	for rnd := 0; rnd < maxRounds; rnd++ {
		res.Rounds = rnd + 1
		if a.aborted.Load() {
			res.Outcome = OutcomeInterrupted
			a.io.Println(i18n.T("agent.interrupted"))
			return
		}
		a.syncEnvState()
		a.tidyContext()
		isLast := rnd == maxRounds-1

		msg := a.callLLM(callOptions{silent: silent, useTools: !isLast})
		if a.aborted.Load() {
			res.Outcome = OutcomeInterrupted
			a.io.Println(i18n.T("agent.interrupted"))
			return
		}
		if msg == nil {
			msg = a.callLLM(callOptions{silent: silent, forcePrompt: "Continue with the next step."})
			if msg == nil {
				res.Outcome = OutcomeNoResponse
				a.io.Println(i18n.T("agent.no_response"))
				return
			}
		}

		if len(msg.ToolCalls) == 0 && msg.Content == "" {
			a.io.Println(i18n.T("agent.empty_reply", rnd+1, maxRounds))
			retry := a.callLLM(callOptions{
				silent: silent, useTools: !isLast, noThinking: true,
				forcePrompt: "Your previous response was empty. Output the next tool call now, without thinking.",
			})
			if retry != nil && (len(retry.ToolCalls) > 0 || retry.Content != "") {
				msg = retry
			}
		}

		tcList := msg.ToolCalls
		content := msg.Content

		if len(tcList) > 0 {
			reasoningRounds = 0
			lastContentNorm = ""
			repeatCount = 0
			textOnlyRounds = 0
			if len(tcList) > maxCallsPerRound {
				a.io.Println(i18n.T("agent.too_many_calls", len(tcList), maxCallsPerRound))
				// Every call needs a result: a call without one makes the
				// next request invalid for strict servers.
				for _, tc := range tcList[maxCallsPerRound:] {
					a.addMsg(Message{Role: "tool", ToolCallID: tc.ID, Content: skippedCallResult})
				}
				tcList = tcList[:maxCallsPerRound]
			}
			for _, tc := range tcList {
				name := tc.Name
				args := decodeArgs(tc.Arguments)

				if name == respondToolName {
					answer := strings.TrimSpace(strArg(args, "message"))
					if answer != "" {
						a.io.Println(fmt.Sprintf("  %s\n", answer))
					} else {
						a.io.Println("")
					}
					res.Outcome = OutcomeRespond
					return
				}

				a.io.Println(i18n.T("agent.tool_line", rnd+1, maxRounds, name, shortArgs(args, 60)))

				res.ToolCalls++
				var result string
				switch {
				case a.aborted.Load():
					// Pair the call with a result, but run nothing more after
					// the user pressed stop.
					result = "Error: interrupted by the user, the call was not executed."
				case msg.Truncated && (name == writeToolsFile || name == editToolsFile):
					result = a.truncatedWriteResult(args)
				case msg.Truncated && name == runBashTool:
					result = "Error: your tool call was cut off mid-generation, so the command is incomplete and was NOT run. Run a shorter command or split the work into smaller steps."
				default:
					result = a.executeTool(name, args)
				}

				if strings.HasPrefix(result, "Error:") {
					res.ToolErrors++
					a.io.Println(i18n.T("agent.tool_error", result))
					recentErrors = append(recentErrors, fmt.Sprintf("%s: %s", name, truncateStr(result, 200)))
					if len(recentErrors) > 8 {
						recentErrors = recentErrors[1:]
					}
				} else {
					res.Tools[name]++
					didWork = true
					preview := strings.ReplaceAll(truncateStr(result, 120), "\n", " ")
					a.io.Println(i18n.T("agent.tool_result", len(result), preview))
				}

				if len(result) > toolResultCap {
					result = truncateUTF8(result, toolResultCap) +
						"\n\n[truncated at 4000 chars. Use a narrower search or read a specific line range.]"
				}
				a.addMsg(Message{Role: "tool", ToolCallID: tc.ID, Content: result})

				argKey, err := json.Marshal(args)
				if err != nil {
					argKey = fmt.Append(argKey, args)
				}
				recentTools = append(recentTools, recentTool{name, string(argKey)})
				if len(recentTools) > 8 {
					recentTools = recentTools[1:]
				}
				same := 0
				sameTool := 0
				for _, t := range recentTools {
					if t.name == name {
						sameTool++
						if t.argKey == string(argKey) {
							same++
						}
					}
				}
				if same >= 3 || sameTool >= 6 {
					a.io.Println(i18n.T("agent.repeating_tool"))
					seen := dedupStrings(recentErrors)
					if len(seen) > 4 {
						seen = seen[len(seen)-4:]
					}
					errBlock := strings.Join(seen, "\n")
					a.addNudge("You have called `" + name + "` over and over without making progress. Errors you got:\n" + errBlock + "\nSTOP using `" + name + "`. You already have everything you need: write the code yourself from your own knowledge, then run it. Do not search the web for this task.")
					recentTools = nil
					recentErrors = nil
				}
			}
		} else if content != "" {
			if msg.Cut == "repetition" {
				res.Outcome = OutcomeRepeated
				a.io.Println(i18n.T("agent.finished_repeat"))
				return
			}
			cleanContent := stripXMLToolCalls(content)
			if cleanContent != "" {
				preview := strings.ReplaceAll(truncateStr(cleanContent, 300), "\n", " ")
				a.io.Println(i18n.T("agent.round", rnd+1, maxRounds, preview))
				msg.Content = cleanContent
				a.updateStoredContent(content, cleanContent)

				norm := strings.Join(strings.Fields(strings.ToLower(cleanContent)), " ")
				if norm != "" && norm == lastContentNorm {
					repeatCount++
				} else {
					repeatCount = 0
					lastContentNorm = norm
				}
				if repeatCount >= 2 {
					res.Outcome = OutcomeRepeated
					a.io.Println(i18n.T("agent.repeating_answer"))
					return
				}
			}

			if looksDone(cleanContent) {
				res.Outcome = OutcomeDone
				a.io.Println("")
				return
			}

			if didWork && textOnlyRounds >= 1 {
				res.Outcome = OutcomeDone
				a.io.Println(i18n.T("agent.text_twice"))
				return
			}

			textOnlyRounds++
			a.addNudge("If the task is fully done, reply with exactly TASK COMPLETE on its own line. Otherwise call the next tool now. Do not repeat your previous message.")
		} else {
			reasoningRounds++
			if reasoningRounds > 2 {
				res.Outcome = OutcomeStuck
				a.io.Println(i18n.T("agent.stuck_reasoning"))
				return
			}
		}
	}

	a.io.Println(i18n.T("agent.max_rounds"))
	msg := a.callLLM(callOptions{
		silent:      true,
		maxTokens:   512,
		forcePrompt: "You reached the tool-use limit. Stop using tools. Write a short summary of what was accomplished and what remains to be done.",
	})
	if msg != nil && msg.Content != "" {
		a.io.Println(msg.Content)
	}
	a.io.Println("")
}

// updateStoredContent syncs the cleaned assistant content back into history.
func (a *Agent) updateStoredContent(original, cleaned string) {
	a.msgMu.Lock()
	defer a.msgMu.Unlock()
	if n := len(a.messages); n > 0 {
		last := &a.messages[n-1]
		if last.Role == "assistant" && last.Content == original && len(last.ToolCalls) == 0 {
			last.Content = cleaned
		}
	}
}

func (a *Agent) truncatedWriteResult(args map[string]any) string {
	target := strArg(args, "path")
	if target == "" {
		target = "the file"
	}
	exists := fileExists(resolveOrJoin(a.workspace, target))
	if exists {
		return "Error: your reply was cut off - " + target + " was NOT modified. Do NOT rewrite the whole file. Call read_file to see the line numbers, then fix only the broken lines with edit_file(start_line=..., end_line=..., new_string=...)."
	}
	return "Error: your tool call was cut off mid-generation, so the content is incomplete and was NOT written. Write the file in smaller pieces: create it with the first part, then append the rest with edit_file."
}

func resolveOrJoin(ws, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(ws, p)
}

type recentTool struct {
	name   string
	argKey string
}

func dedupStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	start := 0
	if len(in) > 5 {
		start = len(in) - 5
	}
	for _, s := range in[start:] {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// skippedCallResult answers a tool call the agent dropped for exceeding
// maxCallsPerRound.
const skippedCallResult = "Error: skipped, you sent more than 3 tool calls at once. Send one call per step."

func truncateStr(s string, n int) string {
	return truncateUTF8(s, n)
}

// truncateUTF8 cuts s to at most n bytes without splitting a character.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// ---- plan mode ----

var writeTools = map[string]bool{writeToolsFile: true, editToolsFile: true, runBashTool: true}

func (a *Agent) processPlanTurn() {
	a.io.Println(i18n.T("plan.analyzing"))
	a.aborted.Store(false)
	blockedWrites := 0
	for rnd := 0; rnd < 3; rnd++ {
		if a.aborted.Load() {
			a.io.Println(i18n.T("agent.interrupted"))
			return
		}
		msg := a.callLLM(callOptions{silent: true, maxTokens: a.config.TN.PlanTokens})
		if a.aborted.Load() {
			a.io.Println(i18n.T("agent.interrupted"))
			return
		}
		if msg == nil {
			msg = a.callLLM(callOptions{silent: true, forcePrompt: "Output your plan now. No more analysis needed.", maxTokens: a.config.TN.PlanTokens})
			if msg == nil {
				a.io.Println(i18n.T("plan.no_response"))
				return
			}
		}

		tcList := msg.ToolCalls
		if len(tcList) == 0 && strings.TrimSpace(msg.Content) == "" {
			retry := a.callLLM(callOptions{silent: true, maxTokens: a.config.TN.PlanTokens, noThinking: true, forcePrompt: "Output your numbered plan now. Do not think, just write it."})
			if retry != nil && strings.TrimSpace(retry.Content) != "" {
				msg = retry
				tcList = msg.ToolCalls
			} else {
				continue
			}
		}

		if len(tcList) == 0 {
			a.showPlan(stripXMLToolCalls(msg.Content))
			return
		}

		if len(tcList) > maxCallsPerRound {
			for _, tc := range tcList[maxCallsPerRound:] {
				a.addMsg(Message{Role: "tool", ToolCallID: tc.ID, Content: skippedCallResult})
			}
			tcList = tcList[:maxCallsPerRound]
		}
		for _, tc := range tcList {
			name := tc.Name
			args := decodeArgs(tc.Arguments)

			if name == respondToolName {
				plan := stripXMLToolCalls(strArg(args, "message"))
				if strings.TrimSpace(plan) != "" {
					a.showPlan(plan)
					return
				}
				result := "Error: empty plan. Write the numbered plan as the message."
				a.io.Println(i18n.T("agent.tool_error", result))
				a.addMsg(Message{Role: "tool", ToolCallID: tc.ID, Content: result})
				continue
			}

			a.io.Println(i18n.T("agent.tool_line", rnd+1, 25, name, shortArgs(args, 60)))
			result := a.executeTool(name, args)

			if strings.HasPrefix(result, "Error:") {
				a.io.Println(i18n.T("agent.tool_error", result))
			}
			a.addMsg(Message{Role: "tool", ToolCallID: tc.ID, Content: result})

			if writeTools[name] {
				blockedWrites++
			}
			if blockedWrites >= 2 {
				blockedWrites = 0
				a.addNudge("You are in PLAN MODE. Writing files and running commands is impossible here - those tools stay disabled no matter how you call them. Do not attempt them again. Output the numbered plan as plain text right now; the code itself will be written after the plan is approved.")
			}
		}
	}

	msg := a.callLLM(callOptions{silent: true, forcePrompt: "Stop using tools. Output your numbered plan now.", maxTokens: a.config.TN.PlanTokens})
	plan := stripXMLToolCalls(msgContent(msg))
	if strings.TrimSpace(plan) == "" {
		msg = a.callLLM(callOptions{silent: true, maxTokens: a.config.TN.PlanTokens, noThinking: true, forcePrompt: "Stop using tools. Write your numbered plan now, without thinking."})
		plan = stripXMLToolCalls(msgContent(msg))
	}
	if strings.TrimSpace(plan) != "" {
		a.showPlan(plan)
	} else {
		a.lastPlan = ""
		a.io.Println(i18n.T("plan.no_plan"))
	}
}

func msgContent(m *Message) string {
	if m == nil {
		return ""
	}
	return m.Content
}

func (a *Agent) showPlan(plan string) {
	a.lastPlan = plan
	steps := countPlanSteps(plan)
	if steps > 0 {
		a.io.Println(i18n.T("plan.steps", steps, a.config.TN.MaxToolRounds))
	}
	a.io.Println("\n" + strings.Repeat("=", 50))
	a.io.Println(i18n.T("plan.title"))
	a.io.Println(strings.Repeat("=", 50))
	for _, line := range strings.Split(strings.TrimSpace(plan), "\n") {
		a.io.Println(fmt.Sprintf("  %s", line))
	}
	a.io.Println(strings.Repeat("=", 50))

	// Best-effort persistence; a failed plan save must not break the turn.
	_, _ = a.sessions.savePlan(plan)
}

func (a *Agent) handlePlanApproval() {
	if strings.TrimSpace(a.lastPlan) == "" {
		a.io.Println(i18n.T("plan.none"))
		a.planMode = false
		return
	}

	resp, err := a.io.Input(i18n.T("plan.approve_prompt"))
	if err != nil {
		resp = "n"
	}
	resp = strings.ToLower(strings.TrimSpace(resp))

	switch resp {
	case "y":
		plan := a.lastPlan
		a.clearMessages()
		a.addMsg(Message{Role: "system", Content: a.systemPrompt()})
		a.addMsg(Message{Role: "user", Content: "Execute this plan step by step:\n\n" + plan + "\n\nWork through each step. Read files before editing. Show progress as you go."})
		a.planMode = false
		a.io.Println("")
		a.processTurn(0, false)
		a.io.Println("")
	case "edit":
		a.io.Println(i18n.T("plan.edit"))
		a.planMode = true
	default:
		a.io.Println(i18n.T("plan.rejected"))
		a.planMode = false
		a.lastPlan = ""
	}
}

// ---- context compaction ----

func (a *Agent) compactMessages() {
	if len(a.messages) <= 2 {
		a.io.Println(i18n.T("compact.nothing"))
		return
	}

	var sb strings.Builder
	for _, m := range a.messages[1:] {
		sb.WriteString(fmt.Sprintf("%s: %s\n", m.Role, truncateStr(m.Content, 400)))
	}
	history := truncateStr(sb.String(), 6000)

	summaryPrompt := "Summarize the conversation below. Keep the key facts needed to continue the work: " +
		"the current task, files read/written, decisions made, and next steps. " +
		"Output ONLY the summary, no tool calls, max ~200 words.\n\n" +
		"--- CONVERSATION ---\n" + history

	msg := a.callLLM(callOptions{silent: true, maxTokens: 512, forcePrompt: summaryPrompt, useTools: false})
	summary := ""
	if msg != nil {
		summary = strings.TrimSpace(msg.Content)
	}
	if summary == "" && len(a.messages) > 0 {
		summary = truncateStr(a.messages[len(a.messages)-1].Content, 2000)
	}

	a.clearMessages()
	a.addMsg(Message{Role: "system", Content: a.systemPrompt()})
	a.addMsg(Message{Role: "user", Content: "Summary of the previous conversation:\n" + summary})
	a.io.Println(i18n.T("compact.done"))
}

// ---- command handling ----

// SlashCommands lists the built-in slash commands (for the TUI autocomplete
// hint and validation).
var SlashCommands = []string{
	"/help", "/session", "/sessions", "/clear", "/new", "/plan", "/compact", "/btw", "/exit", "/update",
}

// handleSessionCommand returns true if the input was a session command.
func (a *Agent) handleSessionCommand(cmd string) bool {
	parts := strings.SplitN(strings.TrimSpace(cmd), " ", 2)
	sub := parts[0]

	if sub == "/sessions" {
		a.printSessionList()
		return true
	}

	if sub == "/session" {
		rest := ""
		if len(parts) > 1 {
			rest = parts[1]
		}
		verb := ""
		verbRest := ""
		if i := strings.Index(rest, " "); i >= 0 {
			verb = rest[:i]
			verbRest = strings.TrimSpace(rest[i+1:])
		} else {
			verb = rest
		}
		verb = strings.ToLower(strings.TrimSpace(verb))

		switch verb {
		case "save":
			name := verbRest
			if name == "" {
				name = fmt.Sprintf("session_%d", time.Now().Unix())
			}
			path, err := a.sessions.save(name, a.messages, a.planMode, a.config.LM.Name, a.workspace)
			if err != nil {
				a.io.Println(i18n.T("session.save_failed", err))
			} else {
				a.io.Println(i18n.T("session.saved", filepath.Base(path)))
			}
			return true

		case "load", "resume":
			name := verbRest
			if name == "" {
				name = a.sessions.lastSession()
				if name == "" {
					a.io.Println(i18n.T("session.none_to_resume"))
					return true
				}
			}
			data, err := a.sessions.load(name)
			if err != nil {
				for _, s := range a.sessions.list() {
					if strings.Contains(s.Name, name) {
						data, err = a.sessions.load(s.Name)
						break
					}
				}
			}
			if err != nil || data == nil {
				a.io.Println(i18n.T("session.not_found", name))
				return true
			}
			a.msgMu.Lock()
			a.messages = data.Messages
			a.planMode = data.PlanMode
			a.msgMu.Unlock()
			a.io.Println(i18n.T("session.resumed", data.Name, len(a.messages)))
			return true

		case "list":
			a.printSessionList()
			return true

		default:
			a.io.Println(i18n.T("session.usage"))
			return true
		}
	}

	return false
}

func (a *Agent) printSessionList() {
	sessions := a.sessions.list()
	if len(sessions) == 0 {
		a.io.Println(i18n.T("session.list_empty"))
		return
	}
	a.io.Println(fmt.Sprintf("\n  %-20s %-30s %-6s %s",
		i18n.T("session.list_header_name"), i18n.T("session.list_header_model"),
		i18n.T("session.list_header_msgs"), i18n.T("session.list_header_time")))
	a.io.Println(fmt.Sprintf("  %s", strings.Repeat("─", 60)))
	for _, s := range sessions {
		a.io.Println(fmt.Sprintf("  %-20s %-30s %-6d %s", s.Name, s.Model, s.Messages, s.Time))
	}
	a.io.Println("")
}

// handleCommand handles built-in slash commands. Returns (handled, exit).
func (a *Agent) handleCommand(cmd string) (bool, bool) {
	c := strings.TrimSpace(cmd)

	switch {
	case c == "/exit":
		a.io.Println(i18n.T("agent.bye"))
		return true, true

	case c == "/help":
		a.io.Println(i18n.T("help.text"))
		return true, false

	case c == "/clear":
		a.clearMessages()
		a.addMsg(Message{Role: "system", Content: a.systemPrompt()})
		a.planMode = false
		a.lastPlan = ""
		a.io.Println(i18n.T("session.cleared"))
		return true, false

	case c == "/new":
		_, _ = a.sessions.save(fmt.Sprintf("session_%d", time.Now().Unix()), a.messages, a.planMode, a.config.LM.Name, a.workspace)
		a.clearMessages()
		a.addMsg(Message{Role: "system", Content: a.systemPrompt()})
		a.planMode = false
		a.lastPlan = ""
		a.io.Println(i18n.T("session.new"))
		return true, false

	case strings.HasPrefix(c, "/plan"):
		a.planMode = true
		desc := strings.TrimSpace(strings.TrimPrefix(c, "/plan"))
		a.clearMessages()
		a.addMsg(Message{Role: "system", Content: a.planPrompt()})
		if desc != "" {
			a.addMsg(Message{Role: "user", Content: desc})
		}
		a.io.Println("")
		return true, false

	case c == "/compact":
		a.compactMessages()
		return true, false

	case strings.HasPrefix(c, "/btw"):
		question := strings.TrimSpace(strings.TrimPrefix(c, "/btw"))
		if question == "" {
			a.io.Println(i18n.T("btw.usage"))
			return true, false
		}
		answer := a.RunSideQuestion(question)
		if answer == "" {
			a.io.Println(i18n.T("btw.no_answer"))
		} else {
			a.io.Println(i18n.T("btw.result", answer))
		}
		return true, false

	case strings.HasPrefix(c, "!"):
		cmdText := strings.TrimSpace(c[1:])
		result := a.executeTool(runBashTool, map[string]any{"command": cmdText, "timeout": float64(30)})
		a.io.Println(result)
		return true, false
	}

	return false, false
}

// Run starts the interactive session loop.
func (a *Agent) Run() {
	a.refreshBrief()

	a.io.Println(i18n.T("agent.banner_model", a.config.LM.Name))
	a.io.Println(i18n.T("agent.banner_workspace", a.workspace))
	a.io.Println(i18n.T("agent.banner_permissions", a.permissions.Mode().String()))
	a.io.Println(i18n.T("agent.banner_help"))

	for {
		line, err := a.io.Input(i18n.T("agent.input_prompt"))
		if err != nil {
			a.io.Println("\n" + i18n.T("agent.bye"))
			return
		}
		userInput := strings.TrimSpace(line)
		if userInput == "" {
			continue
		}
		switch strings.ToLower(userInput) {
		case "exit", "quit":
			a.io.Println(i18n.T("agent.bye"))
			return
		}

		if a.handleSessionCommand(userInput) {
			continue
		}

		handled, exit := a.handleCommand(userInput)
		if exit {
			return
		}
		if handled {
			if a.planMode && strings.HasPrefix(userInput, "/plan") {
				a.processPlanTurn()
				a.handlePlanApproval()
			}
			continue
		}

		if strings.HasPrefix(userInput, "/") {
			a.io.Println(i18n.T("agent.unknown_cmd", userInput))
			continue
		}

		a.refreshBrief()
		if a.planMode {
			a.addMsg(Message{Role: "user", Content: userInput})
			a.syncEnvState()
			a.processPlanTurn()
			a.handlePlanApproval()
		} else {
			a.addMsg(Message{Role: "user", Content: maybeGitHint(userInput)})
			a.syncEnvState()
			a.io.Println("")
			a.processTurn(0, false)
			a.io.Println("")
		}
	}
}

// RunOnce runs a single prompt non-interactively.
func (a *Agent) RunOnce(prompt string) {
	a.refreshBrief()
	a.addMsg(Message{Role: "user", Content: prompt})
	a.syncEnvState()
	a.io.Println("")
	a.processTurn(0, false)
	a.io.Println("")
}
