package deployer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aneeshkp/llm-d-conformance-test/framework/config"
)

func TestSanitizeK8sName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "lowercase conversion",
			input:    "MyModel",
			expected: "mymodel",
		},
		{
			name:     "replace slashes with dashes",
			input:    "org/model-name",
			expected: "org-model-name",
		},
		{
			name:     "replace underscores with dashes",
			input:    "my_model_name",
			expected: "my-model-name",
		},
		{
			name:     "replace spaces with dashes",
			input:    "my model name",
			expected: "my-model-name",
		},
		{
			name:     "replace dots with dashes",
			input:    "qwen2.5-7b",
			expected: "qwen2.5-7b", // dots are NOT replaced per the implementation
		},
		{
			name:     "truncate to 63 characters",
			input:    strings.Repeat("a", 70),
			expected: strings.Repeat("a", 63),
		},
		{
			name:     "trim leading and trailing dashes",
			input:    "/leading-and-trailing/",
			expected: "leading-and-trailing",
		},
		{
			name:     "truncate then trim trailing dash",
			input:    strings.Repeat("a", 62) + "-x",
			expected: strings.Repeat("a", 62), // 64 chars truncated to 63 leaves trailing dash, which gets trimmed
		},
		{
			name:     "multiple replacements combined",
			input:    "Org/My_Model Name",
			expected: "org-my-model-name",
		},
		{
			name:     "trailing slash becomes dash then trimmed",
			input:    "model/",
			expected: "model",
		},
		{
			name:     "all dashes trimmed to empty",
			input:    "///",
			expected: "",
		},
		{
			name:     "truncate then trim trailing dashes",
			input:    strings.Repeat("a", 62) + "//",
			expected: strings.Repeat("a", 62),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeK8sName(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeK8sName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestExtractLLMISVCName(t *testing.T) {
	// Helper to create a temp manifest file with given content.
	createManifest := func(t *testing.T, content string) string {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "manifest.yaml")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("failed to write temp manifest: %v", err)
		}
		return path
	}

	tests := []struct {
		name     string
		tc       *config.TestCase
		expected string
	}{
		{
			name: "extracts metadata.name from manifest",
			tc: func() *config.TestCase {
				path := createManifest(t, `apiVersion: serving.kserve.io/v1alpha1
kind: LLMInferenceService
metadata:
  name: qwen2-7b-gpu
spec:
  model:
    uri: hf://Qwen/Qwen2-7B
`)
				return &config.TestCase{
					Name: "test-case-name",
					Model: config.ModelConfig{
						DisplayName: "display-name",
					},
					Deployment: config.DeployConfig{
						ManifestPath: path,
					},
				}
			}(),
			expected: "qwen2-7b-gpu",
		},
		{
			name: "extracts quoted metadata.name",
			tc: func() *config.TestCase {
				path := createManifest(t, `apiVersion: v1
kind: LLMInferenceService
metadata:
  name: "my-quoted-name"
spec:
  model:
    uri: hf://org/model
`)
				return &config.TestCase{
					Name: "fallback",
					Deployment: config.DeployConfig{
						ManifestPath: path,
					},
				}
			}(),
			expected: "my-quoted-name",
		},
		{
			name: "falls back to displayName when no manifest path",
			tc: &config.TestCase{
				Name: "test-case-name",
				Model: config.ModelConfig{
					DisplayName: "My Display/Name",
				},
				Deployment: config.DeployConfig{
					ManifestPath: "",
				},
			},
			expected: "my-display-name",
		},
		{
			name: "falls back to displayName when manifest file missing",
			tc: &config.TestCase{
				Name: "test-case-name",
				Model: config.ModelConfig{
					DisplayName: "Fallback_Display",
				},
				Deployment: config.DeployConfig{
					ManifestPath: "/nonexistent/path/manifest.yaml",
				},
			},
			expected: "fallback-display",
		},
		{
			name: "falls back to test case name when no displayName",
			tc: &config.TestCase{
				Name: "My_Test_Case",
				Model: config.ModelConfig{
					DisplayName: "",
				},
				Deployment: config.DeployConfig{
					ManifestPath: "",
				},
			},
			expected: "my-test-case",
		},
		{
			name: "falls back to test case name when manifest has no metadata",
			tc: func() *config.TestCase {
				path := createManifest(t, `apiVersion: v1
kind: LLMInferenceService
spec:
  model:
    uri: hf://org/model
`)
				return &config.TestCase{
					Name: "no-metadata-case",
					Deployment: config.DeployConfig{
						ManifestPath: path,
					},
				}
			}(),
			expected: "no-metadata-case",
		},
		{
			name: "falls back when metadata exists but name is empty",
			tc: func() *config.TestCase {
				path := createManifest(t, `apiVersion: v1
kind: LLMInferenceService
metadata:
  labels:
    app: test
`)
				return &config.TestCase{
					Name: "empty-name-case",
					Deployment: config.DeployConfig{
						ManifestPath: path,
					},
				}
			}(),
			expected: "empty-name-case",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractLLMISVCName(tt.tc)
			if got != tt.expected {
				t.Errorf("ExtractLLMISVCName() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestPatchManifest(t *testing.T) {
	// Helper to create a temp manifest file.
	createManifest := func(t *testing.T, content string) string {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "manifest.yaml")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("failed to write temp manifest: %v", err)
		}
		return path
	}

	tests := []struct {
		name            string
		manifest        string
		tc              *config.TestCase
		expectPatched   bool   // if true, expect a new temp file (different path)
		expectURI       string // expected uri value in output
		expectModelName string // expected name value after uri in output
	}{
		{
			name: "patches uri and name fields",
			manifest: `apiVersion: serving.kserve.io/v1alpha1
kind: LLMInferenceService
metadata:
  name: test-svc
spec:
  model:
    uri: hf://original/model
    name: original-model
`,
			tc: &config.TestCase{
				Name: "test",
				Model: config.ModelConfig{
					URI:  "pvc://my-pvc/models/new-model",
					Name: "new-model",
				},
			},
			expectPatched:   true,
			expectURI:       "pvc://my-pvc/models/new-model",
			expectModelName: "new-model",
		},
		{
			name: "patches pvc uri to hf uri",
			manifest: `apiVersion: v1
kind: LLMInferenceService
metadata:
  name: my-svc
spec:
  model:
    uri: pvc://cache/models/qwen
    name: qwen-model
`,
			tc: &config.TestCase{
				Name: "test",
				Model: config.ModelConfig{
					URI:  "hf://Qwen/Qwen2-7B",
					Name: "Qwen2-7B",
				},
			},
			expectPatched:   true,
			expectURI:       "hf://Qwen/Qwen2-7B",
			expectModelName: "Qwen2-7B",
		},
		{
			name: "preserves indentation",
			manifest: `spec:
  model:
      uri: hf://org/model
      name: old-name
`,
			tc: &config.TestCase{
				Name: "test",
				Model: config.ModelConfig{
					URI:  "pvc://new-pvc/model",
					Name: "new-name",
				},
			},
			expectPatched:   true,
			expectURI:       "pvc://new-pvc/model",
			expectModelName: "new-name",
		},
		{
			name: "no patch when no uri field present",
			manifest: `apiVersion: v1
kind: LLMInferenceService
metadata:
  name: test-svc
spec:
  replicas: 1
`,
			tc: &config.TestCase{
				Name: "test",
				Model: config.ModelConfig{
					URI:  "hf://org/model",
					Name: "model",
				},
			},
			expectPatched: false,
		},
		{
			name: "no patch when uri does not contain hf:// or pvc://",
			manifest: `spec:
  model:
    uri: s3://bucket/model
    name: s3-model
`,
			tc: &config.TestCase{
				Name: "test",
				Model: config.ModelConfig{
					URI:  "hf://org/model",
					Name: "model",
				},
			},
			// The uri line is not patched (no hf:// or pvc://), but the name line
			// IS patched because it follows a line starting with "uri:".
			expectPatched:   true,
			expectURI:       "s3://bucket/model", // uri unchanged
			expectModelName: "model",              // name patched from tc
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifestPath := createManifest(t, tt.manifest)
			d := &Deployer{}

			resultPath, err := d.patchManifest(context.Background(), manifestPath, tt.tc)
			if err != nil {
				t.Fatalf("patchManifest() returned error: %v", err)
			}

			if tt.expectPatched {
				// Should return a different (temp) file path
				if resultPath == manifestPath {
					t.Fatalf("expected patched file (different path), got original path")
				}
				defer func() { _ = os.Remove(resultPath) }()

				data, err := os.ReadFile(resultPath)
				if err != nil {
					t.Fatalf("failed to read patched file: %v", err)
				}
				content := string(data)

				// Verify the URI was patched
				if !strings.Contains(content, "uri: "+tt.expectURI) {
					t.Errorf("patched manifest does not contain expected uri.\nGot:\n%s", content)
				}
				// Verify the name was patched
				if !strings.Contains(content, "name: "+tt.expectModelName) {
					t.Errorf("patched manifest does not contain expected name.\nGot:\n%s", content)
				}
			} else if resultPath != manifestPath {
				t.Errorf("expected original path %q, got %q", manifestPath, resultPath)
				_ = os.Remove(resultPath)
			}
		})
	}
}

func TestPatchManifestReadError(t *testing.T) {
	d := &Deployer{}
	_, err := d.patchManifest(context.Background(), "/nonexistent/path/manifest.yaml", &config.TestCase{
		Model: config.ModelConfig{URI: "hf://org/model", Name: "model"},
	})
	if err == nil {
		t.Fatal("expected error for nonexistent manifest, got nil")
	}
	if !strings.Contains(err.Error(), "reading manifest") {
		t.Errorf("expected 'reading manifest' in error, got: %v", err)
	}
}
