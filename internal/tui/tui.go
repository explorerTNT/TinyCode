// Package tui implements the bubbletea terminal UI.
package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/explorerTNT/TinyCode/internal/agent"
	"github.com/explorerTNT/TinyCode/internal/config"
	"github.com/explorerTNT/TinyCode/internal/tools"
)

const (
	maxLogLines   = 2000
	maxTreeNodes  = 600
	maxTreeDepth  = 6
	inputSentinel = "\x00tinycode-quit\x00"
)

// ---------- styling ----------

var (
	styleTitle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	stylePrompt  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	styleHint    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleLogBrd  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("6"))
	styleSideBrd = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("6"))
	styleInpBrd  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("3"))
	styleTree    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	styleTreeSel = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6"))
	styleStatus  = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	styleCode    = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
)

// highlightCode syntax-highlights a code block, returning one string per line.
func highlightCode(code, lang string) []string {
	plain := strings.Split(code, "\n")
	if lang == "" || lang == "text" {
		lang = "python"
	}
	if strings.Count(code, "\n") > 2000 {
		return plain
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		return plain
	}
	var buf bytes.Buffer
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return plain
	}
	if err := formatter.Format(&buf, style, it); err != nil {
		return plain
	}
	out := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(out) == 0 {
		return plain
	}
	return out
}

// ---------- IO bridge ----------

type tuiIO struct {
	p        *tea.Program
	answerCh chan string

	mu        sync.Mutex
	buf       strings.Builder
	inCode    bool
	codeLang  string
	codeLines []string
}

func (t *tuiIO) Printf(format string, args ...any) { t.write(fmt.Sprintf(format, args...)) }
func (t *tuiIO) Println(a ...any)                  { t.write(fmt.Sprintln(a...)) }

func (t *tuiIO) Status(text string) {
	t.p.Send(statusMsg{text: text})
}

func (t *tuiIO) Input(prompt string) (string, error) {
	t.p.Send(enableInputMsg{prompt: prompt})
	v := <-t.answerCh
	if v == inputSentinel {
		return "", io.EOF
	}
	return v, nil
}

func (t *tuiIO) write(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf.WriteString(s)
	data := t.buf.String()
	for {
		i := strings.IndexByte(data, '\n')
		if i < 0 {
			t.buf.Reset()
			t.buf.WriteString(data)
			return
		}
		t.flushLine(strings.TrimRight(data[:i], "\r"))
		data = data[i+1:]
	}
}

func (t *tuiIO) flushLine(line string) {
	line = strings.ReplaceAll(line, "\r", "")
	stripped := strings.TrimSpace(line)
	if strings.HasPrefix(stripped, "```") {
		if t.inCode {
			code := strings.Join(t.codeLines, "\n")
			for _, hl := range highlightCode(code, t.codeLang) {
				t.p.Send(logMsg{line: styleCode.Render(hl)})
			}
			t.codeLines = nil
			t.inCode = false
		} else {
			t.inCode = true
			t.codeLang = strings.TrimSpace(strings.TrimPrefix(stripped, "```"))
		}
		return
	}
	if t.inCode {
		t.codeLines = append(t.codeLines, line)
		return
	}
	t.p.Send(logMsg{line: line})
}

// ---------- messages ----------

type logMsg struct{ line string }
type statusMsg struct{ text string }
type enableInputMsg struct{ prompt string }
type agentDoneMsg struct{}

// ---------- model ----------

type model struct {
	program *tea.Program
	agent   *agent.Agent
	cfg     *config.Config

	log       []string
	logScroll int
	status    string

	tree       *treeNode
	treeSel    *treeNode
	treeScroll int
	treeFocus  bool

	answerCh    chan string
	input       *input
	inputActive bool
	prompt      string

	width, height int
	ready         bool
}

func newModel(cfg *config.Config) *model {
	m := &model{
		cfg:      cfg,
		input:    newInput(),
		answerCh: make(chan string, 1),
		status: fmt.Sprintf("модель: %s\nworkspace: %s\nperms: %s",
			cfg.LM.Name, cfg.TN.Workspace, cfg.TN.PermissionMode),
	}
	m.tree = buildTreeNodes(cfg.TN.Workspace)
	m.treeSel = m.tree
	return m
}

