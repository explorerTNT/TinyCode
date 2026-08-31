package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	repoOwner = "explorerTNT"
	repoName  = "TinyCode"
	apiURL    = "https://api.github.com/repos/" + repoOwner + "/" + repoName + "/releases/latest"
)

// Release holds info about a GitHub release.
type Release struct {
	Tag    string
	Notes  string
	Assets []Asset
}

// Asset is a single download artifact in a release.
type Asset struct {
	Name               string
	BrowserDownloadURL string
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// CheckLatest fetches the latest release from GitHub.
func CheckLatest() (*Release, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("no releases found")
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return nil, fmt.Errorf("github api %d: %s", resp.StatusCode, string(body))
	}

	var gh ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&gh); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}

	assets := make([]Asset, len(gh.Assets))
	for i, a := range gh.Assets {
		assets[i] = Asset{Name: a.Name, BrowserDownloadURL: a.BrowserDownloadURL}
	}

	return &Release{
		Tag:    gh.TagName,
		Notes:  gh.Body,
		Assets: assets,
	}, nil
}

// Compare returns -1, 0, or 1 comparing semver strings a and b.
// Versions without a "v" prefix get one added automatically.
// Non-semver strings fall back to lexicographic comparison.
func Compare(a, b string) int {
	sa := ensureVPrefix(a)
	sb := ensureVPrefix(b)

	if semver.IsValid(sa) && semver.IsValid(sb) {
		return semver.Compare(sa, sb)
	}

	// fallback: lexicographic
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func ensureVPrefix(v string) string {
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}

// IsDev reports whether the version is a development build.
func IsDev(version string) bool {
	return version == "" || version == "dev"
}
