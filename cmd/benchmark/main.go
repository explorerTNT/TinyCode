// Command benchmark measures model performance on coding tasks.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/explorerTNT/TinyCode/internal/agent"
	"github.com/explorerTNT/TinyCode/internal/config"
)

// task is one benchmark job. setup prepares the workspace, verify checks
// the result afterwards: a run only counts as passed when the model's work
// is really there.
type task struct {
	Name   string
	Prompt string
	setup  func(workspace string) error
	verify func(workspace string, turn agent.TurnResult) error
}

var tasks = []task{
	{Name: "hello world",
		Prompt: "Write a Python hello world script that prints 'Hello from tiny-code!' and saves it to hello.py",
		verify: fileContains("hello.py", "Hello from tiny-code!")},
	{Name: "fibonacci",
		Prompt: "Write a Python function that returns the nth Fibonacci number using dynamic programming. Save it to fib.py",
		verify: fileContains("fib.py", "def ")},
	{Name: "file read",
		Prompt: "Read the file hello.py and tell me what it contains",
		verify: toolSucceeded("read_file")},
	{Name: "search",
		Prompt: "Search for all .py files in the current directory and list their names",
		verify: toolSucceeded("list_files")},
	// The tasks below exercise the crutches a small model runs on: a
	// check after the edit, the project map and the instruction file.
	{Name: "fix bug",
		Prompt: "calc.py has a bug: the test in test_calc.py fails. Fix calc.py so that the test passes, then run the test to confirm.",
		setup:  writeFixtures("calc.py", "def add(a, b):\n    return a - b\n", "test_calc.py", "from calc import add\n\nassert add(2, 3) == 5\nprint('ok')\n"),
		verify: allOf(fileContains("calc.py", "return a + b"), toolSucceeded("run_bash"))},
	{Name: "two files",
		Prompt: "Create main.py that imports greet from greet.py and prints greet('world') when run.",
		setup:  writeFixtures("greet.py", "def greet(name):\n    return 'hello ' + name\n"),
		verify: fileContains("main.py", "import")},
	{Name: "instructions",
		Prompt: "Write a script hello.py that prints hi.",
		setup:  writeFixtures("AGENTS.md", "# Project rules\n\nEvery new Python file starts with the line `# tinycode-demo`.\n"),
		verify: fileStartsWith("hello.py", "# tinycode-demo")},
}

func writeFixtures(files ...string) func(string) error {
	return func(workspace string) error {
		for i := 0; i+1 < len(files); i += 2 {
			path := filepath.Join(workspace, filepath.FromSlash(files[i]))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(files[i+1]), 0o644); err != nil {
				return err
			}
		}
		return nil
	}
}

func fileStartsWith(rel, prefix string) func(string, agent.TurnResult) error {
	return func(workspace string, _ agent.TurnResult) error {
		data, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		if !strings.HasPrefix(strings.TrimPrefix(string(data), "\uFEFF"), prefix) {
			return fmt.Errorf("%s does not start with %q", rel, prefix)
		}
		return nil
	}
}

// allOf passes only when every check passes and names the first failure.
func allOf(checks ...func(string, agent.TurnResult) error) func(string, agent.TurnResult) error {
	return func(workspace string, turn agent.TurnResult) error {
		for _, check := range checks {
			if err := check(workspace, turn); err != nil {
				return err
			}
		}
		return nil
	}
}

func fileContains(rel, want string) func(string, agent.TurnResult) error {
	return func(workspace string, _ agent.TurnResult) error {
		data, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), want) {
			return fmt.Errorf("%s does not contain %q", rel, want)
		}
		return nil
	}
}

// toolSucceeded passes when the tool ran without an error and the model then
// wrapped the turn up by itself, not because it ran out of rounds.
func toolSucceeded(tool string) func(string, agent.TurnResult) error {
	return func(_ string, turn agent.TurnResult) error {
		if turn.Tools[tool] == 0 {
			return fmt.Errorf("%s never succeeded (%d errors)", tool, turn.ToolErrors)
		}
		if turn.Outcome != agent.OutcomeRespond && turn.Outcome != agent.OutcomeDone {
			return fmt.Errorf("turn ended as %s after %d rounds", turn.Outcome, turn.Rounds)
		}
		return nil
	}
}