func (m *model) Init() tea.Cmd { return nil }

// Run starts the TUI with the given config, optionally restoring a session
// after the agent is constructed.
func Run(cfg *config.Config, onAgentReady func(*agent.Agent)) error {
	m := newModel(cfg)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	m.program = p
	io := &tuiIO{p: p, answerCh: m.answerCh}

	ag, err := agent.New(cfg, io)
	if err != nil {
		return err
	}
	m.agent = ag
	if onAgentReady != nil {
		onAgentReady(ag)
	}

	go func() {
		ag.Run()
		p.Send(agentDoneMsg{})
	}()

	_, err = p.Run()
	return err
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.SetWidth(msg.Width - 4)
		m.ready = true
		return m, nil

	case logMsg:
		m.appendLog(msg.line)
		return m, nil

	case statusMsg:
		m.status = msg.text
		return m, nil

	case enableInputMsg:
		m.prompt = msg.prompt
		m.inputActive = true
		m.input.Reset()
		m.input.Focus()
		m.updateHint()
		return m, nil

	case agentDoneMsg:
		return m, tea.Quit

	case tea.MouseMsg:
		return m, m.handleMouse(msg)

	case tea.KeyMsg:
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *model) handleKey(msg tea.KeyMsg) tea.Cmd {
	if m.inputActive {
		switch msg.String() {
		case "enter":
			return m.submitInput()
		case "ctrl+c":
			m.answerCh <- inputSentinel
			return tea.Quit
		case "esc":
			m.prompt = ""
			m.inputActive = false
			m.input.Reset()
			m.input.Blur()
			m.answerCh <- ""
			return nil
		default:
			m.input.Update(msg)
			m.updateHint()
			return nil
		}
	}

	switch msg.String() {
	case "ctrl+c":
		if m.inputActive {
			m.answerCh <- inputSentinel
		}
		return tea.Quit
	case "esc":
		if m.agent != nil {
			m.agent.Abort()
			m.appendLog("[Esc] генерация остановлена — введите новый запрос или продолжите.")
		}
		return nil
	case "f2":
		m.rebuildTree()
		return nil
	case "tab":
		m.treeFocus = !m.treeFocus
		return nil
	}

	if m.treeFocus {
		switch msg.String() {
		case "up", "k":
			m.moveTreeSel(-1)
		case "down", "j":
			m.moveTreeSel(1)
		case "enter", " ":
			m.toggleTreeSel()
		case "right", "l":
			m.treeRight()
		case "left", "h":
			m.treeLeft()
		case "pgup":
			m.treeScroll -= m.treeHeight()
			if m.treeScroll < 0 {
				m.treeScroll = 0
			}
		case "pgdown":
			m.treeScroll += m.treeHeight()
		case "home":
			m.treeSel = m.tree
			m.treeScroll = 0
		case "end":
			lines := m.treeLines()
			if len(lines) > 0 {
				m.treeSel = lines[len(lines)-1].node
			}
		}
		return nil
	}

	switch msg.String() {
	case "up", "k":
		if m.logScroll > 0 {
			m.logScroll--
		}
		return nil
	case "down", "j":
		m.logScroll++
		return nil
	case "pgup":
		m.logScroll -= m.logHeight()
		if m.logScroll < 0 {
			m.logScroll = 0
		}
		return nil
	case "pgdown":
		m.logScroll += m.logHeight()
		return nil
	}
	return nil
}

// updateHint recomputes the gray slash-command completion.
func (m *model) updateHint() {
	m.input.SetCompletion("")
	v := m.input.Value()
	if !strings.HasPrefix(v, "/") {
		return
	}
	token := v
	if idx := strings.IndexAny(v, " \t"); idx >= 0 {
		token = v[:idx]
	}
	if len(token) <= 1 {
		return // bare "/", no command name yet
	}
	if token != v {
		return // already typing arguments
	}
	for _, c := range agent.SlashCommands {
		if c == token {
			return // full command already typed
		}
	}
	for _, c := range agent.SlashCommands {
		if strings.HasPrefix(c, token) {
			m.input.SetCompletion(c)
			return
		}
	}
}

