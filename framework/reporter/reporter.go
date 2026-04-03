// Package reporter provides JSON and HTML test report generation.
package reporter

import (
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"time"
)

// Report is the top-level test report structure.
type Report struct {
	Suite       string          `json:"suite"`
	Profile     string          `json:"profile"`
	Platform    string          `json:"platform"`
	StartTime   time.Time       `json:"startTime"`
	EndTime     time.Time       `json:"endTime"`
	Duration    string          `json:"duration"`
	Environment EnvironmentInfo `json:"environment"`
	Results     []TestResult    `json:"results"`
	Summary     Summary         `json:"summary"`
}

// EnvironmentInfo captures cluster and runtime details.
type EnvironmentInfo struct {
	Platform          string            `json:"platform"`
	KubernetesVersion string            `json:"kubernetesVersion"`
	Namespace         string            `json:"namespace"`
	Extra             map[string]string `json:"extra,omitempty"`
}

// TestResult represents a single test case outcome.
type TestResult struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Category    string    `json:"category"`
	Status      Status    `json:"status"`
	StartTime   time.Time `json:"startTime"`
	EndTime     time.Time `json:"endTime"`
	Duration    string    `json:"duration"`
	Error       string    `json:"error,omitempty"`
	Logs        []string  `json:"logs,omitempty"`
	Model       ModelInfo `json:"model"`
}

// ModelInfo captures model-specific details for the report.
type ModelInfo struct {
	Name        string `json:"name"`
	URI         string `json:"uri"`
	Category    string `json:"category"`
	VLLMImage   string `json:"vllmImage,omitempty"`
	VLLMVersion string `json:"vllmVersion,omitempty"`
}

// Summary provides aggregate pass/fail/skip counts.
type Summary struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// Status is the test result status.
type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	StatusSkip Status = "skip"
)

// Reporter accumulates test results and writes JSON + HTML reports.
type Reporter struct {
	report    Report
	outputDir string
}

// New creates a Reporter that will write to the given output directory.
func New(outputDir, suite, profile, platform string) *Reporter {
	return &Reporter{
		outputDir: outputDir,
		report: Report{
			Suite:     suite,
			Profile:   profile,
			Platform:  platform,
			StartTime: time.Now(),
		},
	}
}

// SetEnvironment sets the environment info on the report.
func (r *Reporter) SetEnvironment(env EnvironmentInfo) {
	r.report.Environment = env
}

// UpdateExtra adds or updates a key in the environment extra map.
func (r *Reporter) UpdateExtra(key, value string) {
	if r.report.Environment.Extra == nil {
		r.report.Environment.Extra = make(map[string]string)
	}
	if _, exists := r.report.Environment.Extra[key]; !exists {
		r.report.Environment.Extra[key] = value
	}
}

// AddResult appends a test result.
func (r *Reporter) AddResult(result TestResult) {
	r.report.Results = append(r.report.Results, result)
}

