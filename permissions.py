class PermissionManager:
    def __init__(self, mode: str = "ask"):
        self.mode = mode
        self._approved_bash_patterns: list[str] = []
        self._auto_approve_readonly_commands = True

    def check_bash(self, command: str) -> bool:
        if self.mode == "auto":
            return True
        if self.mode == "deny":
            return False

        stripped = command.strip().lower()

        if self._auto_approve_readonly_commands and self._is_readonly(stripped):
            return True

        for pattern in self._approved_bash_patterns:
            if stripped.startswith(pattern):
                return True

        return self._ask_user(f"Run this command?\n  $ {command}\nApprove? (y/n/a always) ")

    def check_write(self, path: str) -> bool:
        if self.mode == "auto":
            return True
        if self.mode == "deny":
            return False

        return self._ask_user(f"Write to {path}? (y/n) ")

    DANGEROUS_CHARS = (">", "<", "|", ";", "&", "`", "$(", "\n", "\r")

    def _is_readonly(self, cmd: str) -> bool:
        if any(ch in cmd for ch in self.DANGEROUS_CHARS):
            return False

        readonly = {
            "ls", "dir", "cat", "type", "pwd", "whoami", "where",
            "get-location", "get-childitem", "get-content", "cd",
        }
        readonly_pairs = {
            ("git", "status"), ("git", "diff"), ("git", "log"),
            ("git", "branch"), ("git", "remote"), ("git", "--version"),
            ("python", "--version"), ("node", "--version"),
            ("npm", "--version"), ("pip", "list"), ("pip", "--version"),
        }

        tokens = cmd.split()
        if not tokens:
            return False
        if tokens[0] in readonly and len(tokens) <= 2:
            return True
        if len(tokens) >= 2 and (tokens[0], tokens[1]) in readonly_pairs:
            return True
        return False

    def _ask_user(self, prompt: str) -> bool:
        try:
            resp = input(prompt).strip().lower()
            if resp == "a":
                self.mode = "auto"
                return True
            if resp in ("y", "yes"):
                return True
            return False
        except (EOFError, KeyboardInterrupt):
            return False
