package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/explorerTNT/TinyCode/internal/i18n"
)

// planStepRounds is the tool budget for one step of an approved plan.
const planStepRounds = 12

// maxStepReport caps what one step hands on to the next ones.
const maxStepReport = 300

var (
	reNumberedStep = regexp.MustCompile(`^(\d+)[.)]\s+(.+)$`)
	reBulletStep   = regexp.MustCompile(`^[-*]\s+(?:\[[ xX]\]\s+)?(.+)$`)
)

// planSteps extracts the numbered and top-level bulleted steps of a plan.
func planSteps(plan string) []string {
	var steps []string
	for _, line := range strings.Split(plan, "\n") {
		trimmed := strings.TrimSpace(line)
		if m := reNumberedStep.FindStringSubmatch(trimmed); m != nil {
			steps = append(steps, strings.TrimSpace(m[2]))
			continue
		}
		// An indented bullet belongs to the step above it.
		if line == trimmed {
			if m := reBulletStep.FindStringSubmatch(trimmed); m != nil {
				steps = append(steps, strings.TrimSpace(m[1]))
			}
		}
	}
	return steps
}

// runPlanSteps executes an approved plan one step per fresh context. A
// small model loses the thread in one long conversation: by step five it
// has forgotten the plan, repeats finished work and drifts. Here every step
// starts from nothing but the plan and a log of what the earlier steps
// reported, so the context stays short, which also keeps it cheap on slow
// hardware.
func (a *Agent) runPlanSteps(plan string) {
	steps := planSteps(plan)
	if len(steps) < 2 {
		// A single step gains nothing from the step machinery.
		a.clearMessages()
		a.addMsg(Message{Role: "system", Content: a.systemPrompt()})
		a.addMsg(Message{Role: "user", Content: "Execute this plan step by step:\n\n" + plan + "\n\nWork through each step. Read files before editing. Show progress as you go."})
		a.syncEnvState()
		a.processTurn(0, false)
		return
	}

	a.io.Println(i18n.T("plan.run_steps", len(steps)))
	var done []string
	for i, step := range steps {
		if a.aborted.Load() {
			a.io.Println(i18n.T("plan.stopped"))
			return
		}
		a.clearMessages()
		a.addMsg(Message{Role: "system", Content: a.systemPrompt()})
		a.addMsg(Message{Role: "user", Content: planStepPrompt(steps, done, i)})
		a.syncEnvState()
		a.processTurn(planStepRounds, false)
		res := a.LastTurn()
		if a.aborted.Load() {
			a.io.Println(i18n.T("plan.stopped"))
			return
		}
		if res.Outcome != OutcomeRespond && res.Outcome != OutcomeDone {
			// The step's conversation is kept: the user can say how to go on.
			a.io.Println(i18n.T("plan.step_failed", i+1, len(steps), res.Outcome))
			return
		}
		done = append(done, fmt.Sprintf("[x] %d. %s\n    result: %s", i+1, step, oneLine(res.Answer, maxStepReport)))
		a.io.Println(i18n.T("plan.step_done", i+1, len(steps)))
	}
	a.io.Println(i18n.T("plan.finished", len(steps)))

	// Leave the log behind as context for follow-up questions.
	a.clearMessages()
	a.addMsg(Message{Role: "system", Content: a.systemPrompt()})
	a.addMsg(Message{Role: "user", Content: "For reference: the plan that was just executed and what each step reported.\n\n" + strings.Join(done, "\n")})
	a.addMsg(Message{Role: "assistant", Content: "Understood. The plan is complete."})
}

// planStepPrompt is the whole brief for one step.
func planStepPrompt(steps, done []string, i int) string {
	var b strings.Builder
	b.WriteString("You are executing an approved plan, one step at a time. Do ONLY the current step.\n\nTHE PLAN:\n")
	for j, s := range steps {
		fmt.Fprintf(&b, "%d. %s\n", j+1, s)
	}
	b.WriteString("\nSTEPS ALREADY DONE (do not redo them):\n")
	if len(done) == 0 {
		b.WriteString("(none yet)\n")
	}
	for _, d := range done {
		b.WriteString(d + "\n")
	}
	fmt.Fprintf(&b, "\nCURRENT STEP (%d of %d):\n%d. %s\n\n", i+1, len(steps), i+1, steps[i])
	b.WriteString("Use the tools for this step. Read a file before editing it. When the step is finished, call respond with one or two sentences: what you changed (file names) and what the following steps need to know.")
	return b.String()
}

// oneLine folds a model's report into a single line of at most max chars.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		s = truncateUTF8(s, max) + "..."
	}
	return s
}
