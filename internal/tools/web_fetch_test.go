package tools

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

func TestWebFetchDecodesCharsetAndStripsNoise(t *testing.T) {
	page := `<html><head><meta charset="windows-1251"><title>Заголовок</title>` +
		`<style>p { color: red }</style><script>var x = "<b>плохо</b>";</script></head>` +
		`<body><nav>меню</nav><h1>Статья</h1>` +
		`<p>Первый <b>абзац</b> &amp; текст</p>` +
		`<pre>    def f():
    <span>return</span> 1</pre>` +
		`<div>блок один</div><div>блок два</div><footer>подвал</footer></body></html>`
	body, err := charmap.Windows1251.NewEncoder().Bytes([]byte(page))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	got := WebFetch(srv.URL, 5)
	for _, want := range []string{"Статья", "Первый абзац & текст", "    def f():\n    return 1", "блок один", "блок два"} {
		if !strings.Contains(got, want) {
			t.Errorf("extracted text misses %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"меню", "подвал", "плохо", "color: red", "Заголовок"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("extracted text contains %q:\n%s", unwanted, got)
		}
	}
}

func TestWebFetchRejectsBinary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("MZ\x00\x00"))
	}))
	t.Cleanup(srv.Close)

	if got := WebFetch(srv.URL, 5); !strings.Contains(got, "non-text content") {
		t.Fatalf("binary content = %q", got)
	}
}

func TestIsReadable(t *testing.T) {
	for ct, want := range map[string]bool{
		"text/html; charset=utf-8": true, "application/json": true, "application/vnd.api+json": true,
		"application/xml": true, "": true, "application/octet-stream": false, "image/png": false,
		"application/zip": false,
	} {
		if got := isReadable(ct); got != want {
			t.Errorf("isReadable(%q) = %v, want %v", ct, got, want)
		}
	}
}
