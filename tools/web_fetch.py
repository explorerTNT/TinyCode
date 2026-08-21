def web_fetch(url: str, timeout: int = 15) -> str:
    """Fetch and read content from a URL. Use this to read docs, articles, web pages.
    Args:
        url: The full URL to fetch
        timeout: Request timeout in seconds (default 15, max 60)
    """
    try:
        import re
        import requests

        safe_timeout = min(timeout, 60)
        headers = {
            "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
        }
        resp = requests.get(url, headers=headers, timeout=safe_timeout)
        resp.raise_for_status()

        resp.encoding = resp.encoding or resp.apparent_encoding
        html = resp.text
        text = _html_to_text(html)
        text = re.sub(r"\n{3,}", "\n\n", text)
        text = text.strip()

        if len(text) > 8000:
            text = text[:8000] + "\n\n[... truncated at 8000 chars]"

        return f"--- {url} ---\n{text}"
    except ImportError:
        return "Error: the 'requests' package is not installed"
    except Exception as e:
        name = type(e).__name__
        if name == "Timeout":
            return f"Error: Request timed out after {timeout}s"
        if name == "HTTPError":
            status = getattr(getattr(e, "response", None), "status_code", "?")
            return f"Error HTTP {status}: {url}"
        return f"Error fetching URL: {e}"


def _html_to_text(html: str) -> str:
    import re

    html = re.sub(r"<script[^>]*>.*?</script>", "", html, flags=re.DOTALL | re.IGNORECASE)
    html = re.sub(r"<style[^>]*>.*?</style>", "", html, flags=re.DOTALL | re.IGNORECASE)
    html = re.sub(r"<nav[^>]*>.*?</nav>", "", html, flags=re.DOTALL | re.IGNORECASE)
    html = re.sub(r"<footer[^>]*>.*?</footer>", "", html, flags=re.DOTALL | re.IGNORECASE)

    html = re.sub(r"<br\s*/?>", "\n", html, flags=re.IGNORECASE)
    html = re.sub(r"</p>", "\n\n", html, flags=re.IGNORECASE)
    html = re.sub(r"</h[1-6]>", "\n\n", html, flags=re.IGNORECASE)
    html = re.sub(r"<li>", "  * ", html, flags=re.IGNORECASE)
    html = re.sub(r"</li>", "\n", html, flags=re.IGNORECASE)
    html = re.sub(r"<[^>]+>", "", html)

    html = re.sub(r"&amp;", "&", html)
    html = re.sub(r"&lt;", "<", html)
    html = re.sub(r"&gt;", ">", html)
    html = re.sub(r"&quot;", '"', html)
    html = re.sub(r"&#(\d+);", lambda m: chr(int(m.group(1))), html)

    html = re.sub(r"[ \t]+", " ", html)
    html = re.sub(r"\n\s*\n", "\n\n", html)

    return html.strip()
