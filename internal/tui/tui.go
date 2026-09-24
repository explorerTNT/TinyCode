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
	"time"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/explorerTNT/TinyCode/internal/agent"
	"github.com/explorerTNT/TinyCode/internal/config"
	"github.com/explorerTNT/TinyCode/internal/i18n"
	"github.com/explorerTNT/TinyCode/internal/tools"
)

const (
	maxLogLines     = 2000
	maxTreeNodes    = 600
	maxTreeDepth    = 6
	wheelScrollStep = 3
	inputSentinel   = "\x00tinycode-quit\x00"
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
		return plain
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

	fileIndex []fileRef

	answerCh    chan string
	input       *input
	inputActive bool
	sideMode    bool
	prompt      string
	ac          *autocomplete

	updateRunning bool

	lastClickNode *treeNode
	lastClickAt   time.Time

	width, height int
	ready         bool
}

func newModel(cfg *config.Config) *model {
	m := &model{
		cfg:      cfg,
		input:    newInput(),
		answerCh: make(chan string, 1),
		status:   i18n.T("tui.model_status", cfg.LM.Name, cfg.TN.Workspace, cfg.TN.PermissionMode),
	}
	m.tree = buildTreeNodes(cfg.TN.Workspace)
	m.fileIndex = buildFileIndex(m.tree, cfg.TN.Workspace)
	m.treeSel = m.tree
	m.ac = &autocomplete{fr: newFrecency(), build: m.acCandidates}
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
	if cfg.UpdateCheck {
		startUpdateCheck(p, cfg)
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
		m.sideMode = false
		m.input.Reset()
		m.input.Focus()
		m.ac.hide()
		m.updateHint()
		return m, nil

	case agentDoneMsg:
		return m, tea.Quit

	case updateFinishedMsg:
		m.updateRunning = false
		switch {
		case msg.err != nil:
			m.appendLog(i18n.T("update.failed", msg.err))
		case msg.installed:
			m.appendLog(i18n.T("update.done", msg.tag))
		default:
			m.appendLog(i18n.T("update.latest", msg.tag))
		}
		return m, nil

	case tea.MouseMsg:
		return m, m.handleMouse(msg)

	case tea.KeyMsg:
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *model) handleKey(msg tea.KeyMsg) tea.Cmd {
	if m.inputActive {
		if m.ac.active() {
			switch msg.String() {
			case "up", "ctrl+p":
				m.ac.move(-1)
				return nil
			case "down", "ctrl+n":
				m.ac.move(1)
				return nil
			case "tab":
				return m.acComplete()
			case "esc":
				m.ac.hide()
				return nil
			}
		}
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
			m.ac.hide()
			m.answerCh <- ""
			return nil
		default:
			m.input.Update(msg)
			m.ac.update(m.input.Value(), m.input.Cursor())
			m.updateHint()
			return nil
		}
	}

	if m.sideMode {
		switch msg.String() {
		case "enter":
			return m.submitSide()
		case "ctrl+c":
			return tea.Quit
		case "esc":
			m.exitSideMode()
			return nil
		default:
			m.input.Update(msg)
			return nil
		}
	}

	switch msg.String() {
	case "ctrl+c":
		return tea.Quit
	case "esc":
		if m.agent != nil {
			m.agent.Abort()
			m.appendLog(i18n.T("tui.esc_stopped"))
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
			m.moveTreeSel(-m.treeHeight())
		case "pgdown":
			m.moveTreeSel(m.treeHeight())
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
		if max := len(m.log) - m.logHeight(); m.logScroll > max {
			m.logScroll = max
		}
		if m.logScroll < 0 {
			m.logScroll = 0
		}
		return nil
	}

	if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
		m.sideMode = true
		m.input.Reset()
		m.input.Focus()
		m.input.Update(msg)
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
	raw := strings.TrimSpace(m.input.Value())
	value := raw

	if strings.HasPrefix(value, "/") {
		resolved, ok := resolveSlash(value)
		if !ok {
			m.input.SetError(i18n.T("tui.cmd_unknown", value))
			m.input.SetCompletion("")
			return nil
		}
		value = resolved
	} else if !strings.HasPrefix(value, "!") {
		value = resolveMentions(value, m.cfg.TN.Workspace)
	}

	m.input.Reset()
	m.input.Blur()
	m.inputActive = false
	m.ac.hide()
	m.prompt = ""
	if isUpdateCommand(value) {
		return m.beginUpdate(raw)
	}
	if value != "" {
		m.appendLog(">>> " + raw)
	}
	m.answerCh <- value
	return nil
}

// exitSideMode leaves the /btw side-question input without submitting.
func (m *model) exitSideMode() {
	m.sideMode = false
	m.input.Reset()
	m.input.Blur()
}

// parseBtw extracts the question from a /btw value. ok is false when the value
// is not a valid /btw request.
func parseBtw(value string) (string, bool) {
	v := strings.TrimSpace(value)
	if !strings.HasPrefix(v, "/btw") {
		return "", false
	}
	q := strings.TrimSpace(strings.TrimPrefix(v, "/btw"))
	if q == "" {
		return "", false
	}
	return q, true
}

// submitSide routes a side-question submission while the agent is busy. It
// never touches answerCh (the main loop is not reading it mid-turn).
func (m *model) submitSide() tea.Cmd {
	raw := strings.TrimSpace(m.input.Value())
	m.exitSideMode()

	if raw == "" {
		return nil
	}
	if isUpdateCommand(raw) {
		return m.beginUpdate(raw)
	}
	q, ok := parseBtw(raw)
	if !ok {
		m.appendLog(i18n.T("tui.btw_hint"))
		return nil
	}
	m.appendLog("/btw " + q)
	go m.runSideQuestion(q)
	return nil
}

// runSideQuestion answers a side question off the UI loop and streams the
// result into the log panel. Send on a finished program is a safe no-op.
func (m *model) runSideQuestion(q string) {
	m.program.Send(statusMsg{text: i18n.T("tui.btw_answering")})
	answer := strings.TrimSpace(m.agent.RunSideQuestion(q))
	m.program.Send(statusMsg{text: ""})
	if answer == "" {
		m.program.Send(logMsg{line: i18n.T("tui.btw_no_answer")})
		return
	}
	for _, line := range strings.Split(answer, "\n") {
		m.program.Send(logMsg{line: line})
	}
}

// acComplete replaces the mention/command being typed with the selected
// option, then closes the menu.
func (m *model) acComplete() tea.Cmd {
	o, ok := m.ac.selectedOption()
	if !ok {
		return nil
	}
	runes := []rune(m.input.Value())
	index := m.ac.index
	cursor := m.input.Cursor()
	if index < 0 || index > cursor || cursor > len(runes) {
		m.ac.hide()
		return nil
	}
	m.input.SetValue(string(runes[:index]) + o.Value + string(runes[cursor:]))
	if o.Path != "" {
		m.ac.fr.touch(o.Path)
	}
	m.ac.hide()
	m.updateHint()
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
	lines := 1 // input line
	if m.inputActive {
		if n := len(m.ac.options); n > 0 {
			lines += n // autocomplete menu items (no border)
		}
		if m.prompt != "" {
			lines += strings.Count(m.prompt, "\n") + 1
		}
	} else {
		lines++ // busy hint line (side-question prompt)
	}
	if m.input.Error() != "" {
		lines++
	}
	return lines + 2 // border
}

func (m *model) bodyH() int {
	h := m.height - 1 - 1 - m.inputBlockHeight() // header + footer + input block
	if h < 1 {
		h = 1
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
	if treeH < 4 {
		return 0, 0, false // box needs borders(2)+title(1)+content(1)
	}
	topY = 1 + statusH + 2 // header + status box + tree top border + title
	visibleLines := len(m.treeLines())
	innerCap := treeH - 3
	if innerCap < 1 {
		innerCap = 1
	}
	inner := visibleLines
	if inner > innerCap {
		inner = innerCap
	}
	if inner < 1 {
		inner = 1
	}
	innerHeight = inner
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
		return i18n.T("tui.loading")
	}

	bodyH := m.bodyH()
	leftW, rightW := m.sideWidths()

	header := styleTitle.Render("tiny-code") + styleHint.Render(i18n.T("tui.subtitle"))
	if m.agent != nil {
		header += styleHint.Render(i18n.T("tui.model_info", m.agent.ModelName(), m.agent.PermissionMode()))
	}

	// lipgloss Height/Width apply to content only — the border adds 2 rows and
	// 2 cols on top — so size the content total-2 and cap the final block at
	// the total (issue #4: boxes grew past the terminal and slid the layout).
	log := styleLogBrd.
		Width(leftW - 2).
		Height(bodyH - 2).
		MaxHeight(bodyH).
		Render(m.scrolledLog(bodyH - 2))

	statusH := 6
	treeH := bodyH - statusH
	if treeH < 0 {
		statusH = bodyH
		treeH = 0
	}
	// The tree box needs borders(2)+title(1)+content(1)=4 rows; below that
	// the status box takes the whole body.
	renderTree := treeH >= 4

	statusTotal := statusH
	if !renderTree {
		statusTotal = bodyH
	}
	status := styleSideBrd.
		Width(rightW - 2).
		Height(statusTotal - 2).
		MaxHeight(statusTotal).
		Render(m.statusContent(rightW - 2))

	side := status
	if renderTree {
		innerCap := treeH - 3 // borders(2) + title(1)
		if innerCap < 1 {
			innerCap = 1
		}
		title := i18n.T("tui.files")
		if m.treeFocus {
			title = i18n.T("tui.files_active")
		}
		tree := styleSideBrd.
			Width(rightW - 2).
			Height(treeH - 2).
			MaxHeight(treeH).
			Render(title + "\n" + m.treeContent(rightW-2, innerCap))
		side = lipgloss.JoinVertical(lipgloss.Left, status, tree)
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top, log, side)

	inputBlock := m.inputView()

	footer := styleHint.Render(i18n.T("tui.footer"))

	return lipgloss.JoinVertical(lipgloss.Left, header, body, inputBlock, footer)
}

// inputView renders the dedicated input block (prompt, input line with inline
// gray completion, and an optional inline error). The input box is always
// rendered so a side question can be typed while the agent is busy.
func (m *model) inputView() string {
	var lines []string
	if m.inputActive {
		if ac := m.ac.View(m.width - 2); ac != "" {
			lines = append(lines, ac)
		}
		if m.prompt != "" {
			lines = append(lines, stylePrompt.Render(strings.TrimRight(m.prompt, "\n")))
		}
		lines = append(lines, m.input.View())
	} else if m.sideMode {
		lines = append(lines, styleHint.Render(i18n.T("tui.btw_input")))
		lines = append(lines, m.input.View())
	} else {
		lines = append(lines, m.input.View())
		lines = append(lines, styleHint.Render(i18n.T("tui.agent_busy")))
	}
	if e := m.input.Error(); e != "" {
		lines = append(lines, styleErr.Render("  ✗ "+e))
	}
	return styleInpBrd.Width(m.width - 2).Render(strings.Join(lines, "\n"))
}

// statusContent renders the status box content, truncating each line to
// width so a long workspace path cannot wrap and grow the box.
func (m *model) statusContent(width int) string {
	lines := []string{truncatePlain(i18n.T("tui.status_label"), width)}
	for _, ln := range strings.Split(m.status, "\n") {
		lines = append(lines, styleStatus.Render(truncatePlain(ln, width)))
	}
	return strings.Join(lines, "\n")
}

// truncatePlain shortens s to at most w terminal cells, appending an
// ellipsis when cut. s must be plain text (no ANSI sequences).
func truncatePlain(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if n+rw > w-1 {
			break
		}
		b.WriteRune(r)
		n += rw
	}
	return b.String() + "…"
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
		return styleHint.Render(i18n.T("tui.empty"))
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
		return styleTree.Render(truncatePlain(i18n.T("tui.empty"), width))
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
		text := truncatePlain(renderTreeLine(lines[i]), width)
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
	h := m.bodyH() - 6 - 3 // status block(6) + border(2) + title(1)
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

// fileRef is a workspace file or directory collected from the tree for @
// mention candidates.
type fileRef struct {
	rel   string
	abs   string
	isDir bool
}

// buildFileIndex collects every file and directory node in the tree (already
// filtered by EXCLUDE_DIRS and hidden names) into a flat candidate list.
func buildFileIndex(root *treeNode, workspace string) []fileRef {
	var out []fileRef
	var walk func(n *treeNode)
	walk = func(n *treeNode) {
		if n != root {
			rel, err := filepath.Rel(workspace, n.path)
			if err == nil {
				out = append(out, fileRef{rel: filepath.ToSlash(rel), abs: n.path, isDir: n.isDir})
			}
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	if root != nil {
		walk(root)
	}
	return out
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
	m.fileIndex = buildFileIndex(m.tree, m.cfg.TN.Workspace)
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
		if m.treeSel.expanded {
			m.treeSel.expanded = false
			return
		}
		m.treeSel.expanded = true
	}
}

// selectParentIfDescendant moves treeSel to the folder being collapsed when
// the current selection is inside it, preventing a jump to the top.
func (m *model) selectParentIfDescendant(folder *treeNode) {
	if m.treeSel == nil || m.treeSel == folder {
		return
	}
	for n := m.treeSel; n != nil; n = n.parent {
		if n == folder {
			m.treeSel = folder
			return
		}
	}
}

// handleMouse handles a click inside the file tree panel: it selects the
// clicked line and toggles the expand/collapse state of a folder.
func (m *model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if msg.Action != tea.MouseActionPress {
		return nil
	}
	if !m.ready {
		return nil
	}

	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		leftW, rightW := m.sideWidths()
		if msg.X < leftW || msg.X >= leftW+rightW {
			return nil
		}
		topY, innerHeight, ok := m.treePanelGeometry()
		if !ok {
			return nil
		}
		if msg.Y < topY || msg.Y >= topY+innerHeight {
			return nil
		}
		if msg.Button == tea.MouseButtonWheelUp {
			m.moveTreeSel(-wheelScrollStep)
		} else {
			m.moveTreeSel(wheelScrollStep)
		}
		return nil
	}

	if msg.Button != tea.MouseButtonLeft {
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
	if !node.isDir && m.lastClickNode == node && time.Since(m.lastClickAt) < 500*time.Millisecond {
		m.lastClickNode = nil
		m.lastClickAt = time.Time{}
		return m.insertMention(node)
	}
	m.lastClickNode = node
	m.lastClickAt = time.Now()
	m.treeSel = node
	if node.isDir {
		if node.expanded {
			m.selectParentIfDescendant(node)
		}
		node.expanded = !node.expanded
	}
	return nil
}

// insertMention inserts an @path mention for a file into the input, activating
// the input first when it is not yet active.
func (m *model) insertMention(n *treeNode) tea.Cmd {
	if n.isDir {
		return nil
	}
	rel, err := filepath.Rel(m.cfg.TN.Workspace, n.path)
	if err != nil {
		return nil
	}
	mention := "@" + filepath.ToSlash(rel) + " "
	if !m.inputActive {
		m.prompt = ""
		m.inputActive = true
		m.input.Reset()
		m.input.Focus()
	}
	m.input.SetValue(m.input.Value() + mention)
	m.input.Focus()
	m.ac.hide()
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