// resolveSlash validates a slash command. It returns the resolved value (with
// the first prefix match auto-completed) and whether it is valid.
func resolveSlash(value string) (string, bool) {
	token := value
	if idx := strings.IndexAny(value, " \t"); idx >= 0 {
		token = value[:idx]
	}
	if len(token) <= 1 {
		return "", false
	}
	for _, c := range agent.SlashCommands {
		if c == token {
			return value, true
		}
	}
	for _, c := range agent.SlashCommands {
		if strings.HasPrefix(c, token) {
			return c + value[len(token):], true
		}
	}
	return "", false
}

func (m *model) submitInput() tea.Cmd {
	value := strings.TrimSpace(m.input.Value())

	if strings.HasPrefix(value, "/") {
		resolved, ok := resolveSlash(value)
		if !ok {
			m.input.SetError("команда не известна: " + value)
			m.input.SetCompletion("")
			return nil
		}
		value = resolved
	}

	m.input.Reset()
	m.input.Blur()
	m.inputActive = false
	if value != "" {
		m.appendLog(">>> " + value)
	}
	m.prompt = ""
	m.answerCh <- value
	return nil
}

func (m *model) appendLog(line string) {
	if line == "" {
		return
	}
	m.log = append(m.log, line)
	if len(m.log) > maxLogLines {
		m.log = m.log[len(m.log)-maxLogLines:]
	}
}

func (m *model) inputBlockHeight() int {
	lines := 1 // input line (or idle placeholder)
	if m.inputActive && m.prompt != "" {
		lines += strings.Count(m.prompt, "\n") + 1
	}
	if m.input.Error() != "" {
		lines++
	}
	return lines + 2 // border
}

func (m *model) bodyH() int {
	h := m.height - 1 - 1 - m.inputBlockHeight() // header + footer + input block
	if h < 4 {
		h = 4
	}
	return h
}

// sideWidths returns the widths of the log panel (left) and the side panel
// (right), mirroring the split used in View.
func (m *model) sideWidths() (leftW, rightW int) {
	leftW = m.width * 3 / 4
	rightW = m.width - leftW
	if rightW < 24 {
		rightW = 24
		leftW = m.width - rightW
	}
	return leftW, rightW
}

// treePanelGeometry returns the absolute screen row of the first visible tree
// line and the number of tree lines shown, matching the layout in View. ok is
// false when the tree panel is not rendered.
func (m *model) treePanelGeometry() (topY, innerHeight int, ok bool) {
	bodyH := m.bodyH()
	statusH := 6
	treeH := bodyH - statusH
	if treeH < 0 {
		statusH = bodyH
		treeH = 0
	}
	if treeH < 2 {
		return 0, 0, false
	}
	topY = 1 + statusH + 2 // header + status box + tree top border + title
	innerHeight = treeH - 3
	if innerHeight < 1 {
		innerHeight = 1
	}
	return topY, innerHeight, true
}

func (m *model) logHeight() int {
	h := m.bodyH() - 2
	if h < 1 {
		h = 1
	}
	return h
}

// ---------- view ----------

