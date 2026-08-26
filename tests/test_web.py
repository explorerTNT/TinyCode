import pytest

from tools.web_fetch import _html_to_text, web_fetch


class TestHtmlToText:
    def test_strips_tags(self):
        assert _html_to_text("<p>hello</p>").strip() == "hello"

    def test_removes_script(self):
        assert "alert" not in _html_to_text("<script>alert(1)</script>text")

    def test_removes_style(self):
        assert "color" not in _html_to_text("<style>a{color:red}</style>text")

    def test_removes_comments(self):
        assert "secret" not in _html_to_text("<!-- secret -->visible")

    def test_named_entities(self):
        assert _html_to_text("a&amp;b").strip() == "a&b"

    def test_extended_named_entity(self):
        # The hand-rolled table only knew five entities.
        assert "\u00a0" in _html_to_text("a&nbsp;b") or " " in _html_to_text("a&nbsp;b")

    def test_numeric_entity(self):
        assert _html_to_text("&#65;").strip() == "A"

    def test_out_of_range_entity_does_not_crash(self):
        # chr(99999999) raises ValueError, which used to abort the whole fetch.
        assert _html_to_text("&#99999999;") is not None

    def test_list_items(self):
        out = _html_to_text("<ul><li>one</li><li>two</li></ul>")
        assert "one" in out and "two" in out


class TestWebFetchValidation:
    def test_empty_url(self):
        assert "Error" in web_fetch("")

    def test_invalid_url(self):
        assert "Error" in web_fetch("not a url at all")

    def test_unreachable_host_reports_cleanly(self):
        result = web_fetch("http://127.0.0.1:9/nothing", timeout=2)
        assert result.startswith("Error")


class TestWebFetchWithStub:
    @pytest.fixture
    def stub_requests(self, monkeypatch):
        import tools.web_fetch as wf

        class FakeResponse:
            def __init__(self, body=b"<p>hi</p>", headers=None, status=200):
                self._body = body
                self.headers = headers or {"Content-Type": "text/html"}
                self.status_code = status
                self.encoding = "utf-8"
                self.apparent_encoding = "utf-8"

            def __enter__(self):
                return self

            def __exit__(self, *a):
                return False

            def raise_for_status(self):
                pass

            def iter_content(self, chunk_size=65536):
                for i in range(0, len(self._body), chunk_size):
                    yield self._body[i:i + chunk_size]

        def install(**kwargs):
            response = FakeResponse(**kwargs)
            monkeypatch.setattr(
                wf, "requests", type("M", (), {"get": staticmethod(lambda *a, **k: response)})(),
                raising=False,
            )
            import sys
            fake_module = type("M", (), {"get": staticmethod(lambda *a, **k: response)})()
            monkeypatch.setitem(sys.modules, "requests", fake_module)
            return response

        return install

    def test_reads_text_page(self, stub_requests):
        stub_requests(body=b"<p>hello world</p>")
        assert "hello world" in web_fetch("https://example.com")

    def test_binary_content_type_refused(self, stub_requests):
        stub_requests(headers={"Content-Type": "application/octet-stream"})
        assert "not text" in web_fetch("https://example.com/file.bin")

    def test_declared_oversize_refused(self, stub_requests):
        stub_requests(headers={
            "Content-Type": "text/html", "Content-Length": "900000000",
        })
        assert "too large" in web_fetch("https://example.com/big")

    def test_download_capped(self, stub_requests):
        stub_requests(body=b"<p>x</p>" * 2_000_000)
        out = web_fetch("https://example.com/huge")
        assert len(out) < 20000

    def test_json_allowed(self, stub_requests):
        stub_requests(body=b'{"a": 1}', headers={"Content-Type": "application/json"})
        assert "a" in web_fetch("https://example.com/api")

    def test_bare_domain_gets_scheme(self, stub_requests):
        stub_requests(body=b"<p>ok</p>")
        assert "ok" in web_fetch("example.com")
