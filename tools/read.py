from pathlib import Path

from .glob import suggest_files


BINARY_EXTS = {
    ".ico", ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp", ".svgz",
    ".exe", ".dll", ".so", ".dylib", ".pdb", ".bin", ".obj", ".lib",
    ".zip", ".rar", ".7z", ".gz", ".tar", ".xz", ".jar", ".nupkg",
    ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
    ".mp3", ".mp4", ".avi", ".mkv", ".wav", ".mov", ".ttf", ".otf",
    ".woff", ".woff2", ".pyc", ".pyd", ".class", ".wasm", ".db", ".sqlite",
}

MAX_READ_BYTES = 400_000
MAX_LINE_CHARS = 2000


def _looks_binary(filepath: Path) -> bool:
    """A NUL byte in the first 8 KB is the classic binary heuristic."""
    try:
        with open(filepath, "rb") as f:
            return b"\x00" in f.read(8192)
    except OSError:
        return False


def read_file(path: str, offset: int = 1, limit: int = 2000) -> str:
    """Read a file from the filesystem and return its contents.

    Args:
        path: Path to the file (absolute or relative to workspace)
        offset: Line number to start reading from (1-indexed, default 1)
        limit: Maximum number of lines to read (default 2000, max 5000)
    """
    try:
        filepath = Path(path).resolve()
        if not filepath.exists():
            msg = f"Error: File not found: {path}"
            candidates = suggest_files(path, filepath.parent if filepath.parent.exists() else Path.cwd())
            if candidates:
                msg += " Did you mean:\n" + "\n".join(f"  {c}" for c in candidates)
            return msg
        if not filepath.is_file():
            return f"Error: Not a file: {path}"

        # Binary content read as text becomes replacement-char noise that
        # poisons the context and sends small models into a repetition loop.
        if filepath.suffix.lower() in BINARY_EXTS:
            size = filepath.stat().st_size
            return (
                f"Error: '{filepath.name}' is a binary file ({_fmt_size(size)}), not text. "
                "Do not read it. Its name and size are all the information you need."
            )
        if _looks_binary(filepath):
            size = filepath.stat().st_size
            return (
                f"Error: '{filepath.name}' appears to be binary ({_fmt_size(size)}), not text. "
                "Do not read it."
            )

        size = filepath.stat().st_size
        if size > MAX_READ_BYTES:
            return (
                f"Error: '{filepath.name}' is too large ({_fmt_size(size)}). "
                "Use search_files to find the relevant part, then read with offset/limit."
            )

        total = 0
        lines = []
        cap = min(limit, 5000)
        start = max(0, offset - 1)
        end = start + cap

        with open(filepath, "r", encoding="utf-8", errors="replace") as f:
            for line in f:
                total += 1
                if start < total <= end:
                    if len(line) > MAX_LINE_CHARS:
                        line = line[:MAX_LINE_CHARS] + " ... [line truncated]\n"
                    lines.append(f"{total}: {line}")

        if start >= total:
            return f"--- {filepath} (offset {offset} past end, {total} lines total) ---\n"

        info = f"--- {filepath} (lines {start + 1}-{min(end, total)} of {total}) ---\n"
        if end < total:
            info += f"... (showing {len(lines)} of {total} lines, use offset={end + 1} for more)\n"

        return info + "".join(lines)
    except Exception as e:
        return f"Error reading file: {e}"


def _fmt_size(size: int) -> str:
    if size < 1024:
        return f"{size}B"
    if size < 1024 * 1024:
        return f"{size / 1024:.1f}KB"
    return f"{size / 1024 / 1024:.1f}MB"
