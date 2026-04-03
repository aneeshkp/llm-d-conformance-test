package config

import (
	"os"
	"path/filepath"
	"testing"
)

// helper to write a file in a directory
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
	return path
}

const testCaseYAML = `name: test-model
description: A test model
labels:
  - gpu
  - smoke
model:
  name: test-org/test-model
  uri: "hf://test-org/test-model"
  displayName: Test Model
  category: single-node-gpu
deployment:
  manifestPath: deploy/manifests/test.yaml
  namespace: default
  replicas: 1
  readyTimeout: 10m
  resources:
    cpu: "4"
    memory: 16Gi
    gpus: 1
validation:
  healthEndpoint: /health
  inferenceCheck: true
  testPrompts:
    - "Hello"
  expectedCodes: [200]
  timeout: 5m
  retryAttempts: 3
  retryInterval: 10s
cleanup: true
`

const testCaseYAML2 = `name: test-cpu-model
description: A CPU test model
labels:
  - cpu
  - smoke
model:
  name: test-org/cpu-model
  uri: "hf://test-org/cpu-model"
  displayName: CPU Model
  category: cpu
deployment:
  manifestPath: deploy/manifests/cpu.yaml
  namespace: default
  replicas: 1
  readyTimeout: 5m
  resources:
    cpu: "2"
    memory: 8Gi
    gpus: 0
validation:
  healthEndpoint: /health
  inferenceCheck: false
  expectedCodes: [200]
  timeout: 2m
  retryAttempts: 2
  retryInterval: 5s
cleanup: true
`

const testCaseYAML3 = `name: test-cache-model
description: A cache-aware model
labels:
  - gpu
  - cache-aware
model:
  name: test-org/cache-model
  uri: "hf://test-org/cache-model"
  displayName: Cache Model
  category: cache-aware
deployment:
  manifestPath: deploy/manifests/cache.yaml
  namespace: default
  replicas: 2
  readyTimeout: 15m
  resources:
    cpu: "8"
    memory: 32Gi
    gpus: 2
validation:
  healthEndpoint: /health
  inferenceCheck: true
  chatPrompts:
    - system: You are a helpful assistant.
      user: What is 2+2?
  expectedCodes: [200]
  timeout: 5m
  retryAttempts: 3
  retryInterval: 10s
  metricsCheck:
    enabled: true
    checkVLLM: true
    checkEPP: true
    checkPrefixCache: true
cleanup: false
`

const profileYAML = `name: smoke
description: Smoke test profile
platform: any
labels:
  - smoke
parallel: false
timeout: 30m
`

const profileWithTestCasesYAML = `name: targeted
description: Targeted profile
platform: ocp
testCases:
  - test-model
  - test-cpu-model
parallel: true
timeout: 1h
`

// --- LoadTestCase tests ---

func TestLoadTestCase(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantName  string
		wantErr   bool
		wantGPUs  int
		wantLabel string
	}{
		{
			name:      "valid test case",
			content:   testCaseYAML,
			wantName:  "test-model",
			wantGPUs:  1,
			wantLabel: "gpu",
		},
		{
			name:      "valid CPU test case",
			content:   testCaseYAML2,
			wantName:  "test-cpu-model",
			wantGPUs:  0,
			wantLabel: "cpu",
		},
		{
			name:    "invalid YAML",
			content: "{{invalid yaml: [",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeFile(t, dir, "tc.yaml", tt.content)

			tc, err := LoadTestCase(path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", tc.Name, tt.wantName)
			}
			if tc.Deployment.Resources.GPUs != tt.wantGPUs {
				t.Errorf("GPUs = %d, want %d", tc.Deployment.Resources.GPUs, tt.wantGPUs)
			}
			if len(tc.Labels) == 0 || tc.Labels[0] != tt.wantLabel {
				t.Errorf("Labels[0] = %v, want %q", tc.Labels, tt.wantLabel)
			}
		})
	}
}

