package tools

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// maxFetchBytes is how much of a page is downloaded before extraction.
const maxFetchBytes = 4 << 20

// maxPageChars is how much extracted text one fetch returns.
const maxPageChars = 8000

var reNewlines = regexp.MustCompile(`\n{3,}`)
var reSpaces = regexp.MustCompile(`[ \t]+`)
var reBlankLines = regexp.MustCompile(`\n[ \t]*\n[ \t\n]*`)
var reIndent = regexp.MustCompile(`(?m)^[ \t]+`)

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
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json,application/xml,text/plain;q=0.9,*/*;q=0.5")

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

	contentType := resp.Header.Get("Content-Type")
	if !isReadable(contentType) {
		return fmt.Sprintf("Error: %s returned non-text content (%s). web_fetch reads pages, documents and APIs only.", urlStr, contentType)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
	if err != nil {
		return fmt.Sprintf("Error fetching URL: %v", err)
	}

	text := htmlToText(string(decodePage(body, contentType)))
	text = reNewlines.ReplaceAllString(text, "\n\n")
	text = strings.TrimSpace(text)
	if len(text) > maxPageChars {
		text = truncateRunes(text, maxPageChars) + "\n\n[... truncated at 8000 chars]"
	}
	return fmt.Sprintf("--- %s ---\n%s", urlStr, text)
}

// isReadable keeps fetches to text: markup, JSON, XML and plain text.
func isReadable(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType == "" {
		return true
	}
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/json", "application/xml", "application/xhtml+xml", "application/yaml", "application/x-yaml":
		return true
	}
	return strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml")
}

// decodePage converts a page to UTF-8 by its Content-Type charset or its
// <meta charset> tag; many Russian sites are still in Windows-1251.
func decodePage(body []byte, contentType string) []byte {
	r, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return body
	}
	decoded, err := io.ReadAll(r)
	if err != nil && len(decoded) == 0 {
		return body
	}
	return decoded
}

func isTimeout(err error) bool {
	type timeout interface{ Timeout() bool }
	if t, ok := err.(timeout); ok {
		return t.Timeout()
	}
	return false
}

var (
	skipTags = map[string]bool{
		"script": true, "style": true, "nav": true, "footer": true,
		"noscript": true, "head": true, "svg": true, "template": true, "iframe": true,
	}
	paragraphTags = map[string]bool{"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true}
	blockTags     = map[string]bool{
		"div": true, "tr": true, "pre": true, "blockquote": true, "section": true,
		"article": true, "ul": true, "ol": true, "table": true, "dd": true, "dt": true,
		"header": true, "main": true, "figure": true, "figcaption": true, "address": true,
		"details": true, "summary": true, "hr": true,
	}
	voidTags = map[string]bool{
		"br": true, "hr": true, "img": true, "input": true, "meta": true, "link": true,
		"area": true, "base": true, "col": true, "embed": true, "source": true,
		"track": true, "wbr": true,
	}
)

// htmlToText converts HTML to plain text: scripts, styles and navigation
// are dropped, block elements break lines, list items become bullets, and
// whitespace inside <pre> is kept as is.
func htmlToText(src string) string {
	type segment struct {
		text string
		pre  bool
	}
	var segments []segment
	skipDepth, preDepth := 0, 0
	emit := func(s string) {
		if s == "" {
			return
		}
		if n := len(segments); n > 0 && segments[n-1].pre == (preDepth > 0) {
			segments[n-1].text += s
			return
		}
		segments = append(segments, segment{text: s, pre: preDepth > 0})
	}

	z := html.NewTokenizer(strings.NewReader(src))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			name := strings.ToLower(z.Token().Data)
			if skipDepth > 0 {
				if skipTags[name] && tt == html.StartTagToken {
					skipDepth++
				}
				continue
			}
			if skipTags[name] && tt == html.StartTagToken {
				skipDepth = 1
				continue
			}
			switch {
			case name == "br":
				emit("\n")
			case name == "li":
				if preDepth == 0 {
					emit("\n  * ")
				}
			case name == "pre":
				if preDepth == 0 {
					emit("\n\n")
				}
				preDepth++
			case paragraphTags[name]:
				emit("\n\n")
			case blockTags[name]:
				emit("\n")
			}
		case html.EndTagToken:
			name := strings.ToLower(z.Token().Data)
			if skipDepth > 0 {
				if skipTags[name] {
					skipDepth--
				}
				continue
			}
			switch {
			case name == "pre":
				if preDepth > 0 {
					preDepth--
				}
				if preDepth == 0 {
					emit("\n\n")
				}
			case name == "li":
				if preDepth == 0 {
					emit("\n")
				}
			case paragraphTags[name]:
				emit("\n\n")
			case blockTags[name]:
				emit("\n")
			}
		case html.TextToken:
			if skipDepth == 0 {
				emit(html.UnescapeString(string(z.Text())))
			}
		}
	}

	var b strings.Builder
	for _, s := range segments {
		if s.pre {
			b.WriteString(strings.Trim(s.text, "\n"))
			b.WriteString("\n")
			continue
		}
		s.text = reSpaces.ReplaceAllString(s.text, " ")
		s.text = reBlankLines.ReplaceAllString(s.text, "\n\n")
		s.text = reIndent.ReplaceAllString(s.text, "")
		b.WriteString(s.text)
	}
	return reNewlines.ReplaceAllString(strings.TrimSpace(b.String()), "\n\n")
}
