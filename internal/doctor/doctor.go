// Package doctor checks that the model server is reachable, serves the
// configured model and is fast enough for the agent loop.
package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/explorerTNT/TinyCode/internal/config"
	"github.com/explorerTNT/TinyCode/internal/i18n"
	"github.com/explorerTNT/TinyCode/internal/llm"
)

// Report is the outcome of the checkup.
type Report struct {
	// Lines is what to show the user, in order.
	Lines []string
	// Problems counts findings that keep the agent from working well.
	Problems int
}

func (r *Report) add(line string, problems int) {
	r.Lines = append(r.Lines, line)
	r.Problems += problems
}

// Run checks the configured model server. It never fails itself: whatever
// is wrong becomes a line of the report.
func Run(cfg *config.Config) Report {
	r := &Report{}
	base := fmt.Sprintf("http://%s:%d", cfg.LM.Host, cfg.LM.Port)
	client := llm.NewClient(base+"/v1", 0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	models, err := client.ListModels(ctx)
	cancel()
	if err != nil {
		r.add(i18n.T("doctor.connect_fail", base, err), 1)
		r.add(i18n.T("doctor.connect_hint"), 0)
		return *r
	}

	var names []string
	served := false
	for _, m := range models.Models {
		names = append(names, m.ID)
		if sameModel(m.ID, cfg.LM.Name) {
			served = true
		}
	}
	r.add(i18n.T("doctor.models", strings.Join(names, ", ")), 0)
	if !served {
		r.add(i18n.T("doctor.model_mismatch", cfg.LM.Name), 1)
	}

	// llama.cpp publishes its settings on /props; other servers may not.
	if props := serverProps(base + "/props"); props != nil {
		r.add(i18n.T("doctor.server", props.Context, props.Slots), 0)
		if want := cfg.TN.ContextLimit + cfg.TN.MaxTokens; props.Context > 0 && props.Context < want {
			r.add(i18n.T("doctor.ctx_small", props.Context, want), 1)
		}
		if !props.ToolsInTemplate {
			r.add(i18n.T("doctor.no_tools"), 1)
		}
	}

	promptSpeed, genSpeed, err := probe(base+"/v1/chat/completions", cfg.LM.Name)
	if err != nil {
		r.add(i18n.T("doctor.probe_fail", err), 1)
		return *r
	}
	if promptSpeed > 0 {
		r.add(i18n.T("doctor.speed", promptSpeed, genSpeed), 0)
	} else {
		r.add(i18n.T("doctor.probe_time", genSpeed), 0)
	}
	if genSpeed > 0 && genSpeed < 5 {
		r.add(i18n.T("doctor.slow"), 1)
	}
	if r.Problems == 0 {
		r.add(i18n.T("doctor.ok"), 0)
	}
	return *r
}

// sameModel accepts the configured name with or without an "owner/" prefix.
func sameModel(served, configured string) bool {
	s, c := strings.ToLower(served), strings.ToLower(configured)
	return s == c || strings.HasSuffix(s, "/"+c) || strings.HasSuffix(c, "/"+s)
}

type serverSettings struct {
	Context         int
	Slots           int
	ToolsInTemplate bool
}

func serverProps(url string) *serverSettings {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var props struct {
		TotalSlots               int    `json:"total_slots"`
		ChatTemplate             string `json:"chat_template"`
		ChatTemplateTool         string `json:"chat_template_tool"`
		DefaultGenerationSetting struct {
			NContext int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&props); err != nil || props.TotalSlots == 0 {
		return nil
	}
	return &serverSettings{
		Context:         props.DefaultGenerationSetting.NContext,
		Slots:           props.TotalSlots,
		ToolsInTemplate: strings.Contains(props.ChatTemplate, "tools") || props.ChatTemplateTool != "",
	}
}

const probePrompt = "Reply with the numbers 1 to 30 separated by single spaces and nothing else."

// probe sends one short request and returns the prompt-processing and
// generation speeds in tokens per second, as far as the server reports
// them. Speeds the server does not report come out as 0.
func probe(url, model string) (promptSpeed, genSpeed float64, err error) {
	body, _ := json.Marshal(map[string]any{
		"model":       model,
		"messages":    []map[string]string{{"role": "user", "content": probePrompt}},
		"max_tokens":  64,
		"temperature": 0,
	})
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	var out struct {
		Timings struct {
			PromptPerSecond    float64 `json:"prompt_per_second"`
			PredictedPerSecond float64 `json:"predicted_per_second"`
		} `json:"timings"`
		Usage struct {
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, 0, err
	}
	if out.Error.Message != "" {
		return 0, 0, fmt.Errorf("%s", out.Error.Message)
	}
	if out.Timings.PredictedPerSecond > 0 {
		return out.Timings.PromptPerSecond, out.Timings.PredictedPerSecond, nil
	}
	if n := out.Usage.CompletionTokens; n > 0 {
		return 0, float64(n) / time.Since(start).Seconds(), nil
	}
	return 0, 0, nil
}