func (m *model) View() string {
	if !m.ready {
		return "loading…"
	}

	bodyH := m.bodyH()
	leftW, rightW := m.sideWidths()

	header := styleTitle.Render("tiny-code") + styleHint.Render(" — локальный AI-агент")
	if m.agent != nil {
		header += styleHint.Render(fmt.Sprintf(" · модель %s · %s", m.agent.ModelName(), m.agent.PermissionMode()))
	}

	log := styleLogBrd.Width(leftW).Height(bodyH).Render(m.scrolledLog(bodyH - 2))

	statusH := 6
	treeH := bodyH - statusH
	if treeH < 0 {
		statusH = bodyH
		treeH = 0
	}

	status := styleSideBrd.Width(rightW).Height(statusH - 2).Render(" статус\n" + styleStatus.Render(m.status))
	side := status
	if treeH >= 2 {
		title := " файлы"
		if m.treeFocus {
			title = " файлы [активно]"
		}
		tree := styleSideBrd.Width(rightW).Height(treeH - 2).Render(title + "\n" + m.treeContent(rightW-2, treeH-3))
		side = lipgloss.JoinVertical(lipgloss.Left, status, tree)
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top, log, side)

	inputBlock := m.inputView()

	footer := styleHint.Render("Tab — файлы · Enter — отправить · Esc — остановить · ↑/↓ — скролл · Ctrl+C — выход")

	return lipgloss.JoinVertical(lipgloss.Left, header, body, inputBlock, footer)
}

// inputView renders the dedicated input block (prompt, input line with inline
// gray completion, and an optional inline error).
func (m *model) inputView() string {
	var lines []string
	if m.inputActive {
		if m.prompt != "" {
			lines = append(lines, stylePrompt.Render(strings.TrimRight(m.prompt, "\n")))
		}
		lines = append(lines, m.input.View())
	} else {
		lines = append(lines, styleHint.Render("Спросите агента… (Enter — ввод)"))
	}
	if e := m.input.Error(); e != "" {
		lines = append(lines, styleErr.Render("  ✗ "+e))
	}
	return styleInpBrd.Width(m.width - 2).Render(strings.Join(lines, "\n"))
}

// scrolledLog returns up to height visible log lines, clamping the scroll offset.
func (m *model) scrolledLog(height int) string {
	total := len(m.log)
	if height < 1 {
		height = 1
	}
	bottom := total - height
	if bottom < 0 {
		bottom = 0
	}
	if m.logScroll > bottom {
		m.logScroll = bottom
	}
	if m.logScroll < 0 {
		m.logScroll = 0
	}
	end := m.logScroll + height
	if end > total {
		end = total
	}
	content := strings.Join(m.log[m.logScroll:end], "\n")
	if content == "" {
		return styleHint.Render("(пусто)")
	}
	return content
}

func (m *model) treeContent(width, height int) string {
	if height < 1 {
		height = 1
	}
	lines := m.treeLines()
	total := len(lines)
	if total == 0 {
		return styleTree.Render("(пусто)")
	}
	if m.treeSel == nil {
		m.treeSel = lines[0].node
	}

	selIdx := 0
	for i, l := range lines {
		if l.node == m.treeSel {
			selIdx = i
			break
		}
	}
	if selIdx < m.treeScroll {
		m.treeScroll = selIdx
	}
	if selIdx >= m.treeScroll+height {
		m.treeScroll = selIdx - height + 1
	}
	if m.treeScroll > total-height {
		m.treeScroll = total - height
	}
	if m.treeScroll < 0 {
		m.treeScroll = 0
	}
	end := m.treeScroll + height
	if end > total {
		end = total
	}

	var b strings.Builder
	for i := m.treeScroll; i < end; i++ {
		text := renderTreeLine(lines[i])
		if i == selIdx {
			text = styleTreeSel.Width(width).Render(text)
		} else {
			text = styleTree.Render(text)
		}
		if i > m.treeScroll {
			b.WriteByte('\n')
		}
		b.WriteString(text)
	}
	return b.String()
}

// treeHeight returns the inner height of the tree panel.
func (m *model) treeHeight() int {
	h := m.bodyH() - 6 - 3 // status block + border/title lines
	if h < 1 {
		h = 1
	}
	return h
}

// ---------- file tree ----------

// treeNode is a node in the interactive file tree.
type treeNode struct {
	name     string
	path     string
	isDir    bool
	expanded bool
	parent   *treeNode
	children []*treeNode
}

// treeLine is a single visible row in the flattened tree.
type treeLine struct {
	node  *treeNode
	depth int
}

