// Command tiny-code is a lightweight local AI coding agent.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/explorerTNT/TinyCode/internal/agent"
	"github.com/explorerTNT/TinyCode/internal/config"
	"github.com/explorerTNT/TinyCode/internal/tui"
)

func main() {
	var (
		model      = flag.String("model", "", "override model name")
		workspace  = flag.String("workspace", "", "workspace directory")
		permission = flag.String("permission", "", "permission mode: auto|ask|deny")
		resume     = flag.String("resume", "", "resume a session (last one if empty)")
	)
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	if *model != "" {
		cfg.LM.Name = *model
	}
	if *workspace != "" {
		cfg.TN.Workspace = *workspace
	}
	if *permission != "" {
		cfg.TN.PermissionMode = *permission
	}

	prompt := strings.Join(flag.Args(), " ")

	if prompt != "" {
		runOnce(cfg, prompt)
		return
	}

	var onReady func(*agent.Agent)
	if resumeFlagSet() {
		onReady = func(a *agent.Agent) {
			if !a.ResumeSession(*resume) {
				a.IO().Println("  [no session to resume]\n")
			}
		}
	}

	if err := tui.Run(cfg, onReady); err != nil {
		fmt.Fprintf(os.Stderr, "tui error: %v\n", err)
		os.Exit(1)
	}
}

func resumeFlagSet() bool {
	set := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "resume" {
			set = true
		}
	})
	return set
}

func runOnce(cfg *config.Config, prompt string) {
	io := agent.NewConsoleIO()
	ag, err := agent.New(cfg, io)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
		os.Exit(1)
	}
	ag.RunOnce(prompt)
}