// Finalize computes the summary and writes both JSON and HTML reports.
func (r *Reporter) Finalize() (string, error) {
	r.report.EndTime = time.Now()
	r.report.Duration = r.report.EndTime.Sub(r.report.StartTime).String()

	// Compute summary
	for i := range r.report.Results {
		r.report.Summary.Total++
		switch r.report.Results[i].Status {
		case StatusPass:
			r.report.Summary.Passed++
		case StatusFail:
			r.report.Summary.Failed++
		case StatusSkip:
			r.report.Summary.Skipped++
		}
	}

	if err := os.MkdirAll(r.outputDir, 0o755); err != nil { //nolint:gosec // G301: report directory needs read access
		return "", fmt.Errorf("creating report directory: %w", err)
	}

	baseName := fmt.Sprintf("report-%s-%s",
		r.report.Profile,
		r.report.StartTime.Format("20060102-150405"))

	// Write JSON
	jsonPath := filepath.Join(r.outputDir, baseName+".json")
	data, err := json.MarshalIndent(r.report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling report: %w", err)
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil { //nolint:gosec // G306: report file needs read access
		return "", fmt.Errorf("writing JSON report: %w", err)
	}

	// Write HTML
	htmlPath := filepath.Join(r.outputDir, baseName+".html")
	if err := r.writeHTML(htmlPath); err != nil {
		return jsonPath, fmt.Errorf("writing HTML report: %w", err)
	}

	return htmlPath, nil
}

// GetReport returns the current report (useful for in-test assertions).
func (r *Reporter) GetReport() Report {
	return r.report
}

func (r *Reporter) writeHTML(path string) error {
	tmpl, err := template.New("report").Funcs(template.FuncMap{
		"statusClass": func(s Status) string {
			switch s {
			case StatusPass:
				return "pass"
			case StatusFail:
				return "fail"
			default:
				return "skip"
			}
		},
		"statusIcon": func(s Status) string {
			switch s {
			case StatusPass:
				return "PASS"
			case StatusFail:
				return "FAIL"
			default:
				return "SKIP"
			}
		},
	}).Parse(htmlTemplate)
	if err != nil {
		return fmt.Errorf("parsing HTML template: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating HTML file: %w", err)
	}
	defer func() { _ = f.Close() }()

	return tmpl.Execute(f, r.report)
}

const htmlTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>LLM-D Conformance Report — {{.Profile}}</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: #0d1117; color: #c9d1d9; padding: 24px; }
  h1 { color: #f0f6fc; margin-bottom: 8px; }
  .subtitle { color: #8b949e; margin-bottom: 24px; }
  .summary { display: flex; gap: 16px; margin-bottom: 32px; }
  .summary-card { padding: 16px 24px; border-radius: 8px; background: #161b22; border: 1px solid #30363d; min-width: 120px; }
  .summary-card .number { font-size: 32px; font-weight: bold; }
  .summary-card .label { color: #8b949e; font-size: 14px; }
  .summary-card.total .number { color: #c9d1d9; }
  .summary-card.passed .number { color: #3fb950; }
  .summary-card.failed .number { color: #f85149; }
  .summary-card.skipped .number { color: #d29922; }
  .env { background: #161b22; border: 1px solid #30363d; border-radius: 8px; padding: 16px; margin-bottom: 32px; }
  .env table { width: 100%; }
  .env td { padding: 4px 12px; }
  .env td:first-child { color: #8b949e; width: 180px; }
  .results { margin-bottom: 32px; }
  .result { background: #161b22; border: 1px solid #30363d; border-radius: 8px; margin-bottom: 12px; overflow: hidden; }
  .result-header { display: flex; align-items: center; padding: 12px 16px; cursor: pointer; }
  .result-header:hover { background: #1c2128; }
  .badge { padding: 2px 10px; border-radius: 12px; font-size: 12px; font-weight: 600; margin-right: 12px; }
  .badge.pass { background: #238636; color: #fff; }
  .badge.fail { background: #da3633; color: #fff; }
  .badge.skip { background: #9e6a03; color: #fff; }
  .result-name { font-weight: 600; color: #f0f6fc; flex: 1; }
  .result-meta { color: #8b949e; font-size: 13px; }
  .result-details { display: none; padding: 0 16px 16px; border-top: 1px solid #30363d; }
  .result.open .result-details { display: block; }
  .error-box { background: #3d1f1f; border: 1px solid #f85149; border-radius: 6px; padding: 12px; margin: 12px 0; font-family: monospace; font-size: 13px; color: #ffa198; white-space: pre-wrap; word-break: break-all; }
  .logs { background: #0d1117; border: 1px solid #30363d; border-radius: 6px; padding: 12px; margin: 12px 0; font-family: monospace; font-size: 12px; max-height: 300px; overflow-y: auto; }
  .logs div { padding: 1px 0; }
  .detail-table td { padding: 4px 12px; vertical-align: top; }
  .detail-table td:first-child { color: #8b949e; white-space: nowrap; }
  footer { color: #484f58; font-size: 13px; text-align: center; padding: 24px; }
</style>
</head>
<body>

<h1>LLM-D Conformance Report</h1>
<p class="subtitle">{{.Suite}} | Profile: {{.Profile}} | Platform: {{.Platform}} | {{.StartTime.Format "2006-01-02 15:04:05"}} | Duration: {{.Duration}}</p>

<div class="summary">
  <div class="summary-card total"><div class="number">{{.Summary.Total}}</div><div class="label">Total</div></div>
  <div class="summary-card passed"><div class="number">{{.Summary.Passed}}</div><div class="label">Passed</div></div>
  <div class="summary-card failed"><div class="number">{{.Summary.Failed}}</div><div class="label">Failed</div></div>
  <div class="summary-card skipped"><div class="number">{{.Summary.Skipped}}</div><div class="label">Skipped</div></div>
</div>

<div class="env">
  <table>
    <tr><td>Platform</td><td>{{.Environment.Platform}}</td></tr>
    <tr><td>Kubernetes</td><td>{{.Environment.KubernetesVersion}}</td></tr>
    <tr><td>Namespace</td><td>{{.Environment.Namespace}}</td></tr>
    {{range $k, $v := .Environment.Extra}}<tr><td>{{$k}}</td><td>{{$v}}</td></tr>{{end}}
  </table>
</div>

<div class="results">
{{range .Results}}
  <div class="result" onclick="this.classList.toggle('open')">
    <div class="result-header">
      <span class="badge {{statusClass .Status}}">{{statusIcon .Status}}</span>
      <span class="result-name">{{.Name}}</span>
      <span class="result-meta">{{.Model.Name}} | {{.Category}} | {{.Duration}}</span>
    </div>
    <div class="result-details">
      <table class="detail-table">
        {{if .Description}}<tr><td>Description</td><td>{{.Description}}</td></tr>{{end}}
        <tr><td>Model</td><td>{{.Model.Name}}</td></tr>
        <tr><td>URI</td><td>{{.Model.URI}}</td></tr>
        <tr><td>Category</td><td>{{.Model.Category}}</td></tr>
        {{if .Model.VLLMVersion}}<tr><td>vLLM Version</td><td>{{.Model.VLLMVersion}}</td></tr>{{end}}
        {{if .Model.VLLMImage}}<tr><td>vLLM Image</td><td style="font-size:12px;word-break:break-all">{{.Model.VLLMImage}}</td></tr>{{end}}
        <tr><td>Duration</td><td>{{.Duration}}</td></tr>
      </table>
      {{if .Error}}<div class="error-box">{{.Error}}</div>{{end}}
      {{if .Logs}}
      <div class="logs">
        {{range .Logs}}<div>{{.}}</div>{{end}}
      </div>
      {{end}}
    </div>
  </div>
{{end}}
</div>

<footer>Generated by llm-d-conformance-test</footer>

</body>
</html>`
