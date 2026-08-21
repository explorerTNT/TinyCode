import os


def ask_user(question: str) -> str:
    """Ask the user a question and get their text response.
    Use this when you need clarification, confirmation, or additional information.

    Args:
        question: The question to ask the user
    """
    auto = os.environ.get("TINY_CODE_ASK_USER")
    if auto:
        print(f"\n--- AI asks: {question} ---")
        print(f"> [auto-answer: {auto}]")
        return f"User's response: {auto}"
    try:
        print(f"\n--- AI asks: {question} ---")
        print("> ", end="", flush=True)
        answer = input().strip()
        return f"User's response: {answer}"
    except (EOFError, KeyboardInterrupt):
        return "User cancelled the input"

