import difflib
import re
from pathlib import Path

from .textfile import (
    apply_newlines,
    dominant_newline,
    join_lines,
    normalize,
    read_text,
    split_lines,
    write_text,
)

# A fuzzy match is only accepted when it is both strong and clearly better than
# the runner-up. These thresholds are deliberately strict: applying an edit to
# the wrong block corrupts the file silently, which is far worse than returning
# an error the model can recover from.
FUZZY_MIN_RATIO = 0.94
FUZZY_MIN_MARGIN = 0.08


def _near_miss(lines: list[str], old_string: str, max_hits: int = 3) -> str:
    """Show real lines resembling a failed old_string.

    A 2B model retypes a line from memory and gets the spacing or a digit
    wrong, then has no way to see what the file really says.
    """
    probe_lines = [ln.strip() for ln in old_string.strip().split("\n") if ln.strip()]
    if not probe_lines:
        return ""

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
            neighbours = sum(scored.get(ln + off, 0) for off in range(1, span + 1))
            boosted[ln] = scored[ln] + neighbours
        scored = boosted

    ranked = sorted(scored.items(), key=lambda kv: (-kv[1], kv[0]))
    out = [f"  line {ln}: {lines[ln - 1].strip()[:120]}" for ln, _ in ranked[:max_hits]]
    if len(ranked) > max_hits:
        out.append(f"  ... and {len(ranked) - max_hits} more")
    return "\n".join(out)


def _write_lines(
    filepath: Path,
    lines: list[str],
    newline: str,
    trailing: bool,
    encoding: str,
    original_len: int,
    shown_path: str,
    detail: str,
) -> str:
    out = join_lines(lines, newline, trailing)
    write_text(filepath, out, encoding)
    diff = len(out) - original_len
    return (
        f"Successfully edited {shown_path} "
        f"({'+' if diff >= 0 else ''}{diff} bytes, {detail})"
    )


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
    lines, trailing = split_lines(content)
    newline = dominant_newline(content)
    total = len(lines)

    if total == 0:
        return f"Error: {shown_path} is empty, there is no line {start} to replace"
    if start < 1:
        return f"Error: start_line must be 1 or greater, got {start}"
    if start > total:
        return f"Error: start_line {start} is out of range (file has {total} lines)"
    if end < start:
        return f"Error: end_line {end} is before start_line {start}"
    if end > total:
        return f"Error: end_line {end} is out of range (file has {total} lines)"

    replaced = lines[start - 1:end]
    # The model may send \r\n, \n or a mix regardless of what the file uses.
    new_lines, _ = split_lines(normalize(new_text)) if new_text else ([], False)

    updated = lines[:start - 1] + new_lines + lines[end:]
    return _write_lines(
        filepath, updated, newline, trailing, encoding, len(content), shown_path,
        f"lines {start}-{end} replaced: {len(replaced)} -> {len(new_lines)}",
    )


