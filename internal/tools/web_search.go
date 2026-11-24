package tools

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// maxSearchPageBytes is how much of a results page is parsed.
const maxSearchPageBytes = 2 << 20

// WebSearch searches DuckDuckGo's HTML endpoint and returns up to maxResults.
func WebSearch(query string, maxResults int) string {
	if maxResults < 1 {
		maxResults = 1
	}
	if maxResults > 10 {
		maxResults = 10
	}

	client := &http.Client{Timeout: 20 * time.Second}
	form := url.Values{"q": []string{query}}
	req, err := http.NewRequest("POST", "https://html.duckduckgo.com/html/", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Sprintf("Error searching web: %v", err)
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("Error searching web: %v", err)
	}
	defer resp.Body.Close()
	// 202 is the anti-bot challenge page; its body is not results.
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusForbidden {
			return fmt.Sprintf("Error: the search engine blocked the request (HTTP %d, anti-bot check). Wait a minute, or fetch a known URL with web_fetch.", resp.StatusCode)
		}
		return fmt.Sprintf("Error searching web: HTTP %d", resp.StatusCode)
	}

	results := parseSearchResults(io.LimitReader(resp.Body, maxSearchPageBytes))
	if len(results) == 0 {
		return fmt.Sprintf("No results for '%s'", query)
	}
	if len(results) > maxResults {
		results = results[:maxResults]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- Web search: %s (%d results) ---", query, len(results))
	for i, r := range results {
		snippet := cleanWS(r.snippet)
		if len(snippet) > 300 {
			snippet = truncateRunes(snippet, 300)
		}
		fmt.Fprintf(&b, "\n\n%d. %s\n   %s\n   %s", i+1, r.title, snippet, r.href)
	}
	return b.String()
}

type searchResult struct {
	title, snippet, href string
}

// parseSearchResults reads the result links and snippets of a DuckDuckGo
// HTML page. Text is taken only from inside those two elements, and an
// element ends when its own closing tag comes: nested tags such as the
// <b> around matched terms must not cut a snippet short.
func parseSearchResults(r io.Reader) []searchResult {
	const (
		outside = iota
		inTitle
		inSnippet
	)
	var (
		results []searchResult
		cur     searchResult
		buf     strings.Builder
		zone    = outside
		depth   int
	)
	close := func() {
		switch zone {
		case inTitle:
			cur.title = cleanWS(buf.String())
		case inSnippet:
			cur.snippet = cleanWS(buf.String())
			results = append(results, cur)
			cur = searchResult{}
		}
		buf.Reset()
		zone = outside
	}

	z := html.NewTokenizer(r)
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			close()
			// The last result may have had no snippet.
			if cur.title != "" || cur.href != "" {
				results = append(results, cur)
			}
			return results
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			if zone == outside {
				class := tokenAttr(tok, "class")
				switch {
				case strings.Contains(class, "result__a"):
					// A result is complete once its snippet closes; one
					// without a snippet ends when the next result starts.
					if cur.title != "" || cur.href != "" {
						results = append(results, cur)
					}
					zone, depth = inTitle, 0
					cur = searchResult{href: decodeUDDG(tokenAttr(tok, "href"))}
				case strings.Contains(class, "result__snippet"):
					zone, depth = inSnippet, 0
				default:
					continue
				}
				buf.Reset()
			}
			if tt == html.StartTagToken && !voidTags[strings.ToLower(tok.Data)] {
				depth++
			}
		case html.EndTagToken:
			if zone != outside {
				depth--
				if depth <= 0 {
					close()
				}
			}
		case html.TextToken:
			if zone != outside {
				buf.WriteString(html.UnescapeString(string(z.Text())))
			}
		}
	}
}

func tokenAttr(tok html.Token, key string) string {
	for _, a := range tok.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func decodeUDDG(href string) string {
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	if v := u.Query().Get("uddg"); v != "" {
		return v
	}
	return href
}

func cleanWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
