from context import count_message_tokens, count_tokens


class TestCountTokens:
    def test_empty(self):
        assert count_tokens("") == 0

    def test_minimum_one(self):
        assert count_tokens("a") == 1

    def test_grows_with_length(self):
        assert count_tokens("x" * 400) > count_tokens("x" * 40)

    def test_non_ascii_costs_more(self):
        # Cyrillic is multi-byte in UTF-8, so the byte heuristic must not
        # under-report it.
        assert count_tokens("привет" * 20) > count_tokens("hello" * 20) / 2


class TestCountMessageTokens:
    def test_basic(self):
        assert count_message_tokens({"role": "user", "content": "hello"}) > 0

    def test_cache_is_transparent(self):
        msg = {"role": "user", "content": "hello world"}
        first = count_message_tokens(msg)
        assert count_message_tokens(msg) == first

    def test_cache_invalidated_on_edit(self):
        msg = {"role": "user", "content": "short"}
        before = count_message_tokens(msg)
        msg["content"] = "a much longer piece of content " * 20
        assert count_message_tokens(msg) > before

    def test_cache_key_not_counted_as_content(self):
        msg = {"role": "user", "content": "hello"}
        cached = count_message_tokens(msg)
        fresh = count_message_tokens({"role": "user", "content": "hello"})
        assert cached == fresh

    def test_tool_calls_counted(self):
        plain = {"role": "assistant", "content": ""}
        with_calls = {
            "role": "assistant",
            "content": "",
            "tool_calls": [{"id": "1", "function": {"name": "read_file", "arguments": "{}"}}],
        }
        assert count_message_tokens(with_calls) > count_message_tokens(plain)

    def test_non_dict_handled(self):
        assert count_message_tokens("not a dict") > 0

    def test_unserialisable_value_does_not_raise(self):
        assert count_message_tokens({"role": "user", "content": object()}) > 0
