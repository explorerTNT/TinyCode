// Command tiny-code is a lightweight local AI coding agent.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/explorerTNT/TinyCode/internal/agent"
	"github.com/explorerTNT/TinyCode/internal/config"
	"github.com/explorerTNT/TinyCode/internal/i18n"
	"github.com/explorerTNT/TinyCode/internal/tui"
	"github.com/explorerTNT/TinyCode/internal/updater"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var (
		model       = flag.String("model", "", "override model name")
		workspace   = flag.String("workspace", "", "workspace directory")
		permission  = flag.String("permission", "", "permission mode: auto|ask|deny")
		resume      = flag.Bool("resume", false, "resume the last session")
		session     = flag.String("session", "", "session name to resume (used with -resume)")
		lang        = flag.String("lang", "", "interface language: en|ru")
		showVersion = flag.Bool("version", false, "print version and exit")
		doUpdate    = flag.Bool("update", false, "check for updates and install if available")
		noUpdate    = flag.Bool("no-update", false, "skip update check on startup")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s\n", i18n.T("main.version", version))
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", i18n.T("main.config_error", err))
		os.Exit(1)
	}

	if *lang != "" {
		cfg.Language = *lang
	}
	i18n.Set(i18n.Parse(cfg.Language))

	// --- update mode ---
	if *doUpdate {
		runUpdate()
		return
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

	if cfg.TN.Workspace == "" {
		if wd, err := os.Getwd(); err == nil {
			cfg.TN.Workspace = wd
		}
	}

	// --- background update check (console mode only; the TUI shows it in-app) ---
	updateEnabled := !*noUpdate && !updater.IsDev(version)
	prompt := strings.Join(flag.Args(), " ")

	if prompt != "" {
		if updateEnabled {
			go checkUpdate()
		}
		runOnce(cfg, prompt)
		return
	}

	cfg.Version = version
	cfg.UpdateCheck = updateEnabled

	var onReady func(*agent.Agent)
	if *resume {
		onReady = func(a *agent.Agent) {
			if !a.ResumeSession(*session) {
				a.IO().Println(i18n.T("main.no_session"))
			}
		}
	}

	if err := tui.Run(cfg, onReady); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", i18n.T("main.tui_error", err))
		os.Exit(1)
	}
}

func runOnce(cfg *config.Config, prompt string) {
	io := agent.NewConsoleIO()
	ag, err := agent.New(cfg, io)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", i18n.T("main.agent_error", err))
		os.Exit(1)
	}
	ag.RunOnce(prompt)
}

func checkUpdate() {
	release, err := updater.CheckLatest()
	if err != nil {
		return
	}
	if updater.Compare(release.Tag, version) <= 0 {
		return
	}
	fmt.Fprintf(os.Stderr, i18n.T("update.available")+"\n", release.Tag, version)
}

func runUpdate() {
	fmt.Fprintf(os.Stderr, i18n.T("update.downloading")+"\n", "latest")
	release, err := updater.CheckLatest()
	if err != nil {
		fmt.Fprintf(os.Stderr, i18n.T("update.failed")+"\n", err)
		os.Exit(1)
	}

	if updater.Compare(release.Tag, version) <= 0 {
		fmt.Fprintf(os.Stderr, i18n.T("update.latest")+"\n", version)
		return
	}

	if err := updater.Install(release); err != nil {
		fmt.Fprintf(os.Stderr, i18n.T("update.failed")+"\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, i18n.T("update.done")+"\n", release.Tag)
}
