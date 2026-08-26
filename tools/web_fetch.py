import html as html_module
import re

# A response is streamed and abandoned once it exceeds this. Without a cap,
# `web_fetch` on a large binary pulled the whole thing into memory before
# discovering it was not text.
MAX_DOWNLOAD_BYTES = 5_000_000
MAX_TEXT_CHARS = 8000

TEXTUAL_TYPES = (
    "text/", "application/json", "application/xml", "application/xhtml",
    "application/rss", "application/atom", "application/javascript",
    "+json", "+xml",
)


def web_fetch(url: str, timeout: int = 15) -> str:
    """Fetch and read content from a URL. Use this to read docs, articles, web pages.

    Args:
        url: The full URL to fetch
        timeout: Request timeout in seconds (default 15, max 60)
    """
    try:
        import requests
    except ImportError:
        return "Error: the 'requests' package is not installed"

    url = (url or "").strip()
    if not url:
        return "Error: empty URL"
    if not re.match(r"^https?://", url, re.IGNORECASE):
        # A bare domain is what a small model usually produces; assume https
        # rather than failing on a technicality.
        if re.match(r"^[\w.-]+\.\w{2,}", url):
            url = "https://" + url
        else:
            return f"Error: not a valid http(s) URL: {url}"

    try:
        timeout = int(timeout)
    except (TypeError, ValueError):
        timeout = 15
    safe_timeout = max(1, min(timeout, 60))

    headers = {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
    }

    try:
        with requests.get(
            url, headers=headers, timeout=safe_timeout, stream=True
        ) as resp:
            resp.raise_for_status()

            content_type = (resp.headers.get("Content-Type") or "").lower()
            if content_type and not any(t in content_type for t in TEXTUAL_TYPES):
                return (
                    f"Error: {url} is '{content_type.split(';')[0]}', not text. "
                    "web_fetch only reads text pages."
                )

            declared = resp.headers.get("Content-Length")
            if declared and declared.isdigit() and int(declared) > MAX_DOWNLOAD_BYTES:
                return (
                    f"Error: {url} is {int(declared) // 1_000_000}MB, too large to read. "
                    "Fetch a more specific page."
                )

            chunks = []
            total = 0
            for chunk in resp.iter_content(chunk_size=65536):
                if not chunk:
                    continue
                total += len(chunk)
                if total > MAX_DOWNLOAD_BYTES:
                    break
                chunks.append(chunk)

            raw = b"".join(chunks)
            encoding = resp.encoding or resp.apparent_encoding or "utf-8"

        try:
            page = raw.decode(encoding, errors="replace")
        except (LookupError, UnicodeDecodeError):
            page = raw.decode("utf-8", errors="replace")

        text = _html_to_text(page)
        text = re.sub(r"\n{3,}", "\n\n", text).strip()

        if not text:
            return f"--- {url} ---\n(page had no readable text)"

        if len(text) > MAX_TEXT_CHARS:
            text = text[:MAX_TEXT_CHARS] + f"\n\n[... truncated at {MAX_TEXT_CHARS} chars]"

        return f"--- {url} ---\n{text}"
    except Exception as e:
        name = type(e).__name__
        if "Timeout" in name:
            return f"Error: Request timed out after {safe_timeout}s"
        if name == "HTTPError":
            status = getattr(getattr(e, "response", None), "status_code", "?")
            return f"Error HTTP {status}: {url}"
        if "ConnectionError" in name:
            return f"Error: could not connect to {url}"
        return f"Error fetching URL: {e}"


def _html_to_text(page: str) -> str:
    page = re.sub(r"<script[^>]*>.*?</script>", "", page, flags=re.DOTALL | re.IGNORECASE)
    page = re.sub(r"<style[^>]*>.*?</style>", "", page, flags=re.DOTALL | re.IGNORECASE)
    page = re.sub(r"<nav[^>]*>.*?</nav>", "", page, flags=re.DOTALL | re.IGNORECASE)
    page = re.sub(r"<footer[^>]*>.*?</footer>", "", page, flags=re.DOTALL | re.IGNORECASE)
    page = re.sub(r"<!--.*?-->", "", page, flags=re.DOTALL)

    page = re.sub(r"<br\s*/?>", "\n", page, flags=re.IGNORECASE)
    page = re.sub(r"</p>", "\n\n", page, flags=re.IGNORECASE)
    page = re.sub(r"</h[1-6]>", "\n\n", page, flags=re.IGNORECASE)
    page = re.sub(r"<li[^>]*>", "  * ", page, flags=re.IGNORECASE)
    page = re.sub(r"</li>", "\n", page, flags=re.IGNORECASE)
    page = re.sub(r"<[^>]+>", "", page)

    # The hand-rolled entity table missed most named entities and crashed on an
    # out-of-range numeric one (`chr(99999999)` raises). The stdlib handles the
    # full set and leaves anything invalid as literal text.
    page = html_module.unescape(page)

    page = re.sub(r"[ \t]+", " ", page)
    page = re.sub(r"\n\s*\n", "\n\n", page)

    return page.strip()
