package permissions

import (
	"strings"

	"github.com/explorerTNT/TinyCode/internal/i18n"
)

// Mode is the permission enforcement policy.
type Mode int8

const (
	ModeAuto Mode = iota + 1
	ModeDeny
	ModeAsk
)

// ParseMode maps a config string ("auto", "ask", "deny") to a Mode.
// Unknown or empty values default to ModeAsk.
func ParseMode(s string) Mode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "auto":
		return ModeAuto
	case "deny":
		return ModeDeny
	default:
		return ModeAsk
	}
}

func (m Mode) String() string {
	switch m {
	case ModeAuto:
		return "auto"
	case ModeDeny:
		return "deny"
	default:
		return "ask"
	}
}

// InputFunc reads a line from the user and returns it (untrimmed). A
// non-nil error signals that input is unavailable (closed stdin / TUI quit),
// which is treated as a rejection.
type InputFunc func(prompt string) (string, error)

// Service enforces write, shell and web permissions, prompting the user in
// "ask" mode through the injected InputFunc.
type Service struct {
	mode      Mode
	workspace string
	input     InputFunc
}

// New builds a Service bound to a workspace. mode==0 means ModeAsk. A nil
// input falls back to a stdin reader.
func New(mode Mode, workspace string, input InputFunc) *Service {
	if mode == 0 {
		mode = ModeAsk
	}
	if input == nil {
		input = defaultInput
	}
	return &Service{mode: mode, workspace: workspace, input: input}
}

// SetInput swaps the input source at runtime (used to rewire the console
// reader once a TUI takes over, and vice versa).
func (s *Service) SetInput(input InputFunc) {
	s.input = input
}

// Mode returns the current enforcement policy.
func (s *Service) Mode() Mode { return s.mode }

// CheckBash decides whether a shell command may run. In ask mode a command
// that the read-only policy recognises in full is approved without a prompt.
func (s *Service) CheckBash(command string) bool {
	switch s.mode {
	case ModeAuto:
		return true
	case ModeDeny:
		return false
	}
	if isCommandReadOnly(s.workspace, strings.ToLower(strings.TrimSpace(command))) {
		return true
	}
	return s.askUser(i18n.T("perm.run", command))
}

// CheckWrite decides whether a file write may proceed.
func (s *Service) CheckWrite(path string) bool {
	switch s.mode {
	case ModeAuto:
		return true
	case ModeDeny:
		return false
	}
	return s.askUser(i18n.T("perm.write", path))
}

// CheckWeb decides whether a web request to the given query or URL may
// leave the machine.
func (s *Service) CheckWeb(target string) bool {
	switch s.mode {
	case ModeAuto:
		return true
	case ModeDeny:
		return false
	}
	return s.askUser(i18n.T("perm.web", target))
}

// askUser prompts the user and interprets the reply. "a" switches the service
// into auto mode; "y"/"yes" approves; anything else (including an input error)
// rejects.
func (s *Service) askUser(prompt string) bool {
	resp, err := s.input(prompt)
	if err != nil {
		return false
	}
	resp = strings.ToLower(strings.TrimSpace(resp))
	if resp == "a" {
		s.mode = ModeAuto
		return true
	}
	return resp == "y" || resp == "yes"
}
