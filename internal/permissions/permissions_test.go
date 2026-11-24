package permissions

import (
	"errors"
	"strings"
	"testing"
)

// refuseAsker fails the test if the service asks anything.
func refuseAsker(t *testing.T) InputFunc {
	t.Helper()
	return func(prompt string) (string, error) {
		t.Errorf("unexpected prompt: %q", prompt)
		return "", errors.New("no input in tests")
	}
}

func TestAskApprovesReadOnlyWithoutPrompt(t *testing.T) {
	s := New(ModeAsk, t.TempDir(), refuseAsker(t))
	if !s.CheckBash("git log --oneline -10") {
		t.Fatal("read-only command should be approved in ask mode")
	}
	if !s.CheckBash("Get-Content main.go") {
		t.Fatal("workspace read should be approved in ask mode")
	}
}

func TestAskPromptsForEverythingElse(t *testing.T) {
	var prompts []string
	answers := map[string]string{}
	s := New(ModeAsk, t.TempDir(), func(prompt string) (string, error) {
		prompts = append(prompts, prompt)
		for fragment, answer := range answers {
			if strings.Contains(prompt, fragment) {
				return answer, nil
			}
		}
		return "n", nil
	})

	answers["git branch -D main"] = "n"
	if s.CheckBash("git branch -D main") {
		t.Fatal(`"n" must reject`)
	}
	answers["npm test"] = "y"
	if !s.CheckBash("npm test") {
		t.Fatal(`"y" must approve`)
	}
	answers["Remove-Item -Recurse build"] = "YES"
	if !s.CheckBash("Remove-Item -Recurse build") {
		t.Fatal(`"yes" must approve regardless of case`)
	}
	if len(prompts) != 3 {
		t.Fatalf("expected 3 prompts, got %d: %q", len(prompts), prompts)
	}
	if s.Mode() != ModeAsk {
		t.Fatalf("mode changed unexpectedly: %s", s.Mode())
	}
}

func TestAlwaysSwitchesToAuto(t *testing.T) {
	s := New(ModeAsk, t.TempDir(), func(string) (string, error) { return "a", nil })
	if !s.CheckWeb("https://example.com") {
		t.Fatal(`"a" must approve`)
	}
	if s.Mode() != ModeAuto {
		t.Fatalf("mode = %s, want auto", s.Mode())
	}
	if !s.CheckBash("make") || !s.CheckWeb("anything") || !s.CheckWrite("x.txt") {
		t.Fatal("auto mode must approve everything")
	}
}

func TestInputErrorRejects(t *testing.T) {
	s := New(ModeAsk, t.TempDir(), func(string) (string, error) { return "", errors.New("closed") })
	if s.CheckBash("make") || s.CheckWeb("query") || s.CheckWrite("x.txt") {
		t.Fatal("an input error must count as a rejection")
	}
}

func TestDenyBlocksEverything(t *testing.T) {
	s := New(ModeDeny, t.TempDir(), refuseAsker(t))
	if s.CheckBash("git status") || s.CheckWeb("query") || s.CheckWrite("x.txt") {
		t.Fatal("deny mode must reject without prompting")
	}
}

func TestAutoAllowsEverything(t *testing.T) {
	s := New(ModeAuto, t.TempDir(), refuseAsker(t))
	if !s.CheckBash("make") || !s.CheckWeb("query") || !s.CheckWrite("x.txt") {
		t.Fatal("auto mode must approve without prompting")
	}
}

func TestZeroModeMeansAsk(t *testing.T) {
	s := New(0, t.TempDir(), refuseAsker(t))
	if s.Mode() != ModeAsk {
		t.Fatalf("mode = %s, want ask", s.Mode())
	}
}
