// Package config provides configuration types and loaders for the LLM-D conformance test framework.
package config

import "time"

// TestProfile defines a named collection of test cases to run.
type TestProfile struct {
	Name        string       `yaml:"name"`
	Description string       `yaml:"description"`
	Platform    string       `yaml:"platform"` // "ocp", "aks", "gks", "any"
	Labels      []string     `yaml:"labels"`
	TestCases   []string     `yaml:"testCases"` // references to TestCase names
	Parallel    bool         `yaml:"parallel"`
	Timeout     Duration     `yaml:"timeout"`
	Environment *Environment `yaml:"environment,omitempty"`
}

// Environment captures platform-specific settings.
type Environment struct {
	Kubeconfig    string `yaml:"kubeconfig,omitempty"`
	Namespace     string `yaml:"namespace"`
	Platform      string `yaml:"platform"`      // "ocp", "aks", "gks"
	PullSecret    string `yaml:"pullSecret,omitempty"`
	StorageClass  string `yaml:"storageClass,omitempty"`
	IngressDomain string `yaml:"ingressDomain,omitempty"`
}

// TestCase defines a single test scenario driven by config.
type TestCase struct {
	Name        string          `yaml:"name"`
	Description string          `yaml:"description"`
	Labels      []string        `yaml:"labels"`
	Model       ModelConfig     `yaml:"model"`
	Deployment  DeployConfig    `yaml:"deployment"`
	Validation  ValidateConfig  `yaml:"validation"`
	Cleanup     bool            `yaml:"cleanup"`
}

// ModelConfig describes the LLM model under test.
type ModelConfig struct {
	Name        string       `yaml:"name"`        // e.g. "deepseek-ai/DeepSeek-R1-0528"
	URI         string       `yaml:"uri"`         // e.g. "hf://deepseek-ai/DeepSeek-R1-0528"
	DisplayName string       `yaml:"displayName"` // human-friendly name
	Category    string       `yaml:"category"`    // "deepseek", "cache-aware", "moe", "cpu", "single-node-gpu"
	Cache       *CacheConfig `yaml:"cache,omitempty"`
}

// CacheConfig controls model download and PVC caching behavior.
type CacheConfig struct {
	Enabled      bool     `yaml:"enabled"`               // if true, download model to PVC before deploying
	PVCName      string   `yaml:"pvcName,omitempty"`     // explicit PVC name (auto-generated if empty)
	StorageSize  string   `yaml:"storageSize,omitempty"` // e.g. "100Gi" (auto-sized if empty)
	StorageClass string   `yaml:"storageClass,omitempty"`
	Timeout      Duration `yaml:"timeout,omitempty"`     // download timeout (default 90m)
	KeepPVC      bool     `yaml:"keepPVC"`               // if true, don't delete PVC on cleanup (reuse across runs)
}

// DeployConfig captures deployment parameters for the LLMInferenceService.
type DeployConfig struct {
	ManifestPath    string            `yaml:"manifestPath"`    // path to YAML manifest
	Namespace       string            `yaml:"namespace"`       // target namespace
	Replicas        int               `yaml:"replicas"`
	ServiceAccount  string            `yaml:"serviceAccount"`
	ReadyTimeout    Duration          `yaml:"readyTimeout"`
	Resources       ResourceConfig    `yaml:"resources"`
	Parallelism     *ParallelismConfig `yaml:"parallelism,omitempty"`
	Prefill         *PrefillConfig    `yaml:"prefill,omitempty"`
	Worker          bool              `yaml:"worker"`
	NetworkAttach   string            `yaml:"networkAttachment,omitempty"`
	EnvOverrides    map[string]string `yaml:"envOverrides,omitempty"`
}

// ResourceConfig specifies compute resource requirements.
type ResourceConfig struct {
	CPU              string `yaml:"cpu"`
	Memory           string `yaml:"memory"`
	GPUs             int    `yaml:"gpus"`
	EphemeralStorage string `yaml:"ephemeralStorage,omitempty"`
	RDMA             bool   `yaml:"rdma"`
}

// ParallelismConfig for data/expert/tensor parallelism.
type ParallelismConfig struct {
	Data      int  `yaml:"data"`
	DataLocal int  `yaml:"dataLocal"`
	Expert    bool `yaml:"expert"`
	Tensor    int  `yaml:"tensor"`
}

// PrefillConfig for prefill/decode disaggregation.
type PrefillConfig struct {
	Replicas    int            `yaml:"replicas"`
	Parallelism *ParallelismConfig `yaml:"parallelism,omitempty"`
	Resources   ResourceConfig `yaml:"resources"`
}

// ValidateConfig defines what to validate after deployment.
type ValidateConfig struct {
	HealthEndpoint string   `yaml:"healthEndpoint"` // default "/health"
	HealthPort     int      `yaml:"healthPort"`     // default 8000
	HealthScheme   string   `yaml:"healthScheme"`   // "HTTP" or "HTTPS"
	InferenceCheck bool     `yaml:"inferenceCheck"` // whether to send a test prompt
	TestPrompts    []string `yaml:"testPrompts,omitempty"`
	ExpectedCodes  []int    `yaml:"expectedCodes"`
	Timeout        Duration `yaml:"timeout"`
	RetryAttempts  int      `yaml:"retryAttempts"`
	RetryInterval  Duration `yaml:"retryInterval"`
}

// Duration wraps time.Duration for YAML unmarshalling.
type Duration struct {
	time.Duration
}

// UnmarshalYAML parses a duration string like "5m" or "300s".
func (d *Duration) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = dur
	return nil
}

// MarshalYAML outputs the duration as a string.
func (d Duration) MarshalYAML() (interface{}, error) {
	return d.Duration.String(), nil
}
