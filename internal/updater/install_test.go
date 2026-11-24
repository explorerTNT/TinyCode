package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

const payload = "PAYLOAD"

// buildArchive packs the payload the way the release workflow does for the
// running platform.
func buildArchive(t *testing.T) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	name := "tiny-code_v1.0.0_" + runtime.GOOS + "_" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		zw := zip.NewWriter(&buf)
		f, err := zw.Create("tiny-code_v1.0.0_" + runtime.GOOS + "_" + runtime.GOARCH + "/" + binaryName())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(payload)); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return name + ".zip", buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name + "/" + binaryName(), Mode: 0o755, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return name + ".tar.gz", buf.Bytes()
}

func TestDownloadAndExtractVerifiesChecksum(t *testing.T) {
	name, archive := buildArchive(t)
	sum := sha256.Sum256(archive)

	mux := http.NewServeMux()
	mux.HandleFunc("/release.bin", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) })
	mux.HandleFunc("/tampered.bin", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(append(append([]byte{}, archive...), 'x'))
	})
	mux.HandleFunc("/SHA256SUMS.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "%x  %s\n%x  other.tar.gz\n", sum, name, sha256.Sum256([]byte("other")))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	release := &Release{Tag: "v1.0.0", Assets: []Asset{
		{Name: name, BrowserDownloadURL: srv.URL + "/release.bin"},
		{Name: checksumsAsset, BrowserDownloadURL: srv.URL + "/SHA256SUMS.txt"},
	}}
	asset, err := findAsset(release)
	if err != nil {
		t.Fatal(err)
	}

	bin, err := downloadAndExtract(asset, release)
	if err != nil {
		t.Fatalf("verified archive rejected: %v", err)
	}
	if string(bin) != payload {
		t.Fatalf("extracted %q", bin)
	}

	tampered := *asset
	tampered.BrowserDownloadURL = srv.URL + "/tampered.bin"
	if _, err := downloadAndExtract(&tampered, release); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("a modified archive must be refused, got %v", err)
	}

	noSums := &Release{Tag: "v1.0.0", Assets: []Asset{*asset}}
	if _, err := downloadAndExtract(asset, noSums); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("a release without checksums must be refused, got %v", err)
	}
}

func TestChecksumFor(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	sums := strings.ToUpper(sum) + "  a.zip\n" + strings.Repeat("0", 64) + " *b.zip\nshort hash  c.zip\n"
	if got, err := checksumFor(sums, "a.zip"); err != nil || got != sum {
		t.Fatalf("checksumFor(a.zip) = (%q, %v)", got, err)
	}
	if got, err := checksumFor(sums, "b.zip"); err != nil || got != strings.Repeat("0", 64) {
		t.Fatalf("binary-mode line not read: (%q, %v)", got, err)
	}
	for _, name := range []string{"c.zip", "missing.zip"} {
		if _, err := checksumFor(sums, name); err == nil {
			t.Errorf("checksumFor(%q) should fail", name)
		}
	}
}
