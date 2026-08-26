import os
import re
import subprocess
import sys

LINUX_SUBSTRINGS = [
    "bash -c", "/bin/bash", "sh -c", "/bin/sh",
    "/usr/", "/etc/", "/home/", "/var/",
]

LINUX_COMMANDS = {
    "which", "uname", "chmod", "chown", "apt", "apt-get", "yum", "dnf",
    "head", "tail", "grep", "sed", "awk", "xargs", "touch", "rm", "mv", "cp",
}

PS_HELP = (
    "This is Windows. Use PowerShell syntax. "
    "Common mappings: `ls` = `Get-ChildItem`, `cat` = `Get-Content`, "
    "`which` = `Get-Command`, `head` = `Select-Object -First`, "
    "`grep` = `Select-String`, `uname` = `$PSVersionTable`, "
    "`python3` = `python`, `pip3` = `pip`."
)

MAX_STDOUT = 10000
MAX_STDERR = 5000


def run_bash(command: str, timeout: int = 30, cwd: str = None) -> str:
    """Run a shell command on the user's machine and return the output.
    Use this for: git operations, running builds, executing tests, file operations.
    Be careful: this can modify the system.

    Args:
        command: The shell command to execute (PowerShell syntax on Windows)
        timeout: Maximum execution time in seconds (default 30, max 120)
        cwd: Directory to run in. Defaults to the process working directory.
    """
    try:
        timeout = int(timeout)
    except (TypeError, ValueError):
        timeout = 30
    # Bound outside the try block: referencing it from the TimeoutExpired
    # handler raised UnboundLocalError when the failure happened earlier.
    safe_timeout = max(1, min(timeout, 120))

    if command is None or not str(command).strip():
        return "Error: empty command"
    command = str(command)

    # An explicit cwd is passed by the agent instead of mutating the process
    # working directory, which is global state shared with the TUI thread.
    work_dir = cwd or os.getcwd()
    if not os.path.isdir(work_dir):
        return f"Error: working directory does not exist: {work_dir}"

    try:
        if sys.platform == "win32":
            command = _sanitize_cmd(command)

            hint = _check_linux_hints(command)
            if hint:
                return f"Error: {hint}\n{PS_HELP}"

            # PowerShell is the documented shell for this agent, and cmd.exe
            # mangles nested quotes: `python -c "import x"` reaches Python as
            # a broken string literal. Route everything through PowerShell.
            shell_cmd = ["powershell", "-NoProfile", "-NonInteractive", "-Command", command]
        else:
            shell_cmd = ["sh", "-c", command]

        result = subprocess.run(
            shell_cmd,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=safe_timeout,
            cwd=work_dir,
            # A command that waits on stdin would otherwise hang until the
            # timeout with no indication of why.
            stdin=subprocess.DEVNULL,
        )

        output_parts = []
        if result.stdout:
            stdout = result.stdout.rstrip()
            if len(stdout) > MAX_STDOUT:
                stdout = stdout[:MAX_STDOUT] + "\n(truncated)"
            output_parts.append(stdout)
        if result.stderr:
            err = result.stderr.rstrip()
            if result.returncode != 0:
                lines = err.splitlines()
                if len(lines) > 15:
                    err = "(truncated)\n" + "\n".join(lines[-15:])
                elif len(err) > 2500:
                    err = "(truncated)\n" + err[-2500:].lstrip()
            output_parts.append(f"--- stderr ---\n{err[:MAX_STDERR]}")

        if not output_parts:
            if re.search(r"\bpython\b.*\.py\b", command):
                # Running a script that prints nothing is nearly always a bug,
                # but a bare "(no output)" reads like success to a small model.
                output_parts.append(
                    "(no output — the script printed nothing. If it was meant "
                    "to display something, this is a logic bug, not a success.)"
                )
            else:
                output_parts.append("(no output)")

        output = "\n".join(output_parts)

        if result.returncode != 0:
            output = f"Exit code: {result.returncode}\n{output}"
            if "Traceback" in (result.stderr or ""):
                output += "\n\nHINT: The script crashed. Read the error, fix the bug in the .py file, then re-run."

        return output
    except subprocess.TimeoutExpired:
        return f"Error: Command timed out after {safe_timeout} seconds"
    except FileNotFoundError as e:
        return f"Error: Command not found: {e}"
    except Exception as e:
        return f"Error executing command: {e}"


