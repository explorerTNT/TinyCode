package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/explorerTNT/TinyCode/internal/config"
	"github.com/explorerTNT/TinyCode/internal/i18n"
)

// layoutModel builds a ready model of the given size with fileCount workspace
// files and a deterministic English catalog.
func layoutModel(t *testing.T, width, height, fileCount int) *model {
	t.Helper()
	i18n.Set(i18n.En)
	dir := t.TempDir()
	for i := 0; i < fileCount; i++ {
		writeFile(t, dir, fmt.Sprintf("file_%02d.txt", i), "x\n")
	}
	cfg := &config.Config{
		Language: "en",
		LM:       &config.LMConfig{Name: "m"},
		TN:       &config.TNConfig{Workspace: dir, PermissionMode: "ask"},
	}
	m := newModel(cfg)
	m.width, m.height = width, height
	m.ready = true
	return m
}

func viewRows(t *testing.T, m *model) []string {
	t.Helper()
	return strings.Split(m.View(), "\n")
}

// boxCorner returns the first row where col holds corner, or -1.
func boxCorner(rows []string, col int, corner rune) int {
	for i, ln := range rows {
		r := []rune(ln)
		if col >= 0 && col < len(r) && r[col] == corner {
			return i
		}
	}
	return -1
}

// lastBoxCorner returns the last row where col holds corner, or -1. The right
// column stacks two boxes, so its overall bottom is the last corner.
func lastBoxCorner(rows []string, col int, corner rune) int {
	for i := len(rows) - 1; i >= 0; i-- {
		r := []rune(rows[i])
		if col >= 0 && col < len(r) && r[col] == corner {
			return i
		}
	}
	return -1
}

// TestViewLayoutFitsTerminal: View() must be exactly width x height, or the
// whole UI slides (issue #4: columns/panel overflow the terminal).
func TestViewLayoutFitsTerminal(t *testing.T) {
	cases := []struct{ w, h, files int }{
		{100, 40, 30},
		{100, 40, 0},
		{80, 24, 30},
		{120, 50, 0},
		{90, 14, 5},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%dx%d_f%d", c.w, c.h, c.files), func(t *testing.T) {
			m := layoutModel(t, c.w, c.h, c.files)
			rows := viewRows(t, m)
			if len(rows) != c.h {
				t.Errorf("view height = %d, want %d", len(rows), c.h)
			}
			for i, ln := range rows {
				if got := lipgloss.Width(ln); got != c.w {
					t.Errorf("row %d width = %d, want %d: %q", i, got, c.w, ln)
					break
				}
			}
		})
	}
}

// TestViewLayoutColumnsAligned: the log box and the right column (status+tree)
// must end on the same screen row — the visible symptom of issue #4.
func TestViewLayoutColumnsAligned(t *testing.T) {
	cases := []struct{ w, h, files int }{
		{100, 40, 30},
		{100, 40, 0},
		{80, 24, 30},
		{90, 14, 5},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%dx%d_f%d", c.w, c.h, c.files), func(t *testing.T) {
			m := layoutModel(t, c.w, c.h, c.files)
			rows := viewRows(t, m)
			leftW, _ := m.sideWidths()

			leftBottom := boxCorner(rows, 0, '╰')
			if leftBottom < 0 {
				t.Fatal("log box bottom border not found")
			}
			rightBottom := lastBoxCorner(rows, leftW, '╰')
			if rightBottom < 0 {
				t.Fatalf("right column bottom border not found at col %d", leftW)
			}
			if leftBottom != rightBottom {
				t.Errorf("bottom borders misaligned: left=%d right=%d (delta %+d)",
					leftBottom, rightBottom, rightBottom-leftBottom)
			}
			if want := m.bodyH(); leftBottom != want { // header row 0 + bodyH rows
				t.Errorf("log bottom border row = %d, want %d", leftBottom, want)
			}
		})
	}
}

