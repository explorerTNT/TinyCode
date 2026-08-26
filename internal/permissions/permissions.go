package permissions

import (
	"slices"
	"strings"

	"github.com/explorerTNT/TinyCode/internal/tools"
)

// Mode is the permission enforcement policy.
type Mode int8

const (
	ModeAuto Mode = iota + 1
	ModeDeny
	ModeAsk
)

const (
	dangerousChars     = "><|;&`\n\r"
	dangerousSubString = "$("
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

// Service enforces write and shell permissions, prompting the user in "ask"
// mode through the injected InputFunc.
type Service struct {
	mode                        Mode
	approvedBashPatterns        []string
	autoApproveReadOnlyCommands bool
	input                       InputFunc
}

// New builds a Service. mode==0 means ModeAsk. A nil input falls back to a
// stdin reader.
func New(mode Mode, input InputFunc) *Service {
	if mode == 0 {
		mode = ModeAsk
	}
	if input == nil {
		input = defaultInput
	}
	return &Service{
		mode:                        mode,
		autoApproveReadOnlyCommands: true,
		input:                       input,
	}
}

// SetInput swaps the input source at runtime (used to rewire the console
// reader once a TUI takes over, and vice versa).
func (s *Service) SetInput(input InputFunc) {
	s.input = input
}

// Mode returns the current enforcement policy.
func (s *Service) Mode() Mode { return s.mode }

// CheckBash decides whether a shell command may run.
func (s *Service) CheckBash(command string) bool {
	switch s.mode {
	case ModeAuto:
		return true
	case ModeDeny:
		return false
	}

	stripped := strings.ToLower(strings.TrimSpace(command))

	if s.autoApproveReadOnlyCommands && isCommandReadOnly(stripped) {
		return true
	}

	for _, pattern := range s.approvedBashPatterns {
		if strings.HasPrefix(stripped, pattern) {
			return true
		}
	}

	return s.askUser("Run this command?\n  $ " + command + "\nApprove? (y/n/a always) ")
}

// CheckWrite decides whether a file write may proceed.
func (s *Service) CheckWrite(path string) bool {
	switch s.mode {
	case ModeAuto:
		return true
	case ModeDeny:
		return false
	}
	return s.askUser("Write to " + path + "? (y/n) ")
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

func containsDangerous(s string) bool {
	return strings.Contains(s, dangerousSubString) || strings.ContainsAny(s, dangerousChars)
}

func isCommandReadOnly(command string) bool {
	if containsDangerous(command) {
		return false
	}

	elems := strings.Fields(command)
	if len(elems) == 0 {
		return false
	}
	if len(elems) <= 2 && slices.Contains(tools.ReadOnlySingleCommands, elems[0]) {
		return true
	}
	if len(elems) >= 2 && slices.Contains(tools.ReadOnlyPairsCommands, [2]string{elems[0], elems[1]}) {
		return true
	}
	return false
}
