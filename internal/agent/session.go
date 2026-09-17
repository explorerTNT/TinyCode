package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/explorerTNT/TinyCode/internal/config"
)

// sessionData is the on-disk session representation.
type sessionData struct {
	Name      string    `json:"name"`
	Model     string    `json:"model"`
	Workspace string    `json:"workspace"`
	PlanMode  bool      `json:"plan_mode"`
	Messages  []Message `json:"messages"`
	Timestamp float64   `json:"timestamp"`
}

// SessionManager persists and restores conversations under ~/.tinycode/sessions.
type SessionManager struct {
	workspace  string
	sessionDir string
	plansDir   string
}

func newSessionManager(workspace string) (*SessionManager, error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return nil, err
	}
	sessionDir := filepath.Join(dir, "sessions")
	plansDir := filepath.Join(dir, "plans")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		return nil, err
	}
	return &SessionManager{workspace: workspace, sessionDir: sessionDir, plansDir: plansDir}, nil
}

func (s *SessionManager) safePath(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	safe := strings.TrimSpace(b.String())
	if safe == "" {
		safe = "session"
	}
	return filepath.Join(s.sessionDir, safe+".json")
}

func (s *SessionManager) save(name string, messages []Message, planMode bool, model, workspace string) (string, error) {
	path := s.safePath(name)
	data := sessionData{
		Name: name, Model: model, Workspace: workspace,
		PlanMode: planMode, Messages: messages, Timestamp: float64(time.Now().Unix()),
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func (s *SessionManager) load(name string) (*sessionData, error) {
	path := s.safePath(name)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var data sessionData
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

type sessionSummary struct {
	Name     string
	Model    string
	Messages int
	Time     string
}

func (s *SessionManager) list() []sessionSummary {
	entries, _ := os.ReadDir(s.sessionDir)
	type fileEntry struct {
		name string
		t    time.Time
	}
	var files []fileEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileEntry{e.Name(), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].t.After(files[j].t) })

	var results []sessionSummary
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(s.sessionDir, f.name))
		if err != nil {
			continue
		}
		var data sessionData
		if err := json.Unmarshal(b, &data); err != nil {
			continue
		}
		name := data.Name
		if name == "" {
			name = strings.TrimSuffix(f.name, ".json")
		}
		t := time.Unix(int64(data.Timestamp), 0)
		results = append(results, sessionSummary{
			Name: name, Model: data.Model, Messages: len(data.Messages),
			Time: t.Format("Jan 02 15:04"),
		})
	}
	return results
}

func (s *SessionManager) lastSession() string {
	sessions := s.list()
	if len(sessions) == 0 {
		return ""
	}
	return sessions[0].Name
}

func (s *SessionManager) savePlan(plan string) (string, error) {
	path := filepath.Join(s.plansDir, fmt.Sprintf("plan_%d.md", time.Now().Unix()))
	return path, os.WriteFile(path, []byte(plan), 0o644)
}
