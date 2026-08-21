import json


def count_tokens(text: str) -> int:
    if not text:
        return 0
    return max(1, len(text.encode("utf-8", errors="replace")) // 4)


def count_message_tokens(msg: dict) -> int:
    return count_tokens(json.dumps(msg, ensure_ascii=False, default=str))


def truncate_messages(messages: list, max_tokens: int) -> list:
    total = 0
    result = []

    for msg in reversed(messages):
        content = msg.get("content", "")
        tokens = count_tokens(content)
        if total + tokens > max_tokens:
            msg_copy = dict(msg)
            ratio = (max_tokens - total) / max(tokens, 1)
            keep_chars = max(100, int(len(content) * ratio))
            msg_copy["content"] = content[:keep_chars] + f"\n... [truncated, was {tokens} tokens]"
            result.insert(0, msg_copy)
            break
        result.insert(0, msg)
        total += tokens

    return result
