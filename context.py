import json


def count_tokens(text: str) -> int:
    if not text:
        return 0
    return max(1, len(text.encode("utf-8", errors="replace")) // 4)


def _sizeof(value) -> int:
    if value is None:
        return 0
    try:
        return len(value)
    except TypeError:
        return -1


def count_message_tokens(msg: dict) -> int:
    """Estimate a message's token cost.

    The result is memoised on the message itself. ContextManager.trim used to
    re-serialise the entire history through json.dumps before every single LLM
    call, which on a long conversation is the most expensive thing the agent
    does between requests.

    The cache key is the message's identity plus a cheap content fingerprint,
    so an in-place edit (the normaliser merges assistant turns) invalidates it.
    """
    if not isinstance(msg, dict):
        return count_tokens(str(msg))

    # `len` is not safe on arbitrary values: a malformed message can carry a
    # non-sized object in `content`, and the fingerprint must never be the
    # thing that raises.
    fingerprint = (
        _sizeof(msg.get("content")),
        _sizeof(msg.get("tool_calls")),
        _sizeof(msg.get("reasoning_content")),
        msg.get("role"),
    )
    cached = msg.get("_tok_cache")
    if cached is not None and cached[0] == fingerprint:
        return cached[1]

    payload = {k: v for k, v in msg.items() if k != "_tok_cache"}
    tokens = count_tokens(json.dumps(payload, ensure_ascii=False, default=str))
    try:
        msg["_tok_cache"] = (fingerprint, tokens)
    except TypeError:
        pass
    return tokens
