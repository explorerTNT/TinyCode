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


def _match_newlines(text: str, content: str) -> str:
    """Re-encode `text` with the newline style the file already uses.

    The model always emits \\n. Splicing that into a CRLF file leaves mixed
    endings, which then breaks the next old_string match on the same file.
    """
    normalized = text.replace("\r\n", "\n").replace("\r", "\n")
    if "\r\n" in content:
        return normalized.replace("\n", "\r\n")
    return normalized


def _near_miss(content: str, old_string: str, max_hits: int = 3) -> str:
    """Show real lines resembling a failed old_string.

    A 2B model retypes a line from memory and gets the spacing or a digit
    wrong, then has no way to see what the file really says.
    """
    import re

    import difflib

    probe_lines = [ln.strip() for ln in old_string.strip().split("\n") if ln.strip()]
    if not probe_lines:
        return ""

    lines = content.split("\n")

    # Score every file line against every probe line and keep the best matches.
    # Ranking by *similarity* rather than by rarity is what matters: an earlier
    # version picked the anchor with the fewest hits, so "class Bird:" lost to a
    # stray "self.x" that happened to appear once inside a comment.
    scored: dict[int, float] = {}
    for probe in probe_lines:
        is_decl = bool(re.match(r"(class|def|async\s+def)\s+\w", probe))
        for ln, line in enumerate(lines, 1):
            stripped = line.strip()
            if not stripped:
                continue
            ratio = difflib.SequenceMatcher(None, probe, stripped).ratio()
            if ratio < 0.6:
                continue
            # A declaration in the probe is the strongest possible landmark:
            # that is the line the model was aiming at.
            if is_decl and re.match(r"(class|def|async\s+def)\s+\w", stripped):
                ratio += 0.5
            if ratio > scored.get(ln, 0):
                scored[ln] = ratio

    if not scored:
        return ""

    # Reward runs of consecutive matches. When a probe repeats a common
    # signature ("def __init__(self):") every copy scores the same, and only
    # the surrounding lines reveal which one the model actually meant.
    if len(probe_lines) > 1:
        boosted = dict(scored)
        span = len(probe_lines)
        for ln in scored:
            neighbours = sum(
                scored.get(ln + off, 0) for off in range(1, span + 1)
            )
            boosted[ln] = scored[ln] + neighbours
        scored = boosted

    ranked = sorted(scored.items(), key=lambda kv: (-kv[1], kv[0]))
    out = [f"  line {ln}: {lines[ln - 1].strip()[:120]}" for ln, _ in ranked[:max_hits]]
    if len(ranked) > max_hits:
        out.append(f"  ... and {len(ranked) - max_hits} more")
    return "\n".join(out)


def _replace_lines(
    filepath: Path,
    content: str,
    encoding: str,
    shown_path: str,
    start: int,
    end: int,
    new_text: str,
) -> str:
    """Replace an inclusive 1-based line range. Deterministic, no guessing."""
    newline = "\r\n" if "\r\n" in content else "\n"
    lines = content.split(newline)
    # A trailing newline yields a final empty element that is not a real line.
    trailing = lines and lines[-1] == ""
    if trailing:
        lines = lines[:-1]

    total = len(lines)
    if start < 1 or start > total:
        return f"Error: start_line {start} is out of range (file has {total} lines)"
    if end < start:
        return f"Error: end_line {end} is before start_line {start}"
    if end > total:
        return f"Error: end_line {end} is out of range (file has {total} lines)"

    replaced = lines[start - 1:end]
    # The model may send \r\n, \n or a mix regardless of what the file uses.
    # Splitting on "\n" alone leaves a stray \r on every line, which then gets
    # joined with the file's own newline and produces "\r\r\n".
    new_text = new_text.replace("\r\n", "\n").replace("\r", "\n")
    new_lines = new_text.split("\n") if new_text else []
    # Strip a trailing blank the model may append to its replacement text.
    if len(new_lines) > 1 and new_lines[-1] == "":
        new_lines = new_lines[:-1]

    updated = lines[:start - 1] + new_lines + lines[end:]
    out = newline.join(updated) + (newline if trailing else "")

    with open(filepath, "w", encoding=encoding, newline="") as f:
        f.write(out)

    diff = len(out) - len(content)
    return (
        f"Successfully edited {shown_path} "
        f"({'+' if diff >= 0 else ''}{diff} bytes, "
        f"lines {start}-{end} replaced: {len(replaced)} -> {len(new_lines)})"
    )


