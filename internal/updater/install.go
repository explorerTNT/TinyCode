package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// checksumsAsset is published with every release by the release workflow.
const checksumsAsset = "SHA256SUMS.txt"

// Install downloads the release archive for the current platform, checks it
// against the checksums published with the release, and replaces the running
// binary. It is safe to call from a goroutine.
func Install(release *Release) error {
	if release == nil {
		return fmt.Errorf("no release provided")
	}

	asset, err := findAsset(release)
	if err != nil {
		return err
	}

	bin, err := downloadAndExtract(asset, release)
	if err != nil {
		return err
	}

	return replaceBinary(bin)
}

func findAsset(release *Release) (*Asset, error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// Build expected suffixes: e.g. "windows_amd64", "linux_arm64"
	patterns := []string{
		goos + "_" + goarch,
		goarch + "_" + goos,
	}

	for i := range release.Assets {
		name := strings.ToLower(release.Assets[i].Name)
		for _, p := range patterns {
			if strings.Contains(name, p) {
				return &release.Assets[i], nil
			}
		}
	}

	return nil, fmt.Errorf("no asset found for %s/%s in release %s", goos, goarch, release.Tag)
}

func downloadAndExtract(asset *Asset, release *Release) ([]byte, error) {
	data, err := download(asset.BrowserDownloadURL)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	if err := verifyChecksum(release, asset.Name, data); err != nil {
		return nil, err
	}

	if strings.HasSuffix(asset.Name, ".zip") {
		return extractZip(data)
	}
	if strings.HasSuffix(asset.Name, ".tar.gz") || strings.HasSuffix(asset.Name, ".tgz") {
		return extractTarGz(data)
	}

	// Bare binary (no archive).
	return data, nil
}

func download(url string) ([]byte, error) {
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("download read: %w", err)
	}
	return data, nil
}

// verifyChecksum compares the archive with its line in SHA256SUMS.txt. A
// release without checksums is refused: an unverified binary must not
// replace the running one.
func verifyChecksum(release *Release, name string, data []byte) error {
	sums, err := findAssetNamed(release, checksumsAsset)
	if err != nil {
		return err
	}
	text, err := download(sums.BrowserDownloadURL)
	if err != nil {
		return fmt.Errorf("checksums: %w", err)
	}
	want, err := checksumFor(string(text), name)
	if err != nil {
		return err
	}

	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", name, want, got)
	}
	return nil
}

func findAssetNamed(release *Release, name string) (*Asset, error) {
	for i := range release.Assets {
		if release.Assets[i].Name == name {
			return &release.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("release %s has no %s, refusing to install an unverified binary", release.Tag, name)
}

// checksumFor reads the hash of name out of a sha256sum listing, where a
// line is "<hash>  <name>" or "<hash> *<name>" in binary mode.
func checksumFor(sums, name string) (string, error) {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		if sum := strings.ToLower(fields[0]); isSHA256(sum) {
			return sum, nil
		}
	}
	return "", fmt.Errorf("%s has no entry for %s", checksumsAsset, name)
}

var reSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

func isSHA256(s string) bool { return reSHA256.MatchString(s) }

func extractZip(data []byte) ([]byte, error) {
	r, err := zip.NewReader(strings.NewReader(string(data)), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("zip open: %w", err)
	}

	binName := binaryName()
	for _, f := range r.File {
		if filepath.Base(f.Name) == binName {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}

	return nil, fmt.Errorf("binary %s not found in zip", binName)
}

func extractTarGz(data []byte) ([]byte, error) {
	gr, err := gzip.NewReader(strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("gzip open: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	binName := binaryName()

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar read: %w", err)
		}

		if filepath.Base(hdr.Name) == binName {
			return io.ReadAll(tr)
		}
	}

	return nil, fmt.Errorf("binary %s not found in tar.gz", binName)
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "tiny-code.exe"
	}
	return "tiny-code"
}

func replaceBinary(newBin []byte) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("resolve symlink: %w", err)
	}

	if runtime.GOOS == "windows" {
		// Windows locks running executables. Rename first, then write.
		old := self + ".old"
		_ = os.Remove(old) // best-effort cleanup

		if err := os.Rename(self, old); err != nil {
			return fmt.Errorf("rename current binary: %w", err)
		}

		if err := os.WriteFile(self, newBin, 0o755); err != nil {
			// Try to restore.
			_ = os.Rename(old, self)
			return fmt.Errorf("write new binary: %w", err)
		}

		_ = os.Remove(old)
		return nil
	}

	// Unix: write to temp, then rename over the original.
	tmp := self + ".tmp"
	if err := os.WriteFile(tmp, newBin, 0o755); err != nil {
		return fmt.Errorf("write temp binary: %w", err)
	}

	if err := os.Rename(tmp, self); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace binary: %w", err)
	}

	return nil
}
