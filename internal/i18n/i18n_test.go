package i18n

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestCatalogsHaveTheSameKeys(t *testing.T) {
	for key := range catalog[En] {
		if _, ok := catalog[Ru][key]; !ok {
			t.Errorf("key %q has no Russian text", key)
		}
	}
	for key := range catalog[Ru] {
		if _, ok := catalog[En][key]; !ok {
			t.Errorf("key %q has no English text", key)
		}
	}
}

// Both languages receive the same arguments, so both texts must consume the
// same format verbs in the same order.
func TestCatalogsUseTheSameFormatVerbs(t *testing.T) {
	reVerb := regexp.MustCompile(`%[-+ #0]*[0-9.*]*[a-zA-Z]`)
	verbs := func(s string) string {
		var v []string
		for _, m := range reVerb.FindAllString(s, -1) {
			if m != "%%" {
				v = append(v, m)
			}
		}
		sort.Strings(v)
		return strings.Join(v, " ")
	}
	for key, en := range catalog[En] {
		ru, ok := catalog[Ru][key]
		if !ok {
			continue
		}
		if ve, vr := verbs(en), verbs(ru); ve != vr {
			t.Errorf("%s: format verbs differ (en %q, ru %q)", key, ve, vr)
		}
	}
}

// A missing key would reach the user as its raw name, so every key used
// through i18n.T has to exist in both catalogs.
func TestUsedKeysExist(t *testing.T) {
	reKey := regexp.MustCompile(`i18n\.T\("([a-z]+\.[a-z_]+)"`)
	// Absolute, so the repository root itself is not mistaken for a
	// dot-directory by the skip rule below.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	err = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range reKey.FindAllStringSubmatch(string(data), -1) {
			used[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(used) < 50 {
		t.Fatalf("only %d keys found, the source scan is broken", len(used))
	}
	for key := range used {
		for _, lang := range []Lang{En, Ru} {
			if _, ok := catalog[lang][key]; !ok {
				t.Errorf("i18n.T(%q) has no %s text", key, lang)
			}
		}
	}
}

func TestParse(t *testing.T) {
	for in, want := range map[string]Lang{"ru": Ru, "РУССКИЙ": Ru, " Russian ": Ru, "en": En, "": En, "de": En} {
		if got := Parse(in); got != want {
			t.Errorf("Parse(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTFallsBackToKey(t *testing.T) {
	Set(En)
	if got := T("no.such_key"); got != "no.such_key" {
		t.Fatalf("T(missing) = %q", got)
	}
}