func TestLoadTestCase_FileNotFound(t *testing.T) {
	_, err := LoadTestCase("/nonexistent/path/tc.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadTestCase_ChatPrompts(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "cache.yaml", testCaseYAML3)

	tc, err := LoadTestCase(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tc.Validation.ChatPrompts) != 1 {
		t.Fatalf("ChatPrompts length = %d, want 1", len(tc.Validation.ChatPrompts))
	}
	if tc.Validation.ChatPrompts[0].User != "What is 2+2?" {
		t.Errorf("ChatPrompts[0].User = %q, want %q", tc.Validation.ChatPrompts[0].User, "What is 2+2?")
	}
	if tc.Validation.MetricsCheck == nil || !tc.Validation.MetricsCheck.CheckPrefixCache {
		t.Error("expected MetricsCheck.CheckPrefixCache to be true")
	}
}

// --- LoadTestCasesFromDir tests ---

func TestLoadTestCasesFromDir(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string // filename -> content
		wantCount int
		wantErr   bool
	}{
		{
			name: "multiple YAML files",
			files: map[string]string{
				"tc1.yaml": testCaseYAML,
				"tc2.yml":  testCaseYAML2,
			},
			wantCount: 2,
		},
		{
			name: "skips non-YAML files",
			files: map[string]string{
				"tc1.yaml":   testCaseYAML,
				"readme.txt": "not a test case",
				"data.json":  `{"key":"value"}`,
			},
			wantCount: 1,
		},
		{
			name:      "empty directory",
			files:     map[string]string{},
			wantCount: 0,
		},
		{
			name: "invalid YAML in directory",
			files: map[string]string{
				"bad.yaml": "{{invalid",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				writeFile(t, dir, name, content)
			}

			cases, err := LoadTestCasesFromDir(dir)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(cases) != tt.wantCount {
				t.Errorf("got %d test cases, want %d", len(cases), tt.wantCount)
			}
		})
	}
}

func TestLoadTestCasesFromDir_SkipsSubdirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tc1.yaml", testCaseYAML)

	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, subdir, "tc2.yaml", testCaseYAML2)

	cases, err := LoadTestCasesFromDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cases) != 1 {
		t.Errorf("got %d test cases, want 1 (should skip subdirectory)", len(cases))
	}
}

func TestLoadTestCasesFromDir_NonexistentDir(t *testing.T) {
	_, err := LoadTestCasesFromDir("/nonexistent/directory")
	if err == nil {
		t.Fatal("expected error for missing directory, got nil")
	}
}

// --- LoadProfile tests ---

func TestLoadProfile(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantName    string
		wantErr     bool
		checkLabels bool
		wantLabels  []string
	}{
		{
			name:        "valid profile with labels",
			content:     profileYAML,
			wantName:    "smoke",
			checkLabels: true,
			wantLabels:  []string{"smoke"},
		},
		{
			name:     "valid profile with test cases",
			content:  profileWithTestCasesYAML,
			wantName: "targeted",
		},
		{
			name:    "invalid YAML",
			content: ":::bad",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeFile(t, dir, "profile.yaml", tt.content)

			profile, err := LoadProfile(path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if profile.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", profile.Name, tt.wantName)
			}
			if tt.checkLabels {
				if len(profile.Labels) != len(tt.wantLabels) {
					t.Errorf("Labels = %v, want %v", profile.Labels, tt.wantLabels)
				}
			}
		})
	}
}

func TestLoadProfile_FileNotFound(t *testing.T) {
	_, err := LoadProfile("/nonexistent/profile.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

// --- ResolveProfileTestCases tests ---

func TestResolveProfileTestCases(t *testing.T) {
	// Set up a directory with three test cases
	dir := t.TempDir()
	writeFile(t, dir, "tc1.yaml", testCaseYAML)
	writeFile(t, dir, "tc2.yaml", testCaseYAML2)
	writeFile(t, dir, "tc3.yaml", testCaseYAML3)

	tests := []struct {
		name      string
		profile   *TestProfile
		wantCount int
		wantNames []string
	}{
		{
			name: "filter by test case names",
			profile: &TestProfile{
				TestCases: []string{"test-model", "test-cpu-model"},
			},
			wantCount: 2,
			wantNames: []string{"test-model", "test-cpu-model"},
		},
		{
			name: "filter by labels - smoke",
			profile: &TestProfile{
				Labels: []string{"smoke"},
			},
			wantCount: 2, // test-model and test-cpu-model both have "smoke"
		},
		{
			name: "filter by labels - cache-aware",
			profile: &TestProfile{
				Labels: []string{"cache-aware"},
			},
			wantCount: 1,
		},
		{
			name:      "no filters returns all",
			profile:   &TestProfile{},
			wantCount: 3,
		},
		{
			name: "testCases takes precedence over labels",
			profile: &TestProfile{
				TestCases: []string{"test-model"},
				Labels:    []string{"smoke"},
			},
			wantCount: 1,
			wantNames: []string{"test-model"},
		},
		{
			name: "no matching test case names",
			profile: &TestProfile{
				TestCases: []string{"nonexistent"},
			},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cases, err := ResolveProfileTestCases(tt.profile, dir)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(cases) != tt.wantCount {
				names := make([]string, len(cases))
				for i, c := range cases {
					names[i] = c.Name
				}
				t.Errorf("got %d cases %v, want %d", len(cases), names, tt.wantCount)
			}
			if tt.wantNames != nil {
				gotNames := make(map[string]bool, len(cases))
				for _, c := range cases {
					gotNames[c.Name] = true
				}
				for _, wn := range tt.wantNames {
					if !gotNames[wn] {
						t.Errorf("expected case %q not found in results", wn)
					}
				}
			}
		})
	}
}

func TestResolveProfileTestCases_InvalidDir(t *testing.T) {
	profile := &TestProfile{}
	_, err := ResolveProfileTestCases(profile, "/nonexistent/dir")
	if err == nil {
		t.Fatal("expected error for missing directory, got nil")
	}
}

// --- FilterTestCasesByNames tests ---

func TestFilterTestCasesByNames(t *testing.T) {
	allCases := []*TestCase{
		{Name: "alpha"},
		{Name: "beta"},
		{Name: "gamma"},
		{Name: "delta"},
	}

	tests := []struct {
		name      string
		names     []string
		wantCount int
		wantNames []string
	}{
		{
			name:      "single name match",
			names:     []string{"alpha"},
			wantCount: 1,
			wantNames: []string{"alpha"},
		},
		{
			name:      "multiple names",
			names:     []string{"alpha", "gamma", "delta"},
			wantCount: 3,
			wantNames: []string{"alpha", "gamma", "delta"},
		},
		{
			name:      "no match",
			names:     []string{"nonexistent"},
			wantCount: 0,
		},
		{
			name:      "empty names list",
			names:     []string{},
			wantCount: 0,
		},
		{
			name:      "partial match",
			names:     []string{"alpha", "nonexistent"},
			wantCount: 1,
			wantNames: []string{"alpha"},
		},
		{
			name:      "all names",
			names:     []string{"alpha", "beta", "gamma", "delta"},
			wantCount: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := FilterTestCasesByNames(allCases, tt.names)
			if len(filtered) != tt.wantCount {
				t.Errorf("got %d results, want %d", len(filtered), tt.wantCount)
			}
			if tt.wantNames != nil {
				for i, wn := range tt.wantNames {
					if i >= len(filtered) {
						break
					}
					if filtered[i].Name != wn {
						t.Errorf("filtered[%d].Name = %q, want %q", i, filtered[i].Name, wn)
					}
				}
			}
		})
	}
}

