import json


def count_tokens(text: str) -> int:
    if not text:
        return 0
    return max(1, len(text.encode("utf-8", errors="replace")) // 4)


def count_message_tokens(msg: dict) -> int:
    return count_tokens(json.dumps(msg, ensure_ascii=False, default=str))
