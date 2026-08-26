package tools

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

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
	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("Error searching web: HTTP %d", resp.StatusCode)
	}

	type result struct {
		title, snippet, href string
	}
	var results []result

	z := html.NewTokenizer(resp.Body)
	var title, snippet, link string
	inSnippet := false
	snippetDepth := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			for _, a := range tok.Attr {
				if a.Key != "class" {
					continue
				}
				if strings.Contains(a.Val, "result__a") {
					for _, h := range tok.Attr {
						if h.Key == "href" {
							link = decodeUDDG(h.Val)
						}
					}
				} else if strings.Contains(a.Val, "result__snippet") {
					inSnippet = true
					snippetDepth = 1
				}
			}
		case html.TextToken:
			text := strings.TrimSpace(html.UnescapeString(string(z.Text())))
			if text == "" {
				continue
			}
			if inSnippet {
				if snippet == "" {
					snippet = text
				} else {
					snippet += " " + text
				}
			} else if title == "" {
				title = text
			}
		case html.EndTagToken:
			if inSnippet {
				snippetDepth--
				if snippetDepth <= 0 {
					inSnippet = false
					results = append(results, result{title: title, snippet: snippet, href: link})
					title, snippet, link = "", "", ""
				}
			}
		}
		if len(results) >= maxResults {
			break
		}
	}

	if len(results) == 0 {
		return fmt.Sprintf("No results for '%s'", query)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- Web search: %s (%d results) ---", query, len(results))
	for i, r := range results {
		snippet := cleanWS(r.snippet)
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		fmt.Fprintf(&b, "\n\n%d. %s\n   %s\n   %s", i+1, r.title, snippet, r.href)
	}
	return b.String()
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
