import fnmatch
import os
import re
import shutil
import subprocess
from pathlib import Path

from .glob import EXCLUDE_DIRS, NOISE_EXTS

MAX_RESULTS = 50
MATCHES_PER_FILE = 5

# Resolved on first use rather than at import time: the old module-level call
# ran `where rg` (a 5s subprocess) during startup, before anything was shown.
_RG_PATH = None
_RG_LOOKED_UP = False


def search_files(pattern: str, path: str = ".", include: str = None) -> str:
    """Search file contents using a regular expression pattern (grep-like).

    Args:
        pattern: The regex pattern to search for
        path: Directory to search in (default: current workspace)
        include: Optional file glob pattern to filter (e.g. "*.py")
    """
    if not pattern:
        return "Error: empty search pattern"

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

        result_rg = _try_rg(pattern, search_path, include)
        if result_rg is not None:
            return result_rg

        return _fallback_grep(pattern, search_path, include)
    except subprocess.TimeoutExpired:
        return "Error: Search timed out after 30 seconds"
    except FileNotFoundError:
        return _fallback_grep(pattern, Path(path).resolve(), include)
    except Exception as e:
        return f"Error searching files: {e}"


def _grep_single_file(pattern: str, filepath: Path) -> str:
    try:
        regex = re.compile(pattern, re.IGNORECASE)
    except re.error as e:
        return f"Error: Invalid regex '{pattern}': {e}"

    results = []
    truncated = False
    try:
        with open(filepath, "r", encoding="utf-8", errors="replace") as f:
            for ln, line in enumerate(f, 1):
                if regex.search(line):
                    if len(results) >= MAX_RESULTS:
                        truncated = True
                        break
                    results.append(f"{filepath.name}:{ln}: {line.rstrip()[:200]}")
    except OSError as e:
        return f"Error reading {filepath.name}: {e}"

    if not results:
        return f"No matches for '{pattern}' in {filepath.name}"
    header = f"--- {len(results)}{'+' if truncated else ''} match(es) in {filepath.name} ---"
    body = "\n".join(results)
    if truncated:
        body += f"\n[stopped at {MAX_RESULTS} matches - narrow your pattern]"
    return f"{header}\n{body}"


def _find_rg() -> str | None:
    """Locate ripgrep, preferring whatever is on PATH.

    shutil.which replaces a `where rg` subprocess, and the WinGet fallback no
    longer hard-codes a version number that breaks on the next update.
    """
    found = shutil.which("rg")
    if found:
        return found

    local = os.environ.get("LOCALAPPDATA", "")
    if local:
        pkg_root = Path(local) / "Microsoft" / "WinGet" / "Packages"
        try:
            for pkg in pkg_root.glob("BurntSushi.ripgrep*"):
                for candidate in pkg.glob("**/rg.exe"):
                    return str(candidate)
        except OSError:
            pass

    program_files = os.environ.get("PROGRAMFILES", "")
    if program_files:
        candidate = Path(program_files) / "ripgrep" / "rg.exe"
        if candidate.exists():
            return str(candidate)
    return None


def _rg_path() -> str | None:
    global _RG_PATH, _RG_LOOKED_UP
    if not _RG_LOOKED_UP:
        _RG_LOOKED_UP = True
        try:
            _RG_PATH = _find_rg()
        except Exception:
            _RG_PATH = None
    return _RG_PATH


def _glob_for(include: str) -> str:
    """Turn a loose `include` value into a ripgrep glob.

    `str.lstrip('*.')` was used here, which strips a *character set* rather
    than a prefix: "test_*.py" lost nothing but "*.py" and ".py" and "py" all
    had to be special-cased, and any name starting with '*' or '.' was mangled.
    """
    value = (include or "").strip()
    if not value:
        return ""
    if any(ch in value for ch in "*?["):
        return value
    if value.startswith("."):
        # ".py" means the extension.
        return f"*{value}"
    if "." in value:
        # "main.py" is already a concrete filename.
        return value
    # A bare word is an extension ("py") only when it looks like one. A
    # capitalised or long word is far more likely a real filename, and
    # turning "Makefile" into "*.Makefile" matched nothing at all.
    if value.isalnum() and value.islower() and len(value) <= 5:
        return f"*.{value}"
    return value


def _try_rg(pattern: str, path: Path, include: str = None) -> str | None:
    rg_bin = _rg_path()
    if not rg_bin:
        return None
    try:
        # -i keeps ripgrep consistent with the pure-Python fallback, which has
        # always been case-insensitive. Without it the same query returned
        # different results depending on whether ripgrep was installed.
        cmd = [rg_bin, "-n", "-i", "--no-heading", "-m", str(MATCHES_PER_FILE)]
        glob = _glob_for(include)
        if glob:
            cmd.extend(["-g", glob])
        cmd.extend(["--", pattern, str(path)])

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
        shown = lines[:MAX_RESULTS]
        # The old header printed len(lines) as if it were the total, but -m
        # caps matches per file and the list is cut at MAX_RESULTS, so the
        # number was wrong in both directions. Report what is actually shown.
        header = f"--- showing {len(shown)} match(es)"
        if len(lines) > MAX_RESULTS:
            header += f" of {len(lines)}+ found"
        header += f" (max {MATCHES_PER_FILE} per file) ---"
        body = "\n".join(shown)
        if len(lines) > MAX_RESULTS:
            body += f"\n[{len(lines) - MAX_RESULTS} more not shown - narrow your search]"
        return f"{header}\n{body}"
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return None
    except Exception:
        return None


def _fallback_grep(pattern: str, search_path: Path, include: str = None) -> str:
    regex = re.compile(pattern, re.IGNORECASE)
    glob = _glob_for(include)
    results = []
    per_file: dict[Path, int] = {}
    truncated = False

    for root, dirs, files in os.walk(str(search_path)):
        dirs[:] = [d for d in dirs if d not in EXCLUDE_DIRS and not d.startswith(".")]
        if truncated:
            break
        for name in files:
            if glob and not fnmatch.fnmatch(name, glob):
                continue
            filepath = Path(root) / name
            if filepath.suffix.lower() in NOISE_EXTS:
                continue
            try:
                with open(filepath, "r", encoding="utf-8", errors="replace") as f:
                    for ln, line in enumerate(f, 1):
                        if not regex.search(line):
                            continue
                        # Mirror ripgrep's -m so both backends return a
                        # comparable spread of files rather than 50 hits
                        # from whichever file happened to be scanned first.
                        if per_file.get(filepath, 0) >= MATCHES_PER_FILE:
                            break
                        per_file[filepath] = per_file.get(filepath, 0) + 1
                        rel = filepath.relative_to(search_path)
                        results.append(f"{rel}:{ln}: {line.rstrip()[:200]}")
                        if len(results) >= MAX_RESULTS:
                            truncated = True
                            break
            except (OSError, ValueError):
                continue
            if truncated:
                break

    if not results:
        return f"No matches for '{pattern}' in {search_path}"
    header = f"--- showing {len(results)} match(es) (max {MATCHES_PER_FILE} per file) ---"
    body = "\n".join(results)
    if truncated:
        body += f"\n[stopped at {MAX_RESULTS} matches - narrow your search]"
    return f"{header}\n{body}"