type result struct {
	Task             string  `json:"task"`
	Status           string  `json:"status"`
	Outcome          string  `json:"outcome"`
	Rounds           int     `json:"rounds"`
	ToolCalls        int     `json:"tool_calls"`
	ToolErrors       int     `json:"tool_errors"`
	ElapsedS         float64 `json:"elapsed_s"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TokenRate        float64 `json:"token_rate"`
	Error            string  `json:"error,omitempty"`
}

func main() {
	var (
		model     = flag.String("model", "", "model name to benchmark")
		quick     = flag.Bool("quick", false, "run only 2 tasks")
		workspace = flag.String("workspace", "", "temp workspace")
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

	ws := *workspace
	if ws == "" {
		ws = filepath.Join(cfg.TN.Workspace, ".bench_work")
	}
	if err := os.MkdirAll(ws, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}
	cfg.TN.Workspace = ws
	cfg.TN.PermissionMode = "auto"

	chosen := tasks
	if *quick {
		chosen = tasks[:2]
	}

	fmt.Println("tiny-code benchmark")
	fmt.Printf("Model: %s\n", cfg.LM.Name)
	fmt.Printf("Workspace: %s\n", ws)
	fmt.Printf("Tasks: %d\n", len(chosen))

	results, total := runBenchmark(cfg, chosen)
	printSummary(results, total)

	out := map[string]any{
		"model":         cfg.LM.Name,
		"results":       results,
		"total_elapsed": total,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	_ = os.WriteFile("benchmark_result.json", b, 0o644)
	fmt.Println("\nResults saved to benchmark_result.json")
}

func runBenchmark(cfg *config.Config, tasks []task) ([]result, float64) {
	totalStart := time.Now()
	var results []result

	for _, t := range tasks {
		fmt.Printf("\n%s\nBenchmark: %s\n%s\n", strings.Repeat("=", 60), t.Name, strings.Repeat("=", 60))

		if t.setup != nil {
			if err := t.setup(cfg.TN.Workspace); err != nil {
				results = append(results, result{Task: t.Name, Status: "error", Outcome: "error", Error: err.Error()})
				continue
			}
		}
		ag, err := agent.New(cfg, agent.NewConsoleIO())
		if err != nil {
			results = append(results, result{Task: t.Name, Status: "error", Outcome: "error", Error: err.Error()})
			continue
		}

		roundStart := time.Now()
		ag.RunOnce(t.Prompt)
		elapsed := time.Since(roundStart).Seconds()

		turn := ag.LastTurn()
		r := result{
			Task: t.Name, Status: "ok", Outcome: turn.Outcome,
			Rounds: turn.Rounds, ToolCalls: turn.ToolCalls, ToolErrors: turn.ToolErrors,
			ElapsedS: round2(elapsed),
		}
		r.PromptTokens, r.CompletionTokens = ag.TokenCounts()
		if elapsed > 0 {
			r.TokenRate = round2(float64(r.CompletionTokens) / elapsed)
		}
		if err := t.verify(cfg.TN.Workspace, turn); err != nil {
			r.Status = "fail"
			r.Error = err.Error()
		}
		results = append(results, r)

		fmt.Printf("\n  Time: %.2fs | Prompt tok: %d | Completion tok: %d | Rate: %.2f tok/s\n", elapsed, r.PromptTokens, r.CompletionTokens, r.TokenRate)
		fmt.Printf("  %s: %s, %d rounds, %d tool calls, %d errors\n", r.Status, r.Outcome, r.Rounds, r.ToolCalls, r.ToolErrors)
		if r.Error != "" {
			fmt.Printf("  %s\n", r.Error)
		}
	}

	return results, time.Since(totalStart).Seconds()
}

func printSummary(results []result, total float64) {
	line := strings.Repeat("-", 60)
	fmt.Printf("\n%s\nBenchmark Summary\n%s\n", strings.Repeat("=", 60), strings.Repeat("=", 60))

	passed, failed := 0, 0
	var timeSum, rateSum float64
	for _, r := range results {
		if r.Status == "ok" {
			passed++
			timeSum += r.ElapsedS
			rateSum += r.TokenRate
		} else {
			failed++
		}
	}

	fmt.Printf("Total time: %.2fs\n", total)
	fmt.Printf("Tasks: %d passed, %d failed\n", passed, failed)
	if passed > 0 {
		fmt.Printf("Avg task time: %.2fs\n", timeSum/float64(passed))
		fmt.Printf("Avg token rate: %.2f tok/s\n", rateSum/float64(passed))
	}

	fmt.Printf("\n%s\n", line)
	fmt.Printf("%-12s %-14s %-6s %-8s %-5s %-5s %-9s\n", "Task", "Outcome", "Status", "Time", "Rnds", "Calls", "Tokens/s")
	fmt.Println(line)
	for _, r := range results {
		rate := "-"
		if r.Status == "ok" {
			rate = fmt.Sprintf("%.1f", r.TokenRate)
		}
		fmt.Printf("%-12s %-14s %-6s %-8s %-5d %-5d %-9s\n", r.Task, r.Outcome, r.Status, fmt.Sprintf("%.2fs", r.ElapsedS), r.Rounds, r.ToolCalls, rate)
		if r.Error != "" {
			fmt.Printf("  %s\n", r.Error)
		}
	}
	fmt.Println(line)
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
