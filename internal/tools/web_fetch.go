package tools

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// WebFetch fetches and extracts readable text from a URL.
func WebFetch(urlStr string, timeout int) string {
	if timeout < 1 {
		timeout = 15
	}
	if timeout > 60 {
		timeout = 60
	}

	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return fmt.Sprintf("Error fetching URL: %v", err)
	}
	req.Header.Set("User-Agent", browserUA)

	resp, err := client.Do(req)
	if err != nil {
		if isTimeout(err) {
			return fmt.Sprintf("Error: Request timed out after %ds", timeout)
		}
		return fmt.Sprintf("Error fetching URL: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("Error HTTP %d: %s", resp.StatusCode, urlStr)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Sprintf("Error fetching URL: %v", err)
	}

	text := htmlToText(string(body))
	text = re3Newlines.ReplaceAllString(text, "\n\n")
	text = strings.TrimSpace(text)
	if len(text) > 8000 {
		text = text[:8000] + "\n\n[... truncated at 8000 chars]"
	}
	return fmt.Sprintf("--- %s ---\n%s", urlStr, text)
}

var re3Newlines = regexp.MustCompile(`\n{3,}`)

func isTimeout(err error) bool {
	type timeout interface{ Timeout() bool }
	if t, ok := err.(timeout); ok {
		return t.Timeout()
	}
	return false
}

var skipTags = map[string]bool{"script": true, "style": true, "nav": true, "footer": true, "noscript": true, "head": true}

// htmlToText converts HTML to plain text, mirroring the Python _html_to_text
// (strip script/style/nav/footer, newlines after blocks, bullets for li).
func htmlToText(src string) string {
	var b strings.Builder
	z := html.NewTokenizer(strings.NewReader(src))
	skipDepth := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.StartTagToken:
			tok := z.Token()
			if skipTags[strings.ToLower(tok.Data)] {
				skipDepth++
			}
			switch strings.ToLower(tok.Data) {
			case "br":
				if skipDepth == 0 {
					b.WriteString("\n")
				}
			case "li":
				if skipDepth == 0 {
					b.WriteString("  * ")
				}
			}
		case html.EndTagToken:
			tok := z.Token()
			if skipTags[strings.ToLower(tok.Data)] && skipDepth > 0 {
				skipDepth--
			}
			switch strings.ToLower(tok.Data) {
			case "p", "h1", "h2", "h3", "h4", "h5", "h6":
				if skipDepth == 0 {
					b.WriteString("\n\n")
				}
			case "li":
				if skipDepth == 0 {
					b.WriteString("\n")
				}
			}
		case html.TextToken:
			if skipDepth == 0 {
				b.WriteString(html.UnescapeString(string(z.Text())))
			}
		}
	}

	out := b.String()
	out = regexp.MustCompile(`[ \t]+`).ReplaceAllString(out, " ")
	out = regexp.MustCompile(`\n\s*\n`).ReplaceAllString(out, "\n\n")
	return strings.TrimSpace(out)
}
