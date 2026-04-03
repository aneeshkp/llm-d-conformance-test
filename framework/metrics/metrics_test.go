package metrics

import (
	"testing"
)

// ---------------------------------------------------------------------------
// ParsePrometheusText
// ---------------------------------------------------------------------------

func TestParsePrometheusText(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCount int
		check     func(t *testing.T, ms []Metric) // optional deeper assertions
	}{
		{
			name:      "empty input",
			input:     "",
			wantCount: 0,
		},
		{
			name:      "only comments and blank lines",
			input:     "# HELP vllm:prompt_tokens_total Total prompt tokens\n# TYPE vllm:prompt_tokens_total counter\n\n",
			wantCount: 0,
		},
		{
			name:      "single metric without labels",
			input:     "vllm:gpu_cache_usage_perc 0.42\n",
			wantCount: 1,
			check: func(t *testing.T, ms []Metric) {
				if ms[0].Name != "vllm:gpu_cache_usage_perc" {
					t.Errorf("name = %q, want vllm:gpu_cache_usage_perc", ms[0].Name)
				}
				if ms[0].Value != 0.42 {
					t.Errorf("value = %f, want 0.42", ms[0].Value)
				}
				if len(ms[0].Labels) != 0 {
					t.Errorf("expected no labels, got %v", ms[0].Labels)
				}
			},
		},
		{
			name:      "single metric with labels",
			input:     `vllm:prefix_cache_hits{model_name="Qwen/Qwen2.5-7B-Instruct"} 42.0`,
			wantCount: 1,
			check: func(t *testing.T, ms []Metric) {
				if ms[0].Name != "vllm:prefix_cache_hits" {
					t.Errorf("name = %q", ms[0].Name)
				}
				if ms[0].Labels["model_name"] != "Qwen/Qwen2.5-7B-Instruct" {
					t.Errorf("model_name label = %q", ms[0].Labels["model_name"])
				}
				if ms[0].Value != 42.0 {
					t.Errorf("value = %f, want 42", ms[0].Value)
				}
			},
		},
		{
			name: "mixed comments, blanks, and metrics",
			input: `# HELP vllm:prompt_tokens_total Total number of prompt tokens
# TYPE vllm:prompt_tokens_total counter
vllm:prompt_tokens_total 1234

# HELP vllm:generation_tokens_total Total gen tokens
vllm:generation_tokens_total 5678
vllm:request_success_total{finished_reason="stop"} 10
`,
			wantCount: 3,
			check: func(t *testing.T, ms []Metric) {
				if ms[0].Value != 1234 {
					t.Errorf("prompt_tokens value = %f", ms[0].Value)
				}
				if ms[1].Value != 5678 {
					t.Errorf("gen_tokens value = %f", ms[1].Value)
				}
				if ms[2].Labels["finished_reason"] != "stop" {
					t.Errorf("finished_reason = %q", ms[2].Labels["finished_reason"])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParsePrometheusText(tt.input)
			if len(got) != tt.wantCount {
				t.Fatalf("ParsePrometheusText returned %d metrics, want %d", len(got), tt.wantCount)
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseLine
// ---------------------------------------------------------------------------

func TestParseLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantOK  bool
		wantM   Metric
	}{
		{
			name:   "metric without labels",
			line:   "vllm:gpu_cache_usage_perc 0.75",
			wantOK: true,
			wantM:  Metric{Name: "vllm:gpu_cache_usage_perc", Value: 0.75, Labels: map[string]string{}},
		},
		{
			name:   "metric with single label",
			line:   `vllm:request_success_total{finished_reason="stop"} 5`,
			wantOK: true,
			wantM: Metric{
				Name:   "vllm:request_success_total",
				Value:  5,
				Labels: map[string]string{"finished_reason": "stop"},
			},
		},
		{
			name:   "metric with multiple labels",
			line:   `http_requests_total{method="GET",code="200"} 99`,
			wantOK: true,
			wantM: Metric{
				Name:   "http_requests_total",
				Value:  99,
				Labels: map[string]string{"method": "GET", "code": "200"},
			},
		},
		{
			name:   "malformed - no value",
			line:   "vllm:gpu_cache_usage_perc",
			wantOK: false,
		},
		{
			name:   "malformed - unclosed brace",
			line:   `vllm:metric{label="value" 123`,
			wantOK: false,
		},
		{
			name:   "malformed - non-numeric value",
			line:   "vllm:metric abc",
			wantOK: false,
		},
		{
			name:   "scientific notation value",
			line:   "vllm:metric 1.5e2",
			wantOK: true,
			wantM:  Metric{Name: "vllm:metric", Value: 150, Labels: map[string]string{}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseLine(tt.line)
			if ok != tt.wantOK {
				t.Fatalf("parseLine ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if got.Name != tt.wantM.Name {
				t.Errorf("name = %q, want %q", got.Name, tt.wantM.Name)
			}
			if got.Value != tt.wantM.Value {
				t.Errorf("value = %f, want %f", got.Value, tt.wantM.Value)
			}
			for k, v := range tt.wantM.Labels {
				if got.Labels[k] != v {
					t.Errorf("label %q = %q, want %q", k, got.Labels[k], v)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseLabels
// ---------------------------------------------------------------------------

func TestParseLabels(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want map[string]string
	}{
		{
			name: "single label",
			s:    `model_name="Qwen/Qwen2.5-7B-Instruct"`,
			want: map[string]string{"model_name": "Qwen/Qwen2.5-7B-Instruct"},
		},
		{
			name: "multiple labels",
			s:    `method="GET",code="200"`,
			want: map[string]string{"method": "GET", "code": "200"},
		},
		{
			name: "empty string",
			s:    "",
			want: map[string]string{},
		},
		{
			name: "label value with comma inside quotes",
			s:    `name="hello, world",code="200"`,
			want: map[string]string{"name": "hello, world", "code": "200"},
		},
		{
			name: "label with no equals sign is ignored",
			s:    `badlabel`,
			want: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLabels(tt.s)
			if len(got) != len(tt.want) {
				t.Fatalf("parseLabels returned %d labels, want %d; got=%v", len(got), len(tt.want), got)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("label %q = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// splitLabels
// ---------------------------------------------------------------------------

func TestSplitLabels(t *testing.T) {
	tests := []struct {
		name  string
		s     string
		want  []string
	}{
		{
			name: "simple split",
			s:    `method="GET",code="200"`,
			want: []string{`method="GET"`, `code="200"`},
		},
		{
			name: "comma inside quotes is not a delimiter",
			s:    `name="a,b",code="200"`,
			want: []string{`name="a,b"`, `code="200"`},
		},
		{
			name: "single label, no comma",
			s:    `model_name="foo"`,
			want: []string{`model_name="foo"`},
		},
		{
			name: "empty string",
			s:    "",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitLabels(tt.s)
			if len(got) != len(tt.want) {
				t.Fatalf("splitLabels returned %v, want %v", got, tt.want)
			}
			for i, v := range tt.want {
				if got[i] != v {
					t.Errorf("part[%d] = %q, want %q", i, got[i], v)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// newScrapeResult / GetValue / GetAllValues / GetWithLabel / getValueWithFallback
// ---------------------------------------------------------------------------

func buildTestScrapeResult() *ScrapeResult {
	return newScrapeResult("test-pod", []Metric{
		{Name: "metric_a", Value: 10, Labels: map[string]string{}},
		{Name: "metric_b", Value: 20, Labels: map[string]string{"env": "prod"}},
		{Name: "metric_b", Value: 30, Labels: map[string]string{"env": "staging"}},
		{Name: "metric_c_total", Value: 100, Labels: map[string]string{}},
	})
}

func TestNewScrapeResult(t *testing.T) {
	r := buildTestScrapeResult()
	if r.Source != "test-pod" {
		t.Errorf("Source = %q, want test-pod", r.Source)
	}
	if len(r.index) != 3 {
		t.Errorf("index has %d keys, want 3", len(r.index))
	}
	if len(r.index["metric_b"]) != 2 {
		t.Errorf("metric_b has %d entries, want 2", len(r.index["metric_b"]))
	}
}

func TestGetValue(t *testing.T) {
	r := buildTestScrapeResult()

	v, ok := r.GetValue("metric_a")
	if !ok || v != 10 {
		t.Errorf("GetValue(metric_a) = (%f, %v), want (10, true)", v, ok)
	}

	// Returns first match for duplicates
	v, ok = r.GetValue("metric_b")
	if !ok || v != 20 {
		t.Errorf("GetValue(metric_b) = (%f, %v), want (20, true)", v, ok)
	}

	// Missing metric
	v, ok = r.GetValue("nonexistent")
	if ok || v != 0 {
		t.Errorf("GetValue(nonexistent) = (%f, %v), want (0, false)", v, ok)
	}
}

func TestGetAllValues(t *testing.T) {
	r := buildTestScrapeResult()

	vals := r.GetAllValues("metric_b")
	if len(vals) != 2 {
		t.Fatalf("GetAllValues returned %d, want 2", len(vals))
	}
	if vals[0] != 20 || vals[1] != 30 {
		t.Errorf("values = %v, want [20 30]", vals)
	}

	vals = r.GetAllValues("nonexistent")
	if len(vals) != 0 {
		t.Errorf("GetAllValues(nonexistent) returned %v, want empty", vals)
	}
}

func TestGetWithLabel(t *testing.T) {
	r := buildTestScrapeResult()

	v, ok := r.GetWithLabel("metric_b", "env", "staging")
	if !ok || v != 30 {
		t.Errorf("GetWithLabel staging = (%f, %v), want (30, true)", v, ok)
	}

	v, ok = r.GetWithLabel("metric_b", "env", "dev")
	if ok {
		t.Errorf("GetWithLabel dev should not match, got (%f, %v)", v, ok)
	}

	_, ok = r.GetWithLabel("nonexistent", "env", "prod")
	if ok {
		t.Errorf("GetWithLabel on missing metric should return false")
	}
}

func TestGetValueWithFallback(t *testing.T) {
	r := buildTestScrapeResult()

	// Primary exists
	v, ok := r.getValueWithFallback("metric_a", "metric_c_total")
	if !ok || v != 10 {
		t.Errorf("fallback primary hit = (%f, %v), want (10, true)", v, ok)
	}

	// Primary missing, fallback exists
	v, ok = r.getValueWithFallback("metric_c", "metric_c_total")
	if !ok || v != 100 {
		t.Errorf("fallback secondary hit = (%f, %v), want (100, true)", v, ok)
	}

	// Both missing
	v, ok = r.getValueWithFallback("missing1", "missing2")
	if ok {
		t.Errorf("both missing should return false, got (%f, %v)", v, ok)
	}
}

// ---------------------------------------------------------------------------
// ValidateCacheAwareMetrics
// ---------------------------------------------------------------------------

func TestValidateCacheAwareMetrics(t *testing.T) {
	t.Run("hits greater than zero", func(t *testing.T) {
		vllm := []*ScrapeResult{
			newScrapeResult("pod-1", []Metric{
				{Name: MetricPrefixCacheQueriesAlt, Value: 100, Labels: map[string]string{}},
				{Name: MetricPrefixCacheHitsAlt, Value: 50, Labels: map[string]string{}},
				{Name: MetricGPUCacheUsage, Value: 0.35, Labels: map[string]string{}},
			}),
		}
		epp := []*ScrapeResult{
			newScrapeResult("epp-1", []Metric{
				{Name: MetricPrefixIndexerSize, Value: 5, Labels: map[string]string{}},
			}),
		}

		checks := ValidateCacheAwareMetrics(vllm, epp)

		// Expect: queries check, hits check, hit rate check, gpu cache check, EPP prefix indexer
		if len(checks) != 5 {
			t.Fatalf("got %d checks, want 5", len(checks))
		}
		for _, c := range checks {
			if !c.Passed {
				t.Errorf("check %q should pass, msg=%s", c.Name, c.Message)
			}
		}
	})

	t.Run("hits equal zero", func(t *testing.T) {
		vllm := []*ScrapeResult{
			newScrapeResult("pod-1", []Metric{
				{Name: MetricPrefixCacheQueriesAlt, Value: 100, Labels: map[string]string{}},
				{Name: MetricPrefixCacheHitsAlt, Value: 0, Labels: map[string]string{}},
			}),
		}

		checks := ValidateCacheAwareMetrics(vllm, nil)

		// queries pass, hits fail, hit rate fail
		var hitCheck *CheckResult
		for i := range checks {
			if checks[i].Metric == MetricPrefixCacheHits {
				hitCheck = &checks[i]
			}
		}
		if hitCheck == nil {
			t.Fatal("expected a prefix cache hits check")
		}
		if hitCheck.Passed {
			t.Error("prefix cache hits check should fail when hits=0")
		}
	})

	t.Run("no vllm results", func(t *testing.T) {
		checks := ValidateCacheAwareMetrics(nil, nil)
		if len(checks) != 0 {
			t.Errorf("expected 0 checks with no results, got %d", len(checks))
		}
	})
}

// ---------------------------------------------------------------------------
// ValidatePDMetrics
// ---------------------------------------------------------------------------

func TestValidatePDMetrics(t *testing.T) {
	t.Run("healthy P/D with NIXL", func(t *testing.T) {
		vllm := []*ScrapeResult{
			newScrapeResult("decode-pod", []Metric{
				{Name: MetricPromptTokens, Value: 500, Labels: map[string]string{}},
				{Name: MetricGenTokens, Value: 300, Labels: map[string]string{}},
				{Name: MetricRequestSuccess, Value: 8, Labels: map[string]string{"finished_reason": "stop"}},
				{Name: MetricRequestSuccess, Value: 2, Labels: map[string]string{"finished_reason": "length"}},
				{Name: MetricRequestSuccess, Value: 1, Labels: map[string]string{"finished_reason": "abort"}},
				{Name: MetricNIXLTransfers, Value: 5, Labels: map[string]string{}},
				{Name: MetricNIXLFailures, Value: 0, Labels: map[string]string{}},
				{Name: MetricPreemptions, Value: 0, Labels: map[string]string{}},
			}),
		}

		checks := ValidatePDMetrics(vllm)

		// prompt_tokens, gen_tokens, request_success, NIXL transfers, NIXL failures, preemptions = 6
		if len(checks) != 6 {
			t.Fatalf("got %d checks, want 6", len(checks))
		}
		for _, c := range checks {
			if !c.Passed {
				t.Errorf("check %q should pass, msg=%s", c.Name, c.Message)
			}
		}

		// Verify that abort requests are excluded: totalRequests should be 10 (8+2), not 11
		var reqCheck CheckResult
		for _, c := range checks {
			if c.Metric == MetricRequestSuccess {
				reqCheck = c
			}
		}
		if reqCheck.Value != 10 {
			t.Errorf("request_success value = %f, want 10 (abort excluded)", reqCheck.Value)
		}
	})

	t.Run("no NIXL metrics", func(t *testing.T) {
		vllm := []*ScrapeResult{
			newScrapeResult("pod-1", []Metric{
				{Name: MetricPromptTokens, Value: 100, Labels: map[string]string{}},
				{Name: MetricGenTokens, Value: 50, Labels: map[string]string{}},
				{Name: MetricRequestSuccess, Value: 3, Labels: map[string]string{"finished_reason": "stop"}},
			}),
		}

		checks := ValidatePDMetrics(vllm)
		for _, c := range checks {
			if c.Metric == MetricNIXLTransfers || c.Metric == MetricNIXLFailures {
				t.Errorf("NIXL check should not appear when metrics are absent")
			}
		}
	})

	t.Run("excessive preemptions fail", func(t *testing.T) {
		vllm := []*ScrapeResult{
			newScrapeResult("pod-1", []Metric{
				{Name: MetricPromptTokens, Value: 100, Labels: map[string]string{}},
				{Name: MetricGenTokens, Value: 50, Labels: map[string]string{}},
				{Name: MetricRequestSuccess, Value: 3, Labels: map[string]string{"finished_reason": "stop"}},
				{Name: MetricPreemptions, Value: 15, Labels: map[string]string{}},
			}),
		}

		checks := ValidatePDMetrics(vllm)
		var preemptCheck *CheckResult
		for i := range checks {
			if checks[i].Metric == MetricPreemptions {
				preemptCheck = &checks[i]
			}
		}
		if preemptCheck == nil {
			t.Fatal("expected preemptions check")
		}
		if preemptCheck.Passed {
			t.Error("preemptions check should fail when value >= 10")
		}
	})

	t.Run("empty results", func(t *testing.T) {
		checks := ValidatePDMetrics(nil)
		// Should still produce 3 checks (prompt, gen, request) but all fail
		if len(checks) != 3 {
			t.Fatalf("got %d checks, want 3", len(checks))
		}
		for _, c := range checks {
			if c.Passed {
				t.Errorf("check %q should fail with no data", c.Name)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// ValidateSchedulerMetrics
// ---------------------------------------------------------------------------

func TestValidateSchedulerMetrics(t *testing.T) {
	t.Run("all metrics present and healthy", func(t *testing.T) {
		epp := []*ScrapeResult{
			newScrapeResult("epp-pod", []Metric{
				{Name: MetricSchedulerE2E, Value: 42, Labels: map[string]string{}},
				{Name: MetricRequestTotal, Value: 100, Labels: map[string]string{}},
				{Name: MetricRequestErrorTotal, Value: 0, Labels: map[string]string{}},
				{Name: MetricPoolReadyPods, Value: 3, Labels: map[string]string{}},
			}),
		}

		checks := ValidateSchedulerMetrics(epp)
		if len(checks) != 4 {
			t.Fatalf("got %d checks, want 4", len(checks))
		}
		for _, c := range checks {
			if !c.Passed {
				t.Errorf("check %q should pass, msg=%s", c.Name, c.Message)
			}
		}
	})

	t.Run("routing errors present", func(t *testing.T) {
		epp := []*ScrapeResult{
			newScrapeResult("epp-pod", []Metric{
				{Name: MetricSchedulerE2E, Value: 10, Labels: map[string]string{}},
				{Name: MetricRequestErrorTotal, Value: 5, Labels: map[string]string{}},
			}),
		}

		checks := ValidateSchedulerMetrics(epp)
		var errCheck *CheckResult
		for i := range checks {
			if checks[i].Metric == MetricRequestErrorTotal {
				errCheck = &checks[i]
			}
		}
		if errCheck == nil {
			t.Fatal("expected error check")
		}
		if errCheck.Passed {
			t.Error("error check should fail when errors > 0")
		}
	})

	t.Run("no EPP results", func(t *testing.T) {
		checks := ValidateSchedulerMetrics(nil)
		if len(checks) != 0 {
			t.Errorf("expected 0 checks, got %d", len(checks))
		}
	})

	t.Run("metrics absent from pod", func(t *testing.T) {
		epp := []*ScrapeResult{
			newScrapeResult("epp-pod", []Metric{}),
		}

		checks := ValidateSchedulerMetrics(epp)
		if len(checks) != 0 {
			t.Errorf("expected 0 checks when metrics absent, got %d", len(checks))
		}
	})
}
