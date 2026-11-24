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

// task is one benchmark job. verify checks the result afterwards: a run only
// counts as passed when the model's work is really there.
type task struct {
	Name   string
	Prompt string
	verify func(workspace string, turn agent.TurnResult) error
}

var tasks = []task{
	{"hello world", "Write a Python hello world script that prints 'Hello from tiny-code!' and saves it to hello.py",
		fileContains("hello.py", "Hello from tiny-code!")},
	{"fibonacci", "Write a Python function that returns the nth Fibonacci number using dynamic programming. Save it to fib.py",
		fileContains("fib.py", "def ")},
	{"file read", "Read the file hello.py and tell me what it contains",
		toolSucceeded("read_file")},
	{"search", "Search for all .py files in the current directory and list their names",
		toolSucceeded("list_files")},
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