// TestTreePanelGeometry ties treePanelGeometry (mouse hit-testing) to the
// actual rendered borders: topY must be the first tree content row and
// innerHeight the visible tree line count.
func TestTreePanelGeometry(t *testing.T) {
	for _, files := range []int{0, 30} {
		t.Run(fmt.Sprintf("files_%d", files), func(t *testing.T) {
			m := layoutModel(t, 100, 40, files)
			rows := viewRows(t, m)
			leftW, _ := m.sideWidths()

			topY, innerHeight, ok := m.treePanelGeometry()
			if !ok {
				t.Fatal("treePanelGeometry ok = false, want true at 100x40")
			}
			if boxCorner(rows, leftW, '╭') < 0 {
				t.Fatalf("status box top border not found at col %d", leftW)
			}
			// Second ╭ at leftW is the tree box top border; content starts
			// two rows below (border row + title row).
			count, treeTop := 0, -1
			for i, ln := range rows {
				r := []rune(ln)
				if leftW < len(r) && r[leftW] == '╭' {
					count++
					if count == 2 {
						treeTop = i
						break
					}
				}
			}
			if treeTop < 0 {
				t.Fatalf("tree box top border not found at col %d", leftW)
			}
			if got := treeTop + 2; got != topY {
				t.Errorf("first tree content row = %d, treePanelGeometry topY = %d (delta %+d)",
					got, topY, got-topY)
			}

			innerCap := m.bodyH() - 6 - 3 // status(6) + borders(2) + title(1)
			want := len(m.treeLines())
			if want > innerCap {
				want = innerCap
			}
			if want < 1 {
				want = 1
			}
			if innerHeight != want {
				t.Errorf("innerHeight = %d, want %d", innerHeight, want)
			}

			leftBottom := boxCorner(rows, 0, '╰')
			rightBottom := lastBoxCorner(rows, leftW, '╰')
			if rightBottom != leftBottom {
				t.Errorf("tree/status bottom = %d, log bottom = %d", rightBottom, leftBottom)
			}
		})
	}
}

// TestViewLayoutFitsWithLongLogLine: a log line wider than the panel wraps;
// the view must still fit the terminal (row widths included). Border
// alignment is not asserted here — when wrapped content overflows the panel,
// MaxHeight keeps the layout but may clip the bottom border.
func TestViewLayoutFitsWithLongLogLine(t *testing.T) {
	m := layoutModel(t, 100, 40, 30)
	for i := 0; i < 30; i++ {
		m.appendLog(fmt.Sprintf("log line %d", i))
	}
	m.appendLog(strings.Repeat("x", 500))

	rows := viewRows(t, m)
	if len(rows) != m.height {
		t.Errorf("view height = %d, want %d", len(rows), m.height)
	}
	for i, ln := range rows {
		if got := lipgloss.Width(ln); got != m.width {
			t.Errorf("row %d width = %d, want %d: %q", i, got, m.width, ln)
			break
		}
	}
}

// TestViewLayoutFitsWithLongTreeName: a file name wider than the tree panel
// must be truncated, not wrapped — wrapping would grow the box and slide the
// columns again.
func TestViewLayoutFitsWithLongTreeName(t *testing.T) {
	m := layoutModel(t, 100, 40, 0)
	writeFile(t, m.cfg.TN.Workspace, strings.Repeat("n", 120)+".go", "x\n")
	m.rebuildTree()
	for _, l := range m.treeLines() {
		if len(l.node.name) > 100 {
			m.treeSel = l.node
		}
	}

	rows := viewRows(t, m)
	if len(rows) != m.height {
		t.Errorf("view height = %d, want %d", len(rows), m.height)
	}
	for i, ln := range rows {
		if got := lipgloss.Width(ln); got != m.width {
			t.Errorf("row %d width = %d, want %d: %q", i, got, m.width, ln)
			break
		}
	}
	leftW, _ := m.sideWidths()
	leftBottom := boxCorner(rows, 0, '╰')
	rightBottom := lastBoxCorner(rows, leftW, '╰')
	if leftBottom < 0 || rightBottom < 0 {
		t.Fatalf("bottom borders not found: left=%d right=%d", leftBottom, rightBottom)
	}
	if leftBottom != rightBottom {
		t.Errorf("bottom borders misaligned: left=%d right=%d", leftBottom, rightBottom)
	}
}