def _locate_block(lines: list[str], probe: str) -> tuple[tuple[int, int] | None, str, str]:
    """Find the line range in ``lines`` that ``probe`` was meant to match.

    Small models reproduce code from memory and get indentation, trailing
    whitespace or a character or two wrong. Requiring a byte-exact match turns
    every such slip into a guessing loop, so match in widening steps and stop
    at the first level that gives a single unambiguous answer.

    Returns ((start_idx, end_idx_exclusive), note, ambiguity_reason).
    A non-empty reason means a candidate existed but was rejected as unsafe,
    which lets the caller explain *why* instead of just saying "not found".
    """
    probe_lines = normalize(probe).strip("\n").split("\n")
    span = len(probe_lines)
    if span == 0 or span > len(lines):
        return None, "", ""

    stripped_probe = [ln.strip() for ln in probe_lines]

    # Level 1: identical once leading/trailing whitespace is ignored.
    exact = [
        i
        for i in range(len(lines) - span + 1)
        if [ln.strip() for ln in lines[i:i + span]] == stripped_probe
    ]
    if len(exact) == 1:
        return (exact[0], exact[0] + span), " [matched ignoring indentation]", ""
    if len(exact) > 1:
        found = ", ".join(str(i + 1) for i in exact[:5])
        return None, "", (
            f"the text appears {len(exact)} times (lines {found}). "
            "Use start_line/end_line to say which one you mean."
        )

    # Level 2: near-identical text. The threshold is deliberately high so an
    # edit is never applied to a block the model did not mean.
    joined_probe = "\n".join(stripped_probe)
    best_i, best_ratio, runner_up = -1, 0.0, 0.0
    for i in range(len(lines) - span + 1):
        candidate = "\n".join(ln.strip() for ln in lines[i:i + span])
        ratio = difflib.SequenceMatcher(None, joined_probe, candidate).ratio()
        if ratio > best_ratio:
            best_i, runner_up, best_ratio = i, best_ratio, ratio
        elif ratio > runner_up:
            runner_up = ratio

    if best_ratio >= FUZZY_MIN_RATIO and best_ratio - runner_up >= FUZZY_MIN_MARGIN:
        return (best_i, best_i + span), f" [fuzzy match, {best_ratio:.0%} similar]", ""

    # A strong-but-ambiguous match is the dangerous case: two near-identical
    # blocks (an overloaded method, a repeated test case). Refusing here and
    # naming the line is what keeps the edit from landing in the wrong one.
    if best_ratio >= FUZZY_MIN_RATIO:
        return None, "", (
            f"two or more blocks look almost identical (best match at line "
            f"{best_i + 1}, {best_ratio:.0%} similar, with a near-tie). "
            "Use start_line/end_line to disambiguate."
        )

    # Level 3: anchor on a declaration. Small models misremember a signature
    # ("def is_safe(board, r, n)" for "...r, c"), which drops similarity below
    # any safe threshold even though the target is unambiguous.
    decl = re.match(r"\s*((?:async\s+def|def|class)\s+\w+)", probe_lines[0])
    if decl:
        signature = decl.group(1)
        hits = [
            i for i, line in enumerate(lines)
            if re.match(r"\s*" + re.escape(signature) + r"\b", line)
        ]
        if len(hits) == 1:
            i = hits[0]
            indent = len(lines[i]) - len(lines[i].lstrip())
            # The block runs until the next line at the same or lower indent.
            end_i = i + 1
            while end_i < len(lines):
                line = lines[end_i]
                if line.strip() and (len(line) - len(line.lstrip())) <= indent:
                    break
                end_i += 1
            # Replacing a 40-line function because the model sent a 3-line
            # probe destroys code it never looked at. Only accept the anchor
            # when the block is close to the size the probe implies.
            block_span = end_i - i
            if block_span > span * 2 + 5:
                return None, "", (
                    f"'{signature}' at line {i + 1} spans {block_span} lines but "
                    f"you supplied {span}. Read the file and edit by "
                    "start_line/end_line so nothing outside your intent is lost."
                )
            return (i, end_i), f" [matched by declaration '{signature}']", ""
        if len(hits) > 1:
            found = ", ".join(str(i + 1) for i in hits[:5])
            return None, "", (
                f"'{signature}' is declared {len(hits)} times (lines {found}). "
                "Use start_line/end_line to pick one."
            )

    return None, "", ""