def _check_linux_hints(command: str) -> str | None:
    """Reject genuine Linux/bash commands so the small model learns PowerShell.

    Matches only on the *command position* of each segment, so legitimate
    arguments like `python app.py --find x` or `git grep foo` are not rejected.
    """
    lowered = command.strip().lower()

    for frag in LINUX_SUBSTRINGS:
        if frag in lowered:
            return f"Linux command detected: '{frag.strip()}'"

    for segment in re.split(r"\|\||&&|[|;&]", lowered):
        tokens = segment.strip().split()
        if not tokens:
            continue
        head = tokens[0].strip("\"'")
        if head in LINUX_COMMANDS:
            return f"Linux command detected: '{head}'"
    return None


def _sanitize_cmd(command: str) -> str:
    """Fix harmless Windows/Linux mechanics that small models get wrong.

    Only mechanical rewrites live here (python3 -> python, /dev/null, stray cd).
    Real bash usage is rejected by _check_linux_hints instead, so the model
    gets a corrective hint rather than a silent rewrite.
    """
    cmd = command.strip()
    # Only a no-op `cd` into the workspace is dropped. Stripping every leading
    # `cd X && ...` silently changed the working directory of a legitimate
    # command ("cd build && cmake .." ran in the wrong place and failed).
    cmd = re.sub(
        r"^\s*cd\s+(?:\.|[\"']\.[\"'])\s*(?:&&|;)\s*", "", cmd
    )
    cmd = _sub_outside_quotes(r"\bpython3(?:\.\d+)?\b", "python", cmd)
    cmd = _sub_outside_quotes(r"\bpip3\b", "pip", cmd)
    cmd = re.sub(r"\s*2>\s*/dev/null", "", cmd)
    cmd = re.sub(r"\s*1?>\s*/dev/null", "", cmd)
    cmd = re.sub(r"\s+2>&1\s*\|\s*cat\b", "", cmd)
    cmd = _squeeze_spaces_outside_quotes(cmd)
    return cmd.strip().strip(";").strip()


def _sub_outside_quotes(pattern: str, repl: str, cmd: str) -> str:
    """Apply a substitution only to the unquoted parts of a command.

    Rewriting inside quotes corrupts the user's own data: a commit message or
    a here-string that legitimately mentions "python3" must survive intact.
    """
    out = []
    buf = []
    quote = None
    for ch in cmd:
        if quote:
            out.append(ch)
            if ch == quote:
                quote = None
            continue
        if ch in "\"'":
            out.append(re.sub(pattern, repl, "".join(buf)))
            buf = []
            quote = ch
            out.append(ch)
            continue
        buf.append(ch)
    out.append(re.sub(pattern, repl, "".join(buf)))
    return "".join(out)


def _squeeze_spaces_outside_quotes(cmd: str) -> str:
    """Collapse runs of whitespace, but never inside a quoted string.

    A blanket re.sub rewrote the user's own data: the commit message in
    `git commit -m "fix:  spacing"` came out with the spacing altered.
    """
    out = []
    quote = None
    prev_space = False
    for ch in cmd:
        if quote:
            out.append(ch)
            if ch == quote:
                quote = None
            continue
        if ch in "\"'":
            quote = ch
            out.append(ch)
            prev_space = False
            continue
        if ch in " \t":
            if not prev_space:
                out.append(" ")
            prev_space = True
            continue
        out.append(ch)
        prev_space = False
    return "".join(out)
