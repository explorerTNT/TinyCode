import os
import subprocess
from pathlib import Path


def search_files(pattern: str, path: str = ".", include: str = None) -> str:
    """Search file contents using a regular expression pattern (grep-like).

    Args:
        pattern: The regex pattern to search for
        path: Directory to search in (default: current workspace)
        include: Optional file glob pattern to filter (e.g. "*.py")
    """
    import re

    try:
        re.compile(pattern)
    except re.error as e:
        return (
            f"Error: Invalid regex '{pattern}': {e}. "
            "Escape regex metacharacters like ( ) [ ] * + ? to search for them literally."
        )

    try:
        search_path = Path(path).resolve()
        if not search_path.exists():
            return f"Error: Path not found: {path}"
        # Small models pass a file instead of a directory; grepping that single
        # file is obviously what they meant, so just do it.
        if search_path.is_file():
            return _grep_single_file(pattern, search_path)
        if not search_path.is_dir():
            return f"Error: Not a directory: {path}"

        result_rg = _try_rg(pattern, str(search_path), include)
        if result_rg is not None:
            return result_rg

        return _fallback_grep(pattern, path, include)
    except subprocess.TimeoutExpired:
        return f"Error: Search timed out after 30 seconds"
    except FileNotFoundError:
        return _fallback_grep(pattern, path, include)


def _grep_single_file(pattern: str, filepath: Path) -> str:
    import re

    try:
        regex = re.compile(pattern, re.IGNORECASE)
    except re.error as e:
        return f"Error: Invalid regex '{pattern}': {e}"

    results = []
    try:
        with open(filepath, "r", encoding="utf-8", errors="replace") as f:
            for ln, line in enumerate(f, 1):
                if regex.search(line):
                    results.append(f"{filepath.name}:{ln}: {line.rstrip()[:200]}")
                    if len(results) >= 50:
                        break
    except OSError as e:
        return f"Error reading {filepath.name}: {e}"

    if not results:
        return f"No matches for '{pattern}' in {filepath.name}"
    return f"--- {len(results)} match(es) in {filepath.name} ---\n" + "\n".join(results)


def _find_rg() -> str | None:
    try:
        import subprocess as _sp
        result = _sp.run(
            ["where", "rg"], capture_output=True, text=True,
            encoding="utf-8", errors="replace", timeout=5,
        )
        if result.returncode == 0:
            return result.stdout.strip().split("\n")[0]
    except Exception:
        pass
    candidates = [
        Path(os.environ.get("LOCALAPPDATA", "")) / "Microsoft" / "WinGet" / "Packages" / "BurntSushi.ripgrep.MSVC_Microsoft.Winget.Source_8wekyb3d8bbwe" / "ripgrep-15.1.0-x86_64-pc-windows-msvc" / "rg.exe",
        Path(os.environ.get("PROGRAMFILES", "")) / "ripgrep" / "rg.exe",
    ]
    for c in candidates:
        if c.exists():
            return str(c)
    return None


_RG_PATH = _find_rg()


def _try_rg(pattern: str, path: str, include: str = None) -> str | None:
    rg_bin = _RG_PATH or "rg"
    try:
        # -i keeps ripgrep consistent with the pure-Python fallback, which has
        # always been case-insensitive. Without it the same query returned
        # different results depending on whether ripgrep was installed.
        cmd = [rg_bin, "-n", "-i", pattern, str(path)]
        if include:
            glob = f"*.{include.lstrip('*.')}" if "." not in include else include
            cmd.extend(["-g", glob])
        cmd.extend(["--no-heading", "-m", "5"])

        result = subprocess.run(
            cmd, capture_output=True, text=True,
            encoding="utf-8", errors="replace", timeout=30,
        )
        if result.returncode not in (0, 1):
            return None

        output = result.stdout.rstrip() if result.stdout else ""
        if not output:
            # rg exited cleanly with no hits. Returning None here made the
            # caller redo the whole scan in Python just to reach the same
            # answer, which on a large tree costs seconds for nothing.
            return f"No matches for '{pattern}' in {path}"

        lines = output.split("\n")
        capped = lines[:50]
        summary = f"--- {len(lines)} match(es) ---\n" + "\n".join(capped)
        return summary
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return None
    except Exception as e:
        return f"Error searching files: {e}"


def _fallback_grep(pattern: str, path: str, include: str = None) -> str:
    import re
    search_path = Path(path).resolve()
    regex = re.compile(pattern, re.IGNORECASE)
    results = []

    for filepath in search_path.rglob("*"):
        if not filepath.is_file():
            continue
        if include and not filepath.suffix == f".{include.lstrip('*.')}":
            continue
        if filepath.suffix in (".pyc", ".exe", ".dll", ".png", ".jpg", ".o", ".so"):
            continue
        if any(p.startswith(".") for p in filepath.relative_to(search_path).parts):
            continue

        try:
            with open(filepath, "r", encoding="utf-8", errors="replace") as f:
                for ln, line in enumerate(f, 1):
                    if regex.search(line):
                        rel = filepath.relative_to(search_path)
                        results.append(f"{rel}:{ln}: {line.rstrip()[:200]}")
                        if len(results) >= 50:
                            break
            if len(results) >= 50:
                break
        except Exception:
            pass

    if not results:
        return f"No matches for '{pattern}' in {path}"
    return f"--- {len(results)} matches ---\n" + "\n".join(results[:50]) + ("\n..." if len(results) > 50 else "")
