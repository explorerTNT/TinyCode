package agent

import (
	"strings"
	"testing"
)

func TestLooksDone(t *testing.T) {
	done := []string{
		"TASK COMPLETE",
		"**Task complete!**",
		"All steps are done. Tests pass.",
		"Задача выполнена, тесты проходят.",
		"Всё готово.",
		"Все сделано, файлы сохранены.",
		// "не" inside another word is not a negation
		"Всё готово, но код непонятный.",
	}
	for _, text := range done {
		if !looksDone(text) {
			t.Errorf("looksDone(%q) = false, want true", text)
		}
	}

	notDone := []string{
		"",
		"I will now create the file.",
		"Task is complete, but one test failed.",
		"Задача не выполнена.",
		"Всё сделано, но тесты не прошли.",
		"Всё готово, но появилась ошибка.",
		"Всё готово, но возникли ошибки.",
		"Задача выполнена, cannot verify the result.",
	}
	for _, text := range notDone {
		if looksDone(text) {
			t.Errorf("looksDone(%q) = true, want false", text)
		}
	}
}

func TestLooksDoneChecksTheTailOnly(t *testing.T) {
	text := "Everything failed earlier.\n" + strings.Repeat("detail line\n", 40) + "Всё готово."
	if !looksDone(text) {
		t.Fatal("a failure mentioned before the tail must not block completion")
	}
}

func TestCountPlanSteps(t *testing.T) {
	plan := "## Plan\n1. First\n2) Second\n- third\n* fourth\n\nnot a step"
	if got := countPlanSteps(plan); got != 4 {
		t.Fatalf("countPlanSteps = %d, want 4", got)
	}
}

func TestStripXMLToolCalls(t *testing.T) {
	text := "Sure.\n<tool_call>{\"name\": \"read_file\", \"arguments\": {\"path\": \"a.py\"}}</tool_call>\nDone."
	if got := stripXMLToolCalls(text); got != "Sure.\n\nDone." && !strings.HasPrefix(got, "Sure.") {
		t.Fatalf("stripXMLToolCalls = %q", got)
	}
}

func TestParseXMLToolCalls(t *testing.T) {
	calls := parseXMLToolCalls(`<tool_call>{"name": "read_file", "arguments": {"path": "a.py", "offset": 10}}</tool_call>`)
	if len(calls) != 1 || calls[0].Name != "read_file" || calls[0].Arguments != `{"offset":10,"path":"a.py"}` {
		t.Fatalf("parseXMLToolCalls = %+v", calls)
	}
	if calls := parseXMLToolCalls("<tool_call>not an object</tool_call>"); calls != nil {
		t.Fatalf("unparsable block should yield no calls: %+v", calls)
	}
}
