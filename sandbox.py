"""Workspace containment checks shared by the agent and the shell tool.

The path guard used to live inside ``agent.py`` and only inspected a tool's
``path`` argument. ``run_bash`` has no such argument, so every shell command
bypassed the sandbox entirely. The command inspection below closes that hole:
it extracts the filesystem paths a command refers to and refuses the ones that
point outside the workspace.

This is a mitigation, not a security boundary. A determined command can always
obfuscate its target (variables, encoded strings, nested interpreters); the
goal is to stop a confused small model from wandering out of its project
directory, and to make the escape loud rather than silent.
"""
from __future__ import annotations

import re
from pathlib import Path

# Commands that reach outside the current directory by design. Seeing one of
# these means the workspace check cannot be reasoned about statically.
_INTERPRETERS = {
    "powershell", "powershell.exe", "pwsh", "pwsh.exe",
    "cmd", "cmd.exe", "bash", "sh", "zsh",
}

# Destructive operations, matched on the command word so an argument that
# merely contains the word does not trip them.
_DESTRUCTIVE_HEADS = {
    "format", "mkfs", "diskpart", "fdisk",
    "shutdown", "reboot", "halt",
}

# PowerShell and cmd equivalents of `rm -rf`. The original critic only knew
# bash spellings, so the native Windows forms - the ones this agent actually
# produces - went straight through.
_DESTRUCTIVE_PATTERNS = (
    # Recursive+forced deletion of anything.
    r"remove-item\b[^|;]*\s-recurse\b[^|;]*\s-force\b",
    r"remove-item\b[^|;]*\s-force\b[^|;]*\s-recurse\b",
    r"\bri\b[^|;]*\s-recurse\b[^|;]*\s-force\b",
    r"\brd\s+/s\b", r"\brmdir\s+/s\b",
    r"\bdel\s+/[fsq]", r"\berase\s+/[fsq]",
    # Classic bash forms, kept for the non-Windows path.
    r"\brm\s+-[a-z]*r[a-z]*f\b", r"\brm\s+-[a-z]*f[a-z]*r\b",
    r"\brm\s+-r\s+/", r"\brm\s+-rf\s+~",
    # Filesystem / disk level destruction.
    r"\bformat\s+[a-z]:", r"\bmkfs\b", r"\bdd\s+if=",
    r"format-volume\b", r"clear-disk\b", r"remove-partition\b",
    # Fork bomb.
    r":\(\)\s*\{",
    # Piping a download straight into a shell.
    r"(curl|wget|iwr|invoke-webrequest)\b[^|]*\|\s*(sh|bash|iex|invoke-expression)",
    r"(iex|invoke-expression)\b[^|]*\(\s*(new-object\s+net\.webclient|irm|invoke-restmethod)",
    # Registry and system-wide state.
    r"remove-item\b[^|;]*\bhk(lm|cu):", r"\breg\s+delete\b",
    r"set-executionpolicy\b", r"\bvssadmin\s+delete\b",
    r"\bcipher\s+/w", r"\bbcdedit\b",
    # Recursive ACL changes.
    r"\bicacls\b[^|;]*\/t\b[^|;]*\/grant", r"\btakeown\b[^|;]*\/r\b",
)

# Anything that looks like a filesystem path: a drive letter, a UNC share, a
# leading separator, or an explicit parent-directory traversal.
_PATH_LIKE = re.compile(
    r"""(?:
          [A-Za-z]:[\\/][^\s"'|;,)]*     # C:\... or C:/...
        | \\\\[^\s"'|;,)]+               # \\server\share
        | (?<![\w.])[\\/][^\s"'|;,)]+    # /absolute/path
        | (?:\.\.[\\/])[^\s"'|;,)]*      # ../ traversal
    )""",
    re.VERBOSE,
)

# URLs are stripped before the path scan. Otherwise the "s:" inside
# "https://..." satisfies the drive-letter branch of _PATH_LIKE and every
# `git clone` is rejected as an escape attempt.
_URL = re.compile(r"\b[a-z][a-z0-9+.-]*://[^\s\"'|;,)]*", re.IGNORECASE)

