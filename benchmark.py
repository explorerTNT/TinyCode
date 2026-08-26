"""Benchmark: measure tiny-code model performance on real coding tasks."""

import json
import time
from pathlib import Path

from agent import TinyCodeAgent
from config import Config
from system_prompt import SYSTEM_PROMPT


TASKS = [
    {
        "name": "hello world",
        "prompt": "Write a Python hello world script that prints 'Hello from tiny-code!' and saves it to hello.py",
    },
    {
        "name": "fibonacci",
        "prompt": "Write a Python function that returns the nth Fibonacci number using dynamic programming. Save it to fib.py",
    },
    {
        "name": "file read",
        "prompt": "Read the file hello.py and tell me what it contains",
    },
    {
        "name": "search",
        "prompt": "Search for all .py files in the current directory and list their names",
    },
]


def run_benchmark(config: Config, tasks: list) -> tuple[list, float]:
    results = []
    total_start = time.time()

    for task in tasks:
        print(f"\n{'='*60}")
        print(f"Benchmark: {task['name']}")
        print(f"{'='*60}")

        agent = TinyCodeAgent(config)
        # _add_msg keeps the context estimate in sync; appending directly
        # leaves it at zero, so trimming never kicks in during a long run.
        agent._add_msg({"role": "system", "content": SYSTEM_PROMPT})
        agent._add_msg({"role": "user", "content": task["prompt"]})

        round_start = time.time()

        try:
            agent._process_turn(max_rounds=5)

            elapsed = time.time() - round_start

            prompt_tokens = 0
            completion_tokens = 0
            for m in agent.messages:
                c = json.dumps(m, default=str)
                if m.get("role") == "user":
                    prompt_tokens += len(c) // 4
                elif m.get("role") == "assistant":
                    completion_tokens += len(c) // 4

            results.append({
                "task": task["name"],
                "status": "ok",
                "elapsed_s": round(elapsed, 2),
                "prompt_tokens": prompt_tokens,
                "completion_tokens": completion_tokens,
                "token_rate": round(completion_tokens / elapsed, 2) if elapsed > 0 else 0,
            })

            print(f"\n  Time: {elapsed:.2f}s | Prompt tok: {prompt_tokens} | Completion tok: {completion_tokens} | Rate: {results[-1]['token_rate']} tok/s")

        except Exception as e:
            elapsed = time.time() - round_start
            results.append({
                "task": task["name"],
                "status": "error",
                "elapsed_s": round(elapsed, 2),
                "error": str(e),
            })
            print(f"\n  Error: {e}")

    total_elapsed = time.time() - total_start
    return results, total_elapsed


def print_summary(results: list, total_elapsed: float):
    print(f"\n{'='*60}")
    print("Benchmark Summary")
    print(f"{'='*60}")

    ok = [r for r in results if r["status"] == "ok"]
    failed = [r for r in results if r["status"] == "error"]

    print(f"Total time: {total_elapsed:.2f}s")
    print(f"Tasks: {len(ok)} ok, {len(failed)} failed")

    if ok:
        avg_time = sum(r["elapsed_s"] for r in ok) / len(ok)
        avg_rate = sum(r.get("token_rate", 0) for r in ok) / len(ok)
        print(f"Avg task time: {avg_time:.2f}s")
        print(f"Avg token rate: {avg_rate:.2f} tok/s")

    print(f"\n{'─'*60}")
    print(f"{'Task':<20} {'Status':<10} {'Time':<10} {'Tokens/s':<10}")
    print(f"{'─'*60}")
    for r in results:
        status = r["status"]
        t = f"{r['elapsed_s']:.2f}s"
        rate = f"{r.get('token_rate', 0):.1f}" if status == "ok" else "-"
        print(f"{r['task']:<20} {status:<10} {t:<10} {rate:<10}")
    print(f"{'─'*60}")


def main():
    import argparse

    parser = argparse.ArgumentParser(description="Benchmark tiny-code model performance")
    parser.add_argument("--model", help="Model name to benchmark")
    parser.add_argument("--quick", action="store_true", help="Run only 2 tasks")
    parser.add_argument("--workspace", type=Path, default=Path.cwd() / ".bench_work", help="Temp workspace")

    args = parser.parse_args()
    config = Config.from_env()

    if args.model:
        config.model_name = args.model

    ws = args.workspace.resolve()
    ws.mkdir(parents=True, exist_ok=True)
    config.workspace = ws

    tasks = TASKS[:2] if args.quick else TASKS

    print(f"tiny-code benchmark")
    print(f"Model: {config.model_name}")
    print(f"Workspace: {ws}")
    print(f"Tasks: {len(tasks)}")

    results, total_elapsed = run_benchmark(config, tasks)

    print_summary(results, total_elapsed)

    # Written into the benchmark workspace, not the repository root: the old
    # path dropped an untracked artefact next to the source on every run.
    result_file = ws / "benchmark_result.json"
    with open(result_file, "w", encoding="utf-8") as f:
        json.dump(
            {"model": config.model_name, "results": results, "total_elapsed": total_elapsed},
            f, indent=2, ensure_ascii=False,
        )
    print(f"\nResults saved to {result_file}")


if __name__ == "__main__":
    main()

