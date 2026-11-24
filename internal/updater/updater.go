package updater

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
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

// describeRe matches the version CI stamps into development builds:
// "<tag>-<commits since the tag>-g<hash>".
var describeRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)-(\d+)-g[0-9a-f]+(?:-dirty)?$`)

// Compare returns -1, 0, or 1 comparing versions a and b.
// Versions without a "v" prefix get one added automatically. A development
// build counts as newer than the tag it was cut after and older than any
// later release, so it is never offered a "downgrade" to its own tag.
// Non-semver strings fall back to lexicographic comparison.
func Compare(a, b string) int {
	tagA, aheadA, devA := parseDescribe(a)
	tagB, aheadB, devB := parseDescribe(b)
	switch {
	case devA && devB:
		if c := semver.Compare(ensureVPrefix(tagA), ensureVPrefix(tagB)); c != 0 {
			return c
		}
		return cmp.Compare(aheadA, aheadB)
	case devA:
		if c := semver.Compare(ensureVPrefix(tagA), ensureVPrefix(b)); c != 0 {
			return c
		}
		return 1
	case devB:
		if c := semver.Compare(ensureVPrefix(a), ensureVPrefix(tagB)); c != 0 {
			return c
		}
		return -1
	}

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

// parseDescribe splits a git-describe version into its tag and the number
// of commits after it. ok is false for an ordinary version.
func parseDescribe(v string) (tag string, ahead int, ok bool) {
	m := describeRe.FindStringSubmatch(v)
	if m == nil {
		return "", 0, false
	}
	ahead, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}
	return m[1], ahead, true
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
