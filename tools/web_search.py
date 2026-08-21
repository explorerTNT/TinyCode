def web_search(query: str, max_results: int = 5) -> str:
    """Search the web for information. Use this to find answers, docs, or current info.
    Args:
        query: The search query
        max_results: Max results to return (1-10, default 5)
    """
    try:
        import warnings
        with warnings.catch_warnings():
            warnings.simplefilter("ignore")
            from duckduckgo_search import DDGS

        num = min(max(max_results, 1), 10)
        results = []

        with DDGS(headers={"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64)"}) as ddgs:
            for i, r in enumerate(ddgs.text(query, max_results=num)):
                title = r.get("title", "")
                body = r.get("body", "")
                href = r.get("href", "")
                snippet = _clean(body)[:300]
                results.append(f"{i+1}. {title}\n   {snippet}\n   {href}")

        if not results:
            return f"No results for '{query}'"

        return f"--- Web search: {query} ({len(results)} results) ---\n" + "\n\n".join(results)
    except ImportError:
        return "Error: duckduckgo_search not installed. Run: pip install duckduckgo_search"
    except Exception as e:
        return f"Error searching web: {e}"


def _clean(text: str) -> str:
    import re
    text = re.sub(r"\s+", " ", text)
    return text.strip()
