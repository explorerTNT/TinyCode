// Command benchmark measures model performance on coding tasks.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/explorerTNT/TinyCode/internal/agent"
	"github.com/explorerTNT/TinyCode/internal/config"
)

type task struct {
	Name   string
	Prompt string
}

var tasks = []task{
	{"hello world", "Write a Python hello world script that prints 'Hello from tiny-code!' and saves it to hello.py"},
	{"fibonacci", "Write a Python function that returns the nth Fibonacci number using dynamic programming. Save it to fib.py"},
	{"file read", "Read the file hello.py and tell me what it contains"},
	{"search", "Search for all .py files in the current directory and list their names"},
}

type result struct {
	Task             string  `json:"task"`
	Status           string  `json:"status"`
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
		fmt.Printf("\n%s\n", stringsRepeat("=", 60))
		fmt.Printf("Benchmark: %s\n", t.Name)
		fmt.Printf("%s\n", stringsRepeat("=", 60))

		io := agent.NewConsoleIO()
		ag, err := agent.New(cfg, io)
		if err != nil {
			results = append(results, result{Task: t.Name, Status: "error", Error: err.Error()})
			continue
		}

		roundStart := time.Now()
		ag.RunOnce(t.Prompt)
		elapsed := time.Since(roundStart).Seconds()

		r := result{Task: t.Name, Status: "ok", ElapsedS: round2(elapsed)}
		r.PromptTokens, r.CompletionTokens = tokenCounts(ag)
		if elapsed > 0 {
			r.TokenRate = round2(float64(r.CompletionTokens) / elapsed)
		}
		results = append(results, r)

		fmt.Printf("\n  Time: %.2fs | Prompt tok: %d | Completion tok: %d | Rate: %.2f tok/s\n",
			elapsed, r.PromptTokens, r.CompletionTokens, r.TokenRate)
	}

	return results, time.Since(totalStart).Seconds()
}

func tokenCounts(ag *agent.Agent) (int, int) {
	return ag.TokenCounts()
}

func printSummary(results []result, total float64) {
	fmt.Printf("\n%s\n", stringsRepeat("=", 60))
	fmt.Println("Benchmark Summary")
	fmt.Printf("%s\n", stringsRepeat("=", 60))

	ok, failed := 0, 0
	var timeSum, rateSum float64
	for _, r := range results {
		if r.Status == "ok" {
			ok++
			timeSum += r.ElapsedS
			rateSum += r.TokenRate
		} else {
			failed++
		}
	}

	fmt.Printf("Total time: %.2fs\n", total)
	fmt.Printf("Tasks: %d ok, %d failed\n", ok, failed)
	if ok > 0 {
		fmt.Printf("Avg task time: %.2fs\n", timeSum/float64(ok))
		fmt.Printf("Avg token rate: %.2f tok/s\n", rateSum/float64(ok))
	}

	fmt.Printf("\n%s\n", stringsRepeat("─", 60))
	fmt.Printf("%-20s %-10s %-10s %-10s\n", "Task", "Status", "Time", "Tokens/s")
	fmt.Printf("%s\n", stringsRepeat("─", 60))
	for _, r := range results {
		rate := "-"
		if r.Status == "ok" {
			rate = fmt.Sprintf("%.1f", r.TokenRate)
		}
		fmt.Printf("%-20s %-10s %-10s %-10s\n", r.Task, r.Status, fmt.Sprintf("%.2fs", r.ElapsedS), rate)
	}
	fmt.Printf("%s\n", stringsRepeat("─", 60))
}

func stringsRepeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
