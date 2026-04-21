package deployer

import (
	"context"
	"fmt"
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

// mockKubectl creates a kubectlFunc that responds to specific "get secret" calls.
// secrets maps "name/namespace" to YAML content. If a key is absent, the call returns an error.
func mockKubectl(secrets map[string]string) func(ctx context.Context, args ...string) (string, error) {
	return func(ctx context.Context, args ...string) (string, error) {
		// Parse: get secret <name> -n <ns> [-o yaml]
		if len(args) >= 5 && args[0] == "get" && args[1] == "secret" && args[3] == "-n" {
			name := args[2]
			ns := args[4]
			key := name + "/" + ns
			yaml, ok := secrets[key]
			if !ok {
				return "", fmt.Errorf("secret %q not found in namespace %q", name, ns)
			}
			// If -o yaml requested, return YAML; otherwise just confirm existence
			if len(args) >= 7 && args[5] == "-o" && args[6] == "yaml" {
				return yaml, nil
			}
			return "secret/" + name, nil
		}
		// Handle apply: apply -n <ns> -f <filepath>
		if len(args) >= 5 && args[0] == "apply" && args[1] == "-n" && args[3] == "-f" {
			return "applied", nil
		}
		return "", fmt.Errorf("unexpected kubectl args: %v", args)
	}
}

func sampleSecretYAML(name, namespace string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  resourceVersion: "12345"
  uid: abc-123
  creationTimestamp: "2024-01-01T00:00:00Z"
  labels:
    app: test
    name: %s
data:
  .dockerconfigjson: eyJhdXRocyI6e319
type: kubernetes.io/dockerconfigjson
`, name, namespace, name)
}

func TestFindSecret(t *testing.T) {
	secrets := map[string]string{
		"my-secret/kserve": sampleSecretYAML("my-secret", "kserve"),
	}

	d := &Deployer{kubectlFunc: mockKubectl(secrets)}
	ctx := context.Background()

	tests := []struct {
		name       string
		names      []string
		namespaces []string
		wantName   string
		wantNS     string
		wantErr    bool
	}{
		{
			name:       "finds exact match",
			names:      []string{"my-secret"},
			namespaces: []string{"istio-system", "kserve"},
			wantName:   "my-secret",
			wantNS:     "kserve",
		},
		{
			name:       "finds alias when exact not found",
			names:      []string{"not-exist", "my-secret"},
			namespaces: []string{"kserve"},
			wantName:   "my-secret",
			wantNS:     "kserve",
		},
		{
			name:       "returns error when not found",
			names:      []string{"missing-secret"},
			namespaces: []string{"istio-system", "kserve"},
			wantErr:    true,
		},
		{
			name:       "empty names list",
			names:      []string{},
			namespaces: []string{"kserve"},
			wantErr:    true,
		},
		{
			name:       "empty namespaces list",
			names:      []string{"my-secret"},
			namespaces: []string{},
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, ns, err := d.findSecret(ctx, tt.names, tt.namespaces)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if ns != tt.wantNS {
				t.Errorf("namespace = %q, want %q", ns, tt.wantNS)
			}
		})
	}
}

func TestCopySecret(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		srcName  string
		srcNS    string
		destName string
		destNS   string
		// Assertions on the YAML written to the temp file (captured via apply mock)
		wantInApplied    []string
		wantNotInApplied []string
	}{
		{
			name:     "copies with same name",
			srcName:  "my-secret",
			srcNS:    "kserve",
			destName: "my-secret",
			destNS:   "target-ns",
			wantInApplied: []string{
				"name: my-secret",
				"namespace: target-ns",
				"app: test",
			},
			wantNotInApplied: []string{
				"resourceVersion:",
				"uid:",
				"creationTimestamp:",
				"namespace: kserve",
			},
		},
		{
			name:     "copies with renamed secret",
			srcName:  "rhai-pull-secret",
			srcNS:    "istio-system",
			destName: "rhaii-pull-secret",
			destNS:   "my-ns",
			wantInApplied: []string{
				"namespace: my-ns",
			},
			wantNotInApplied: []string{
				"resourceVersion:",
				"uid:",
				"namespace: istio-system",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var appliedYAML string
			secrets := map[string]string{
				tt.srcName + "/" + tt.srcNS: sampleSecretYAML(tt.srcName, tt.srcNS),
			}
			d := &Deployer{
				kubectlFunc: func(ctx context.Context, args ...string) (string, error) {
					// Intercept apply to capture the YAML content
					// args: apply -n <ns> -f <filepath>
					if len(args) >= 5 && args[0] == "apply" && args[1] == "-n" && args[3] == "-f" {
						data, err := os.ReadFile(args[4])
						if err != nil {
							return "", err
						}
						appliedYAML = string(data)
						return "applied", nil
					}
					return mockKubectl(secrets)(ctx, args...)
				},
			}

			err := d.copySecret(ctx, tt.srcName, tt.srcNS, tt.destName, tt.destNS)
			if err != nil {
				t.Fatalf("copySecret() error: %v", err)
			}

			for _, want := range tt.wantInApplied {
				if !strings.Contains(appliedYAML, want) {
					t.Errorf("applied YAML missing %q.\nGot:\n%s", want, appliedYAML)
				}
			}
			for _, notWant := range tt.wantNotInApplied {
				if strings.Contains(appliedYAML, notWant) {
					t.Errorf("applied YAML should not contain %q.\nGot:\n%s", notWant, appliedYAML)
				}
			}
		})
	}
}

func TestCopySecretRenameOnlyMetadataName(t *testing.T) {
	// Verify that renaming only affects metadata.name, not labels or data containing the same string
	ctx := context.Background()
	srcYAML := `apiVersion: v1
kind: Secret
metadata:
  name: rhai-pull-secret
  namespace: kserve
  labels:
    name: rhai-pull-secret
data:
  config: cmhhaS1wdWxsLXNlY3JldA==
`
	var appliedYAML string
	d := &Deployer{
		kubectlFunc: func(ctx context.Context, args ...string) (string, error) {
			if len(args) >= 7 && args[0] == "get" && args[5] == "-o" {
				return srcYAML, nil
			}
			if args[0] == "get" {
				return "ok", nil
			}
			if len(args) >= 5 && args[0] == "apply" && args[3] == "-f" {
				data, _ := os.ReadFile(args[4])
				appliedYAML = string(data)
				return "applied", nil
			}
			return "", fmt.Errorf("unexpected: %v", args)
		},
	}

	err := d.copySecret(ctx, "rhai-pull-secret", "kserve", "rhaii-pull-secret", "target")
	if err != nil {
		t.Fatalf("copySecret() error: %v", err)
	}

	// metadata.name should be renamed
	lines := strings.Split(appliedYAML, "\n")
	foundMetadataName := false
	foundLabelName := false
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		// The metadata name line (indented under metadata:)
		if trimmed == "name: rhaii-pull-secret" && i > 0 && strings.TrimSpace(lines[i-1]) == "metadata:" {
			foundMetadataName = true
		}
		// The label line should NOT be renamed
		if trimmed == "name: rhai-pull-secret" {
			foundLabelName = true
		}
	}
	if !foundMetadataName {
		t.Errorf("metadata.name was not renamed to rhaii-pull-secret.\nGot:\n%s", appliedYAML)
	}
	if !foundLabelName {
		t.Errorf("label 'name: rhai-pull-secret' was incorrectly renamed.\nGot:\n%s", appliedYAML)
	}
}

func TestCopySecretGetError(t *testing.T) {
	d := &Deployer{
		kubectlFunc: func(ctx context.Context, args ...string) (string, error) {
			return "", fmt.Errorf("kubectl failed")
		},
	}

	err := d.copySecret(context.Background(), "secret", "ns1", "secret", "ns2")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestEnsurePullSecrets(t *testing.T) {
	createManifest := func(t *testing.T, content string) string {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "manifest.yaml")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
		return path
	}

	ctx := context.Background()

	t.Run("skips on OCP platform", func(t *testing.T) {
		d := &Deployer{Platform: PlatformOCP}
		err := d.ensurePullSecrets(ctx, "/any/path", "ns")
		if err != nil {
			t.Fatalf("expected nil error for OCP, got: %v", err)
		}
	})

	t.Run("copies secret from source namespace", func(t *testing.T) {
		manifest := createManifest(t, `apiVersion: v1
kind: LLMInferenceService
spec:
  imagePullSecrets:
    - name: my-pull-secret
`)
		secrets := map[string]string{
			"my-pull-secret/kserve": sampleSecretYAML("my-pull-secret", "kserve"),
		}
		d := &Deployer{
			Platform:    PlatformAKS,
			kubectlFunc: mockKubectl(secrets),
		}
		err := d.ensurePullSecrets(ctx, manifest, "target-ns")
		if err != nil {
			t.Fatalf("ensurePullSecrets() error: %v", err)
		}
	})

	t.Run("skips when secret already exists in target namespace", func(t *testing.T) {
		manifest := createManifest(t, `spec:
  imagePullSecrets:
    - name: existing-secret
`)
		secrets := map[string]string{
			"existing-secret/target-ns": sampleSecretYAML("existing-secret", "target-ns"),
		}
		d := &Deployer{
			Platform:    PlatformAKS,
			kubectlFunc: mockKubectl(secrets),
		}
		err := d.ensurePullSecrets(ctx, manifest, "target-ns")
		if err != nil {
			t.Fatalf("ensurePullSecrets() error: %v", err)
		}
	})

	t.Run("uses alias when exact name not found", func(t *testing.T) {
		manifest := createManifest(t, `spec:
  imagePullSecrets:
    - name: rhaii-pull-secret
`)
		// Only the alias "rhai-pull-secret" exists in kserve
		secrets := map[string]string{
			"rhai-pull-secret/kserve": sampleSecretYAML("rhai-pull-secret", "kserve"),
		}
		var logMsgs []string
		d := &Deployer{
			Platform:    PlatformAKS,
			LogFunc:     func(format string, args ...interface{}) { logMsgs = append(logMsgs, fmt.Sprintf(format, args...)) },
			kubectlFunc: mockKubectl(secrets),
		}
		err := d.ensurePullSecrets(ctx, manifest, "target-ns")
		if err != nil {
			t.Fatalf("ensurePullSecrets() error: %v", err)
		}
		// Should log that it copied from an alias
		found := false
		for _, msg := range logMsgs {
			if strings.Contains(msg, "from rhai-pull-secret") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected log message about alias copy, got: %v", logMsgs)
		}
	})

	t.Run("warns and continues when secret not found anywhere", func(t *testing.T) {
		manifest := createManifest(t, `spec:
  imagePullSecrets:
    - name: nonexistent-secret
`)
		var logMsgs []string
		d := &Deployer{
			Platform:    PlatformAKS,
			LogFunc:     func(format string, args ...interface{}) { logMsgs = append(logMsgs, fmt.Sprintf(format, args...)) },
			kubectlFunc: mockKubectl(map[string]string{}),
		}
		err := d.ensurePullSecrets(ctx, manifest, "target-ns")
		if err != nil {
			t.Fatalf("expected nil error (warning only), got: %v", err)
		}
		found := false
		for _, msg := range logMsgs {
			if strings.Contains(msg, "WARNING") && strings.Contains(msg, "nonexistent-secret") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected warning log about missing secret, got: %v", logMsgs)
		}
	})

	t.Run("uses explicit pull secret name override", func(t *testing.T) {
		manifest := createManifest(t, `spec:
  imagePullSecrets:
    - name: manifest-secret
`)
		secrets := map[string]string{
			"override-secret/istio-system": sampleSecretYAML("override-secret", "istio-system"),
		}
		d := &Deployer{
			Platform:       PlatformAKS,
			PullSecretName: "override-secret",
			kubectlFunc:    mockKubectl(secrets),
		}
		err := d.ensurePullSecrets(ctx, manifest, "target-ns")
		if err != nil {
			t.Fatalf("ensurePullSecrets() error: %v", err)
		}
	})

	t.Run("returns error for unreadable manifest", func(t *testing.T) {
		d := &Deployer{
			Platform:    PlatformAKS,
			kubectlFunc: mockKubectl(map[string]string{}),
		}
		err := d.ensurePullSecrets(ctx, "/nonexistent/manifest.yaml", "target-ns")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "reading manifest") {
			t.Errorf("expected 'reading manifest' in error, got: %v", err)
		}
	})

	t.Run("handles manifest with no imagePullSecrets", func(t *testing.T) {
		manifest := createManifest(t, `apiVersion: v1
kind: LLMInferenceService
spec:
  model:
    uri: hf://org/model
`)
		d := &Deployer{
			Platform:    PlatformAKS,
			kubectlFunc: mockKubectl(map[string]string{}),
		}
		err := d.ensurePullSecrets(ctx, manifest, "target-ns")
		if err != nil {
			t.Fatalf("ensurePullSecrets() error: %v", err)
		}
	})

	t.Run("handles multiple imagePullSecrets", func(t *testing.T) {
		manifest := createManifest(t, `spec:
  imagePullSecrets:
    - name: secret-one
    - name: secret-two
`)
		secrets := map[string]string{
			"secret-one/opendatahub": sampleSecretYAML("secret-one", "opendatahub"),
			"secret-two/default":     sampleSecretYAML("secret-two", "default"),
		}
		d := &Deployer{
			Platform:    PlatformAKS,
			kubectlFunc: mockKubectl(secrets),
		}
		err := d.ensurePullSecrets(ctx, manifest, "target-ns")
		if err != nil {
			t.Fatalf("ensurePullSecrets() error: %v", err)
		}
	})
}

func TestEnsurePullSecretsCopyFailure(t *testing.T) {
	createManifest := func(t *testing.T, content string) string {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "manifest.yaml")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
		return path
	}

	manifest := createManifest(t, `spec:
  imagePullSecrets:
    - name: my-secret
`)
	callCount := 0
	d := &Deployer{
		Platform: PlatformAKS,
		kubectlFunc: func(ctx context.Context, args ...string) (string, error) {
			callCount++
			// First call: check if exists in target ns -> no
			if callCount == 1 {
				return "", fmt.Errorf("not found")
			}
			// Second call: findSecret finds it
			if callCount == 2 {
				return "ok", nil
			}
			// Third call: copySecret get -o yaml -> fails
			if callCount == 3 {
				return "", fmt.Errorf("get yaml failed")
			}
			return "", fmt.Errorf("unexpected call %d: %v", callCount, args)
		},
	}

	err := d.ensurePullSecrets(context.Background(), manifest, "target-ns")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "copying pull secret") {
		t.Errorf("expected 'copying pull secret' in error, got: %v", err)
	}
}
