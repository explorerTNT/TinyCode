"""Shared text-file plumbing for the read/write/edit tools.

Every tool that touches a file goes through here so they all agree on three
things that used to differ between them:

* **Encoding** - detected once, preserved on write-back.
* **Line splitting** - universal (``\\r\\n``, ``\\r`` and ``\\n`` all end a
  line). ``read_file`` used to open in text mode (where Python normalises
  everything) while ``edit_file`` opened with ``newline=""`` and split on a
  single style. On a file with mixed endings the two disagreed, so an edit
  addressed by the numbers ``read_file`` had printed landed on the wrong line.
* **Newline style on write** - the file's dominant style wins, so a model that
  only ever emits ``\\n`` cannot silently convert a CRLF file.
"""
from __future__ import annotations

from pathlib import Path

ENCODINGS = ("utf-8", "utf-8-sig", "cp1251")


def read_text(filepath: Path) -> tuple[str, str]:
    """Read a file verbatim. Returns (content, encoding).

    ``newline=""`` keeps the original terminators intact; normalisation is the
    caller's decision, not a side effect of reading.
    """
    for encoding in ENCODINGS:
        try:
            with open(filepath, "r", encoding=encoding, newline="") as f:
                return f.read(), encoding
        except UnicodeDecodeError:
            continue
    with open(filepath, "r", encoding="utf-8", errors="replace", newline="") as f:
        return f.read(), "utf-8"


def dominant_newline(content: str) -> str:
    """The newline style a write-back should use.

    Counting rather than testing for presence matters on a mixed file: a single
    stray ``\\r\\n`` in an LF file must not flip the whole file to CRLF.
    """
    crlf = content.count("\r\n")
    lf = content.count("\n") - crlf
    cr = content.count("\r") - crlf
    if crlf >= lf and crlf >= cr and crlf > 0:
        return "\r\n"
    if cr > lf and cr > 0:
        return "\r"
    return "\n"


def normalize(text: str) -> str:
    """Collapse every newline style to ``\\n``."""
    return text.replace("\r\n", "\n").replace("\r", "\n")


def split_lines(content: str) -> tuple[list[str], bool]:
    """Split into logical lines, universally. Returns (lines, ends_with_newline).

    The trailing empty element produced by a final newline is not a real line,
    so it is reported as a flag instead of being counted.
    """
    normalized = normalize(content)
    ends_with_newline = normalized.endswith("\n")
    if ends_with_newline:
        normalized = normalized[:-1]
    if normalized == "" and ends_with_newline:
        return [""], True
    if normalized == "":
        return [], False
    return normalized.split("\n"), ends_with_newline


def join_lines(lines: list[str], newline: str, trailing: bool) -> str:
    """Inverse of :func:`split_lines`, re-applying the file's newline style."""
    out = newline.join(lines)
    if trailing:
        out += newline
    return out


def apply_newlines(text: str, newline: str) -> str:
    """Re-encode arbitrary model output into a single newline style."""
    normalized = normalize(text)
    if newline == "\n":
        return normalized
    return normalized.replace("\n", newline)


def write_text(filepath: Path, content: str, encoding: str) -> None:
    """Write without Python's automatic newline translation."""
    with open(filepath, "w", encoding=encoding, newline="") as f:
        f.write(content)