def _as_line_number(value) -> int | None:
    """Coerce a line number the model sent as text/float. None if unusable.

    JSON schemas say "integer", but small models routinely emit "2" or 2.0.
    Rejecting those turned a valid edit into a crash the model could not
    interpret, so they are accepted instead.
    """
    if value is None or isinstance(value, bool):
        return None
    if isinstance(value, int):
        return value
    if isinstance(value, float):
        return int(value) if value.is_integer() else None
    if isinstance(value, str):
        text = value.strip()
        if not text:
            return None
        try:
            return int(text)
        except ValueError:
            try:
                num = float(text)
            except ValueError:
                return None
            return int(num) if num.is_integer() else None
    return None


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

        # The model may omit new_string entirely when deleting lines.
        new_string = "" if new_string is None else str(new_string)
        old_string = "" if old_string is None else str(old_string)

        raw_start, raw_end = start_line, end_line
        start_line = _as_line_number(raw_start)
        end_line = _as_line_number(raw_end)
        if start_line is None and raw_start not in (None, "", 0):
            return (
                f"Error: start_line must be a line number, got {raw_start!r}. "
                "Use the numbers printed by read_file."
            )
        if end_line is None and raw_end not in (None, "", 0):
            return (
                f"Error: end_line must be a line number, got {raw_end!r}. "
                "Use the numbers printed by read_file."
            )

        content, used_encoding = read_text(filepath)

        # Line addressing is exact by construction: no memorising, no fuzzy
        # matching, no ambiguity. read_file prints these very numbers.
        if start_line:
            if old_string:
                # Both addressing modes at once is ambiguous; line numbers win,
                # but say so rather than silently ignoring old_string.
                print(
                    f"  [edit_file: start_line and old_string both given for "
                    f"{path}; using line numbers]"
                )
            return _replace_lines(
                filepath, content, used_encoding, path,
                start_line, end_line or start_line, new_string,
            )

        if not old_string:
            return (
                "Error: provide either start_line (preferred, from read_file) "
                "or old_string."
            )

        lines, trailing = split_lines(content)
        newline = dominant_newline(content)

        # All matching happens on a normalised copy so a CRLF file behaves
        # exactly like an LF one; the file's own style is restored on write.
        norm_content = normalize(content)
        norm_old = normalize(old_string)
        norm_new = normalize(new_string)

        count = norm_content.count(norm_old)

        if count == 1:
            if norm_old == norm_new:
                # old_string WAS found, so the only way the file ends up
                # unchanged is new_string being identical. Saying "not found"
                # here is a lie that sends the model hunting for a phantom bug.
                return (
                    "No changes made: old_string and new_string are identical. "
                    "To fix a typo, new_string must differ from old_string."
                )
            new_norm_content = norm_content.replace(norm_old, norm_new, 1)
            out = apply_newlines(new_norm_content, newline)
            write_text(filepath, out, used_encoding)
            diff = len(out) - len(content)
            return (
                f"Successfully edited {path} "
                f"({'+' if diff >= 0 else ''}{diff} bytes, "
                f"{norm_old.count(chr(10)) + 1} lines changed)"
            )

        if count > 1:
            short = norm_old[:50].replace("\n", "\\n")
            return (
                f"Error: Found {count} occurrences of '{short}...' in {path}. "
                "Provide more surrounding context to make old_string unique, "
                "or use start_line/end_line."
            )

        # Exact match failed. Rather than making the model retype the text
        # from memory (which is what it is bad at), locate the block by
        # tolerating the things it actually gets wrong: indentation,
        # trailing spaces and small typos. Only give up if that is
        # ambiguous or too weak a match.
        located, note, reason = _locate_block(lines, norm_old)
        if located is not None:
            start_i, end_i = located
            new_lines, _ = split_lines(norm_new) if norm_new else ([], False)
            updated = lines[:start_i] + new_lines + lines[end_i:]
            return _write_lines(
                filepath, updated, newline, trailing, used_encoding, len(content), path,
                f"lines {start_i + 1}-{end_i} replaced: {end_i - start_i} -> {len(new_lines)}",
            ) + note

        short = norm_old[:50].replace("\n", "\\n")
        msg = f"Error: Could not find '{short}...' in {path}"
        if reason:
            msg += f"\nReason: {reason}"
            return msg
        hint = _near_miss(lines, norm_old)
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
    except Exception as e:
        return f"Error editing file: {e}"