# Environment variables that expand to a location outside any workspace.
_ESCAPING_VARS = re.compile(
    r"(?:\$env:|%)(?:USERPROFILE|APPDATA|LOCALAPPDATA|SYSTEMROOT|WINDIR|"
    r"PROGRAMFILES(?:\(X86\))?|PROGRAMDATA|TEMP|TMP|HOMEPATH|HOMEDRIVE)\b",
    re.IGNORECASE,
)


class SandboxError(Exception):
    """Raised when a requested location escapes the workspace."""


def resolve_in_workspace(raw: str, workspace: Path) -> Path:
    """Resolve ``raw`` against the workspace, refusing anything outside it.

    Raises :class:`SandboxError` on escape and :class:`OSError` on a path the
    filesystem cannot represent at all.
    """
    ws = Path(workspace).resolve()
    candidate = Path(raw)
    if _is_rooted(raw) or candidate.is_absolute():
        # A drive-less rooted path ("/etc/passwd", "\\Windows") is not
        # "absolute" to pathlib on Windows, but the OS still resolves it away
        # from the workspace, so joining it here would hide a real escape.
        resolved = candidate.resolve()
    else:
        resolved = (ws / candidate).resolve()
    if not is_within(resolved, ws):
        raise SandboxError(f"'{raw}' is outside the workspace")
    return resolved


def _is_rooted(raw: str) -> bool:
    """True for a path anchored at a root, with or without a drive letter."""
    return raw.startswith(("/", "\\"))


def is_within(path: Path, workspace: Path) -> bool:
    """True when ``path`` is the workspace itself or lives inside it.

    ``Path.relative_to`` is used rather than string prefixing so that a
    sibling directory sharing a name prefix (``/work`` vs ``/work-backup``)
    is not mistaken for a child.
    """
    try:
        Path(path).resolve().relative_to(Path(workspace).resolve())
        return True
    except (ValueError, OSError):
        return False


def check_command(command: str, workspace: Path) -> str | None:
    """Inspect a shell command. Returns an error message, or None if allowed.

    Two classes of problem are reported: destructive operations (regardless of
    location) and references to paths outside the workspace.
    """
    if not command or not command.strip():
        return None

    lowered = command.lower()

    for pattern in _DESTRUCTIVE_PATTERNS:
        if re.search(pattern, lowered):
            return (
                "command rejected: it matches a destructive pattern "
                f"({pattern!r}). Rewrite it to target only the specific files "
                "you mean, inside the project directory."
            )

    for segment in re.split(r"\|\||&&|[|;&]", lowered):
        tokens = segment.strip().split()
        if tokens and tokens[0].strip("\"'") in _DESTRUCTIVE_HEADS:
            return (
                f"command rejected: '{tokens[0]}' operates on the machine, not "
                "on this project. It is never needed here."
            )

    if _ESCAPING_VARS.search(command):
        return (
            "command rejected: it expands an environment variable that points "
            "outside the project directory. Use a path relative to the project."
        )

    # Blank out URLs (preserving length/offsets is unnecessary here) so their
    # scheme is not read as a drive letter by the path scanner below.
    scannable = _URL.sub(" ", command)

    for match in _PATH_LIKE.finditer(scannable):
        raw = match.group(0).strip("\"'")
        if not raw or _is_flag_like(raw):
            continue
        try:
            resolve_in_workspace(raw, workspace)
        except SandboxError:
            return (
                f"command rejected: '{raw}' is outside the project directory. "
                "Use a path relative to the project, like src/main.py."
            )
        except OSError:
            continue
    return None


def _is_flag_like(raw: str) -> bool:
    """Filter out matches that are switches or protocol prefixes, not paths.

    ``/Force`` in PowerShell and ``//`` in a URL both satisfy the "starts with
    a separator" rule without naming anything on disk.
    """
    if raw.startswith("//"):
        return True
    if raw.startswith("/") and raw[1:2].isalpha() and "/" not in raw[1:] and "\\" not in raw[1:]:
        # A single-segment /switch. A real absolute path of one segment
        # (/etc, /tmp) is caught by the Linux-command check in bash.py.
        return len(raw) <= 12
    return False
