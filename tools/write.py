from pathlib import Path


def _syntax_error(source: str) -> str | None:
    """Return a short description of a Python syntax error, or None if valid."""
    try:
        compile(source, "<write_file>", "exec")
        return None
    except SyntaxError as e:
        line = e.lineno or 0
        text = (e.text or "").rstrip()
        detail = f"SyntaxError on line {line}: {e.msg}"
        if text:
            detail += f"\n  {text.strip()[:120]}"
        return detail
    except ValueError as e:
        # e.g. source containing null bytes
        return f"Invalid source: {e}"


def write_file(path: str, content: str) -> str:
    """Create a new file or overwrite an existing file with the given content.

    Args:
        path: Path where to write the file (absolute or relative to workspace)
        content: The full content to write to the file
    """
    try:
        filepath = Path(path).resolve()
        filepath.parent.mkdir(parents=True, exist_ok=True)

        # A small model often stops mid-expression while still emitting valid
        # JSON, so the truncation is invisible and the file lands broken. Catch
        # it here and tell the model exactly where, instead of letting it
        # rewrite the whole file blindly over and over.
        if filepath.suffix == ".py":
            err = _syntax_error(content)
            if err:
                return (
                    f"Error: NOT written - the Python code is incomplete or invalid.\n{err}\n"
                    "Your output was most likely cut off. Write a SHORTER version "
                    "that is complete and runnable, then extend it with edit_file."
                )

        # newline="" disables Python's automatic \n -> \r\n translation.
        # Without it the file gets CRLF while the model keeps sending \n in
        # edit_file(old_string=...), so no multi-line edit can ever match.
        with open(filepath, "w", encoding="utf-8", newline="") as f:
            f.write(content)

        return f"Successfully wrote {len(content)} bytes to {filepath}"
    except Exception as e:
        return f"Error writing file: {e}"