// --- FilterTestCasesByLabels tests ---

func TestFilterTestCasesByLabels(t *testing.T) {
	allCases := []*TestCase{
		{Name: "case1", Labels: []string{"gpu", "smoke"}},
		{Name: "case2", Labels: []string{"cpu", "smoke"}},
		{Name: "case3", Labels: []string{"gpu", "cache-aware"}},
		{Name: "case4", Labels: []string{"cpu"}},
		{Name: "case5", Labels: []string{}},
	}

	tests := []struct {
		name      string
		labels    []string
		wantCount int
		wantNames []string
	}{
		{
			name:      "single label - gpu",
			labels:    []string{"gpu"},
			wantCount: 2,
			wantNames: []string{"case1", "case3"},
		},
		{
			name:      "single label - smoke",
			labels:    []string{"smoke"},
			wantCount: 2,
			wantNames: []string{"case1", "case2"},
		},
		{
			name:      "multiple labels match (OR logic)",
			labels:    []string{"gpu", "cpu"},
			wantCount: 4,
			wantNames: []string{"case1", "case2", "case3", "case4"},
		},
		{
			name:      "no match",
			labels:    []string{"nonexistent"},
			wantCount: 0,
		},
		{
			name:      "empty labels list",
			labels:    []string{},
			wantCount: 0,
		},
		{
			name:      "cache-aware label",
			labels:    []string{"cache-aware"},
			wantCount: 1,
			wantNames: []string{"case3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := FilterTestCasesByLabels(allCases, tt.labels)
			if len(filtered) != tt.wantCount {
				names := make([]string, len(filtered))
				for i, c := range filtered {
					names[i] = c.Name
				}
				t.Errorf("got %d results %v, want %d", len(filtered), names, tt.wantCount)
			}
			if tt.wantNames != nil {
				for i, wn := range tt.wantNames {
					if i >= len(filtered) {
						break
					}
					if filtered[i].Name != wn {
						t.Errorf("filtered[%d].Name = %q, want %q", i, filtered[i].Name, wn)
					}
				}
			}
		})
	}
}

// --- isYAMLFile tests ---

func TestIsYAMLFile(t *testing.T) {
	tests := []struct {
		name string
		file string
		want bool
	}{
		{"yaml extension", "test.yaml", true},
		{"yml extension", "test.yml", true},
		{"YAML uppercase", "test.YAML", true},
		{"YML uppercase", "test.YML", true},
		{"mixed case", "test.Yaml", true},
		{"json extension", "test.json", false},
		{"no extension", "testfile", false},
		{"txt extension", "test.txt", false},
		{"empty string", "", false},
		{"dot yaml in name", "my.yaml.bak", false},
		{"double extension", "test.tar.yaml", true},
		{"hidden file yaml", ".hidden.yaml", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isYAMLFile(tt.file)
			if got != tt.want {
				t.Errorf("isYAMLFile(%q) = %v, want %v", tt.file, got, tt.want)
			}
		})
	}
}