func buildTreeNodes(root string) *treeNode {
	n := &treeNode{name: filepath.Base(root), path: root, isDir: true, expanded: true}
	count := 0
	addTreeChildren(n, 0, &count)
	return n
}

func addTreeChildren(n *treeNode, depth int, count *int) {
	if depth > maxTreeDepth || *count > maxTreeNodes {
		return
	}
	entries, err := os.ReadDir(n.path)
	if err != nil {
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return !entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		child := &treeNode{name: e.Name(), path: filepath.Join(n.path, e.Name()), parent: n}
		if e.IsDir() {
			if tools.EXCLUDE_DIRS[e.Name()] {
				continue
			}
			child.isDir = true
			child.expanded = false
			n.children = append(n.children, child)
			*count++
			addTreeChildren(child, depth+1, count)
		} else {
			child.isDir = false
			n.children = append(n.children, child)
			*count++
		}
	}
}

// treeLines flattens the tree into visible rows, respecting expand state.
func (m *model) treeLines() []treeLine {
	var out []treeLine
	if m.tree == nil {
		return out
	}
	var walk func(n *treeNode, depth int)
	walk = func(n *treeNode, depth int) {
		out = append(out, treeLine{node: n, depth: depth})
		if n.isDir && n.expanded {
			for _, c := range n.children {
				walk(c, depth+1)
			}
		}
	}
	walk(m.tree, 0)
	return out
}

func (m *model) rebuildTree() {
	m.tree = buildTreeNodes(m.cfg.TN.Workspace)
	m.treeSel = m.tree
	m.treeScroll = 0
}

func (m *model) moveTreeSel(delta int) {
	lines := m.treeLines()
	if len(lines) == 0 {
		return
	}
	idx := -1
	for i, l := range lines {
		if l.node == m.treeSel {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.treeSel = lines[0].node
		return
	}
	idx += delta
	if idx < 0 {
		idx = 0
	}
	if idx >= len(lines) {
		idx = len(lines) - 1
	}
	m.treeSel = lines[idx].node
}

func (m *model) toggleTreeSel() {
	if m.treeSel != nil && m.treeSel.isDir {
		m.treeSel.expanded = !m.treeSel.expanded
	}
}

// handleMouse handles a click inside the file tree panel: it selects the
// clicked line and toggles the expand/collapse state of a folder.
func (m *model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if msg.Action != tea.MouseActionPress {
		return nil
	}
	if msg.Button != tea.MouseButtonLeft {
		return nil
	}
	if !m.ready {
		return nil
	}

	leftW, rightW := m.sideWidths()
	if msg.X < leftW || msg.X >= leftW+rightW {
		return nil
	}

	topY, innerHeight, ok := m.treePanelGeometry()
	if !ok {
		return nil
	}
	rel := msg.Y - topY
	if rel < 0 || rel >= innerHeight {
		return nil
	}

	lines := m.treeLines()
	idx := m.treeScroll + rel
	if idx < 0 || idx >= len(lines) {
		return nil
	}

	node := lines[idx].node
	m.treeSel = node
	if node.isDir {
		node.expanded = !node.expanded
	}
	return nil
}

func (m *model) treeRight() {
	if m.treeSel == nil {
		return
	}
	if m.treeSel.isDir {
		if !m.treeSel.expanded {
			m.treeSel.expanded = true
			return
		}
		if len(m.treeSel.children) > 0 {
			m.treeSel = m.treeSel.children[0]
		}
	}
}

func (m *model) treeLeft() {
	if m.treeSel == nil {
		return
	}
	if m.treeSel.isDir && m.treeSel.expanded {
		m.treeSel.expanded = false
		return
	}
	if m.treeSel.parent != nil {
		m.treeSel = m.treeSel.parent
	}
}

func renderTreeLine(l treeLine) string {
	indent := strings.Repeat("  ", l.depth)
	marker := "  "
	name := l.node.name
	if l.node.isDir {
		if l.node.expanded {
			marker = "◀ "
		} else {
			marker = "▶ "
		}
		name += "/"
	}
	return indent + marker + name
}
