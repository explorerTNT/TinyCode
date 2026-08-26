import fnmatch
import os
import time
from pathlib import Path


EXCLUDE_DIRS = {
    ".venv", "venv", ".git", "__pycache__", "node_modules", ".hg", ".svn",
    ".idea", ".vscode", ".tox", "build", "dist", ".next", ".turbo",
    "bin", "obj", "target", ".gradle", ".pytest_cache", ".mypy_cache",
    ".vs", ".vscode-test", ".cache", ".parcel-cache", "coverage", ".nuget",
}
NOISE_EXTS = {
    ".exe", ".dll", ".pdb", ".obj", ".lib", ".so", ".dylib", ".class",
    ".pyc", ".pyd", ".rar", ".zip", ".7z", ".gz", ".tar", ".nupkg",
    ".ico", ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp",
    ".mp3", ".mp4", ".avi", ".mkv", ".wav", ".ttf", ".otf", ".woff", ".woff2",
}
MAX_FILES = 20000
SCAN_TIMEOUT = 10
SUGGEST_TIMEOUT = 3


def list_files(pattern: str = "*", path: str = ".") -> str:
    """List files matching a glob pattern. Returns files sorted by modification time.

    Args:
        path: Directory to search in (default: current workspace)
        pattern: Glob pattern to match (default: "*")
    """
    try:
        search_path = Path(path).resolve()
        if not search_path.exists():
            return f"Error: Path not found: {path}"
        if not search_path.is_dir():
            return f"Error: Not a directory: {path}"

        return _fallback_glob(pattern or "*", search_path)
    except Exception as e:
        return f"Error listing files: {e}"


def _fallback_glob(pattern: str, search_path: Path) -> str:
    results = []
    deadline = time.time() + SCAN_TIMEOUT
    scanned = 0
    hidden = 0
    # An explicit pattern means the model asked for these files on purpose.
    explicit = pattern not in ("*", "*.*", "")

    # A pattern containing a separator addresses a path ("src/*.py"), which
    # never matches when only the bare file name is tested.
    norm_pattern = pattern.replace("\\", "/")
    path_scoped = "/" in norm_pattern

    for root, dirs, files in os.walk(str(search_path)):
        dirs[:] = [d for d in dirs if d not in EXCLUDE_DIRS]

        if time.time() > deadline:
            return f"Error: Scan timed out (> {SCAN_TIMEOUT}s). Narrow your search."

        for name in files:
            scanned += 1
            if scanned > MAX_FILES:
                return f"Error: Too many files ({MAX_FILES}+). Narrow your search."
            full = Path(root) / name
            try:
                rel = full.relative_to(search_path)
            except ValueError:
                continue
            if path_scoped:
                rel_posix = rel.as_posix()
                matched = fnmatch.fnmatch(rel_posix, norm_pattern)
                # "src/*.py" should also reach nested files, matching how the
                # model expects a directory-scoped glob to behave.
                if not matched and "**" not in norm_pattern:
                    matched = fnmatch.fnmatch(
                        rel_posix, norm_pattern.replace("/", "/*", 1)
                    )
            else:
                matched = fnmatch.fnmatch(name, pattern)
            if matched:
                if not explicit and Path(name).suffix.lower() in NOISE_EXTS:
                    hidden += 1
                    continue
                try:
                    stat = full.stat()
                except OSError:
                    continue
                results.append((rel, stat.st_size, stat.st_mtime))

    results.sort(key=lambda x: -x[2])

    if not results:
        return f"No files matching '{pattern}' in {search_path}"

    limit = 200 if "*" in pattern or "?" in pattern else 1000
    lines = [f"--- {len(results)} files matching '{pattern}' (newest first) ---"]
    for rel, size, mtime in results[:limit]:
        lines.append(
            f"{rel} ({_fmt_size(size)}, "
            f"{time.strftime('%Y-%m-%d %H:%M', time.localtime(mtime))})"
        )
    if len(results) > limit:
        lines.append(f"... and {len(results) - limit} more files")
    if hidden:
        lines.append(f"({hidden} binary/build files hidden)")
    return "\n".join(lines)


def suggest_files(basename: str, search_path: Path, max_results: int = 5) -> list[str]:
    """Find workspace files whose name matches a target basename (for 'did you mean').

    Matches on exact name, then on 'contains'. Ignores build/binary dirs.
    Returns relative paths, best matches first.
    """
    target = (basename or "").lower().replace("\\", "/").rstrip("/").split("/")[-1]
    if not target or target in (".", ".."):
        return []
    try:
        root_path = Path(search_path).resolve()
    except OSError:
        return []
    if not root_path.is_dir():
        return []

    exact = []
    contains = []
    # A shorter budget than the full scan: this runs on an error path, where a
    # ten-second stall reads like the agent has hung.
    deadline = time.time() + SUGGEST_TIMEOUT
    try:
        for root, dirs, files in os.walk(str(root_path)):
            dirs[:] = [d for d in dirs if d not in EXCLUDE_DIRS and not d.startswith(".")]
            if time.time() > deadline:
                break
            for name in files:
                low = name.lower()
                if low == target:
                    exact.append(str((Path(root) / name).relative_to(root_path)))
                elif target in low and Path(name).suffix.lower() not in NOISE_EXTS:
                    contains.append(str((Path(root) / name).relative_to(root_path)))
            if len(exact) >= max_results:
                break
    except (OSError, ValueError):
        return []
    exact.sort()
    contains.sort()
    return (exact + contains)[:max_results]


def _fmt_size(size: int) -> str:
    if size < 1024:
        return f"{size}B"
    if size < 1024 * 1024:
        return f"{size / 1024:.1f}KB"
    return f"{size / 1024 / 1024:.1f}MB"
