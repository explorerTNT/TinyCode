from pathlib import Path


def _read_text(filepath: Path) -> tuple[str, str]:
    """Read a text file, preserving its original encoding for later write-back."""
    for encoding in ("utf-8", "utf-8-sig", "cp1251"):
        try:
            with open(filepath, "r", encoding=encoding, newline="") as f:
                return f.read(), encoding
        except UnicodeDecodeError:
            continue
    with open(filepath, "r", encoding="utf-8", errors="replace", newline="") as f:
        return f.read(), "utf-8"


def _near_miss(content: str, old_string: str, max_hits: int = 3) -> str:
    """Show real lines resembling a failed old_string.

    A 2B model retypes a line from memory and gets the spacing or a digit
    wrong, then has no way to see what the file really says.
    """
    import re

    probe_lines = [ln.strip() for ln in old_string.strip().split("\n") if ln.strip()]
    if not probe_lines:
        return ""

    lines = content.split("\n")

    # A multi-line old_string usually fails on whitespace, not on content. The
    # most useful anchor is the most *distinctive* line of the probe, not the
    # first one: matching on a generic "def __init__(self):" buries the one
    # place that actually matters under identical hits.
    generic = {"def __init__(self):", "try:", "else:", "pass", "return"}
    candidates = [p for p in probe_lines if p not in generic] or probe_lines

    best = []
    for probe in candidates:
        tokens = re.findall(r"[A-Za-z_][A-Za-z0-9_]{2,}", probe)
        if not tokens:
            continue
        anchor = max(tokens, key=len)
        hits = [
            (ln, line) for ln, line in enumerate(lines, 1) if anchor in line
        ]
        # Prefer the anchor that pinpoints the location instead of matching
        # everywhere; a unique hit is far more useful than twenty.
        if hits and (not best or len(hits) < len(best)):
            best = hits
        if len(hits) == 1:
            break

    if not best:
        return ""

    out = []
    for ln, line in best[:max_hits]:
        out.append(f"  line {ln}: {line.strip()[:120]}")
    if len(best) > max_hits:
        out.append(f"  ... and {len(best) - max_hits} more")
    return "\n".join(out)


def edit_file(path: str, old_string: str, new_string: str) -> str:
    """Edit a file by replacing exact text. Use this to make targeted changes
    to existing files without rewriting the entire file.

    Args:
        path: Path to the file to edit
        old_string: The exact text to find and replace (must match exactly)
        new_string: The text to replace it with
    """
    try:
        filepath = Path(path).resolve()
        if not filepath.exists():
            return f"Error: File not found: {path}"
        if not filepath.is_file():
            return f"Error: Not a file: {path}"

        content, used_encoding = _read_text(filepath)

        if not old_string:
            return "Error: old_string must not be empty"

        # The model always emits \n, but the file on disk may use CRLF. Match
        # against a normalised copy and translate offsets back, otherwise no
        # multi-line edit can ever succeed on a Windows-style file.
        crlf = "\r\n" in content
        if crlf and "\r\n" not in old_string:
            content_cmp = content.replace("\r\n", "\n")
            if content_cmp.count(old_string) == 1:
                new_cmp = content_cmp.replace(old_string, new_string, 1)
                new_content = new_cmp.replace("\n", "\r\n")
                with open(filepath, "w", encoding=used_encoding, newline="") as f:
                    f.write(new_content)
                diff = len(new_content) - len(content)
                return (
                    f"Successfully edited {path} "
                    f"({'+' if diff >= 0 else ''}{diff} bytes, "
                    f"{old_string.count(chr(10)) + 1} lines changed)"
                )

        count = content.count(old_string)
        if count == 0:
            short = old_string[:50].replace("\n", "\\n")
            hint = _near_miss(content, old_string)
            msg = f"Error: Could not find '{short}...' in {path}"
            if hint:
                msg += (
                    "\nThe file actually contains these similar lines "
                    "(copy one EXACTLY as old_string):\n" + hint
                )
            return msg
        if count > 1:
            short = old_string[:50].replace("\n", "\\n")
            return (
                f"Error: Found {count} occurrences of '{short}...' in {path}. "
                "Provide more surrounding context to make old_string unique."
            )

        new_content = content.replace(old_string, new_string, 1)

        if new_content == content:
            # old_string WAS found (count == 1 above), so the only way the file
            # is unchanged is new_string being identical. Saying "not found"
            # here is a lie that sends the model hunting for a phantom bug.
            return (
                "No changes made: old_string and new_string are identical. "
                "To fix a typo, new_string must differ from old_string."
            )

        with open(filepath, "w", encoding=used_encoding, newline="") as f:
            f.write(new_content)

        original_len = len(content)
        new_len = len(new_content)
        diff = new_len - original_len

        return f"Successfully edited {path} ({'+' if diff >= 0 else ''}{diff} bytes, {old_string.count(chr(10)) + 1} lines changed)"
    except Exception as e:
        return f"Error editing file: {e}"
