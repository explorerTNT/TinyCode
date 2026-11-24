package tools

import (
	"strings"
	"testing"
)

func TestParseSearchResults(t *testing.T) {
	page := `<div class="result">
		<h2 class="result__title"><a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Faider.chat%2Fdocs%2Fedit&amp;rut=abc">Edit <b>formats</b> | aider</a></h2>
		<a class="result__snippet" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Faider.chat%2Fdocs%2Fedit&amp;rut=abc">The <b>diff</b> edit format asks the LLM to specify file edits as a series of blocks.</a>
		<a class="result__url" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Faider.chat%2Fdocs%2Fedit&amp;rut=abc">aider.chat/docs/edit</a>
	</div>
	<div class="result">
		<a class="result__a" href="https://example.org/x">Second <em>result</em></a>
		<div class="result__snippet">Plain snippet</div>
	</div>
	<div class="result"><a class="result__a" href="https://example.org/empty"></a></div>`

	results := parseSearchResults(strings.NewReader(page))
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3: %+v", len(results), results)
	}
	first := results[0]
	if first.title != "Edit formats | aider" {
		t.Errorf("title = %q", first.title)
	}
	// The <b> around matched terms must not cut the snippet short.
	if first.snippet != "The diff edit format asks the LLM to specify file edits as a series of blocks." {
		t.Errorf("snippet = %q", first.snippet)
	}
	if first.href != "https://aider.chat/docs/edit" {
		t.Errorf("href = %q", first.href)
	}
	// The display URL after the first snippet must not leak into the next title.
	if results[1].title != "Second result" || results[1].snippet != "Plain snippet" {
		t.Errorf("second result = %+v", results[1])
	}
	if results[2].href != "https://example.org/empty" {
		t.Errorf("third result = %+v", results[2])
	}
}

func TestParseSearchResultsEmpty(t *testing.T) {
	if got := parseSearchResults(strings.NewReader("<html>challenge</html>")); len(got) != 0 {
		t.Fatalf("expected no results, got %+v", got)
	}
}
