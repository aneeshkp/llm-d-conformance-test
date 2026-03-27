// Package reporter provides JSON test report generation.
package reporter

import (
	"encoding/json"
	"fmt"
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
	Name           string `json:"name"`
	URI            string `json:"uri"`
	Category       string `json:"category"`
	ContainerImage string `json:"containerImage,omitempty"`
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

// Reporter accumulates test results and writes a JSON report.
type Reporter struct {
	report    Report
	outputDir string
}

// New creates a Reporter that will write to the given output directory.
func New(outputDir, suite, profile, platform string) *Reporter {
	return &Reporter{
		outputDir: outputDir,
		report: Report{
			Suite:    suite,
			Profile:  profile,
			Platform: platform,
			StartTime: time.Now(),
		},
	}
}

// SetEnvironment sets the environment info on the report.
func (r *Reporter) SetEnvironment(env EnvironmentInfo) {
	r.report.Environment = env
}

// AddResult appends a test result.
func (r *Reporter) AddResult(result TestResult) {
	r.report.Results = append(r.report.Results, result)
}

// Finalize computes the summary and writes the JSON report to disk.
func (r *Reporter) Finalize() (string, error) {
	r.report.EndTime = time.Now()
	r.report.Duration = r.report.EndTime.Sub(r.report.StartTime).String()

	// Compute summary
	for _, res := range r.report.Results {
		r.report.Summary.Total++
		switch res.Status {
		case StatusPass:
			r.report.Summary.Passed++
		case StatusFail:
			r.report.Summary.Failed++
		case StatusSkip:
			r.report.Summary.Skipped++
		}
	}

	// Ensure output directory exists
	if err := os.MkdirAll(r.outputDir, 0o755); err != nil {
		return "", fmt.Errorf("creating report directory: %w", err)
	}

	filename := fmt.Sprintf("report-%s-%s.json",
		r.report.Profile,
		r.report.StartTime.Format("20060102-150405"))
	path := filepath.Join(r.outputDir, filename)

	data, err := json.MarshalIndent(r.report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshalling report: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("writing report: %w", err)
	}

	return path, nil
}

// GetReport returns the current report (useful for in-test assertions).
func (r *Reporter) GetReport() Report {
	return r.report
}