def _locate_block(content: str, probe: str) -> tuple[tuple[int, int] | None, str]:
    """Find the character span in `content` that `probe` was meant to match.

    Small models reproduce code from memory and get indentation, trailing
    whitespace or a character or two wrong. Requiring a byte-exact match turns
    every such slip into a guessing loop, so match in widening steps and stop
    at the first level that gives a single unambiguous answer.

    Returns ((start, end), note) or (None, "").
    """
    import difflib

    probe_lines = probe.strip("\n").split("\n")
    file_lines = content.split("\n")
    span = len(probe_lines)
    if span == 0 or span > len(file_lines):
        return None, ""

    # Character offset of the start of each line, so a line range can be
    # translated back into an exact slice of the original text.
    offsets, pos = [], 0
    for line in file_lines:
        offsets.append(pos)
        pos += len(line) + 1

    def span_bounds(i: int) -> tuple[int, int]:
        start = offsets[i]
        end = offsets[i + span - 1] + len(file_lines[i + span - 1])
        return start, end

    stripped_probe = [ln.strip() for ln in probe_lines]

    # Level 1: identical once leading/trailing whitespace is ignored.
    exact = [
        i
        for i in range(len(file_lines) - span + 1)
        if [ln.strip() for ln in file_lines[i:i + span]] == stripped_probe
    ]
    if len(exact) == 1:
        return span_bounds(exact[0]), " [matched ignoring indentation]"
    if len(exact) > 1:
        return None, ""

    # Level 2: near-identical text. The threshold is deliberately high so an
    # edit is never applied to a block the model did not mean.
    joined_probe = "\n".join(stripped_probe)
    best_i, best_ratio, runner_up = -1, 0.0, 0.0
    for i in range(len(file_lines) - span + 1):
        candidate = "\n".join(ln.strip() for ln in file_lines[i:i + span])
        ratio = difflib.SequenceMatcher(None, joined_probe, candidate).ratio()
        if ratio > best_ratio:
            best_i, runner_up, best_ratio = i, best_ratio, ratio
        elif ratio > runner_up:
            runner_up = ratio

    # Require both a strong match and a clear winner.
    if best_ratio >= 0.92 and best_ratio - runner_up >= 0.05:
        return span_bounds(best_i), f" [fuzzy match, {best_ratio:.0%} similar]"

    return None, ""


def edit_file(
    path: str,
    old_string: str = "",
    new_string: str = "",
    start_line: int = 0,
    end_line: int = 0,
) -> str:
    """Replace text in a file. Prefer start_line/end_line - read_file shows
    those numbers, so they need no guessing and always hit the right place.

    Args:
        path: Path to the file to edit
        new_string: The replacement text
        start_line: First line to replace, 1-based, as shown by read_file
        end_line: Last line to replace, inclusive. Omit to replace one line
        old_string: Legacy alternative to line numbers: exact text to find
    """
    try:
        filepath = Path(path).resolve()
        if not filepath.exists():
            return f"Error: File not found: {path}"
        if not filepath.is_file():
            return f"Error: Not a file: {path}"

        content, used_encoding = _read_text(filepath)

        # Line addressing is exact by construction: no memorising, no fuzzy
        # matching, no ambiguity. read_file prints these very numbers.
        if start_line:
            return _replace_lines(
                filepath, content, used_encoding, path,
                start_line, end_line or start_line, new_string,
            )

        if not old_string:
            return (
                "Error: provide either start_line (preferred, from read_file) "
                "or old_string."
            )

        # The model always emits \n, but the file on disk may use CRLF. Match
        # against a normalised copy and translate offsets back, otherwise no
        # multi-line edit can ever succeed on a Windows-style file.
        crlf = "\r\n" in content
        if crlf and "\r\n" not in old_string:
            content_cmp = content.replace("\r\n", "\n")
            if content_cmp.count(old_string) == 1:
                # new_string must be LF here as well: the whole buffer is
                # converted to CRLF below, so any \r left inside it would
                # become \r\r\n.
                new_lf = new_string.replace("\r\n", "\n").replace("\r", "\n")
                new_cmp = content_cmp.replace(old_string, new_lf, 1)
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
            # Exact match failed. Rather than making the model retype the text
            # from memory (which is what it is bad at), locate the block by
            # tolerating the things it actually gets wrong: indentation,
            # trailing spaces and small typos. Only give up if that is
            # ambiguous or too weak a match.
            located, note = _locate_block(content, old_string)
            if located is not None:
                start, end = located
                new_content = content[:start] + _match_newlines(new_string, content) + content[end:]
                with open(filepath, "w", encoding=used_encoding, newline="") as f:
                    f.write(new_content)
                diff = len(new_content) - len(content)
                return (
                    f"Successfully edited {path} "
                    f"({'+' if diff >= 0 else ''}{diff} bytes, "
                    f"{old_string.count(chr(10)) + 1} lines changed){note}"
                )

            short = old_string[:50].replace("\n", "\\n")
            hint = _near_miss(content, old_string)
            msg = f"Error: Could not find '{short}...' in {path}"
            if hint:
                msg += (
                    "\nClosest lines in the file:\n" + hint
                    + "\nUse start_line/end_line with those numbers instead of "
                      "retyping the text."
                )
            else:
                msg += (
                    "\nRun read_file first, then edit by start_line/end_line "
                    "instead of matching text."
                )
            return msg
        if count > 1:
            short = old_string[:50].replace("\n", "\\n")
            return (
                f"Error: Found {count} occurrences of '{short}...' in {path}. "
                "Provide more surrounding context to make old_string unique."
            )

        new_content = content.replace(old_string, _match_newlines(new_string, content), 1)

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
