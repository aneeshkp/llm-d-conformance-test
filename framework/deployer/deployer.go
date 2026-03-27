// Package deployer provides Kubernetes deployment helpers for LLMInferenceService resources.
package deployer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aneeshkp/llm-d-conformance-test/framework/config"
	"github.com/aneeshkp/llm-d-conformance-test/framework/retry"
)

// Platform identifies the target Kubernetes platform.
type Platform string

const (
	PlatformOCP Platform = "ocp"
	PlatformAKS Platform = "aks"
	PlatformGKS Platform = "gks"
	PlatformAny Platform = "any"
)

// Deployer manages deployment of LLMInferenceService resources on Kubernetes.
type Deployer struct {
	Kubeconfig string
	Platform   Platform
	Namespace  string
}

// New creates a Deployer for the given platform and namespace.
func New(kubeconfig string, platform Platform, namespace string) *Deployer {
	return &Deployer{
		Kubeconfig: kubeconfig,
		Platform:   platform,
		Namespace:  namespace,
	}
}

// DeployResult captures the outcome of a deployment attempt.
type DeployResult struct {
	Name      string
	Namespace string
	Success   bool
	Error     error
	Duration  time.Duration
	Logs      []string
}

// Deploy applies a test case's manifest to the cluster.
func (d *Deployer) Deploy(ctx context.Context, tc *config.TestCase) *DeployResult {
	start := time.Now()
	result := &DeployResult{
		Name:      tc.Name,
		Namespace: d.resolveNamespace(tc),
	}

	ns := result.Namespace

	// Ensure namespace exists
	if err := d.ensureNamespace(ctx, ns); err != nil {
		result.Error = fmt.Errorf("creating namespace %s: %w", ns, err)
		result.Duration = time.Since(start)
		return result
	}
	result.Logs = append(result.Logs, fmt.Sprintf("Namespace %s ready", ns))

	// Apply the manifest
	manifestPath := tc.Deployment.ManifestPath
	if manifestPath == "" {
		result.Error = fmt.Errorf("no manifest path specified for test case %s", tc.Name)
		result.Duration = time.Since(start)
		return result
	}

	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		result.Error = fmt.Errorf("manifest not found: %s", manifestPath)
		result.Duration = time.Since(start)
		return result
	}

	output, err := d.kubectl(ctx, "apply", "-n", ns, "-f", manifestPath)
	if err != nil {
		result.Error = fmt.Errorf("applying manifest %s: %w\nOutput: %s", manifestPath, err, output)
		result.Duration = time.Since(start)
		return result
	}
	result.Logs = append(result.Logs, fmt.Sprintf("Applied manifest: %s", manifestPath))
	result.Logs = append(result.Logs, output)

	result.Success = true
	result.Duration = time.Since(start)
	return result
}

// WaitForReady waits for the LLMInferenceService to become ready.
func (d *Deployer) WaitForReady(ctx context.Context, tc *config.TestCase) error {
	ns := d.resolveNamespace(tc)
	timeout := tc.Deployment.ReadyTimeout.Duration
	if timeout == 0 {
		timeout = 10 * time.Minute
	}

	name := extractLLMISVCName(tc)

	return retry.UntilSuccess(ctx, retry.Options{
		Timeout:  timeout,
		Interval: 15 * time.Second,
		Name:     fmt.Sprintf("wait-ready-%s", name),
	}, func() error {
		output, err := d.kubectl(ctx, "get", "llmisvc", name, "-n", ns, "-o", "jsonpath={.status.ready}")
		if err != nil {
			return fmt.Errorf("checking llmisvc ready status: %w (output: %s)", err, output)
		}
		if strings.TrimSpace(output) != "True" && strings.TrimSpace(output) != "true" {
			// Also check pod readiness as a fallback
			podOutput, _ := d.kubectl(ctx, "get", "pods", "-n", ns, "-l",
				fmt.Sprintf("app.kubernetes.io/instance=%s", name),
				"-o", "jsonpath={.items[*].status.phase}")
			return fmt.Errorf("llmisvc %s not ready yet (status: %q, pods: %s)", name, output, podOutput)
		}
		return nil
	})
}

// Cleanup deletes all resources for a test case.
func (d *Deployer) Cleanup(ctx context.Context, tc *config.TestCase) error {
	ns := d.resolveNamespace(tc)
	manifestPath := tc.Deployment.ManifestPath

	if manifestPath == "" {
		return fmt.Errorf("no manifest path for cleanup of %s", tc.Name)
	}

	output, err := d.kubectl(ctx, "delete", "-n", ns, "-f", manifestPath, "--ignore-not-found=true", "--timeout=120s")
	if err != nil {
		return fmt.Errorf("cleanup failed for %s: %w\nOutput: %s", tc.Name, err, output)
	}

	// Wait for pods to terminate
	name := extractLLMISVCName(tc)
	_ = retry.UntilSuccess(ctx, retry.Options{
		Timeout:  2 * time.Minute,
		Interval: 5 * time.Second,
		Name:     fmt.Sprintf("cleanup-wait-%s", name),
	}, func() error {
		output, err := d.kubectl(ctx, "get", "pods", "-n", ns, "-l",
			fmt.Sprintf("app.kubernetes.io/instance=%s", name),
			"-o", "jsonpath={.items}")
		if err != nil {
			return nil // kubectl error likely means resources are gone
		}
		if output == "" || output == "[]" {
			return nil
		}
		return fmt.Errorf("pods still terminating for %s", name)
	})

	return nil
}

// CleanupNamespace deletes the entire namespace.
func (d *Deployer) CleanupNamespace(ctx context.Context, namespace string) error {
	output, err := d.kubectl(ctx, "delete", "namespace", namespace, "--ignore-not-found=true", "--timeout=120s")
	if err != nil {
		return fmt.Errorf("deleting namespace %s: %w\nOutput: %s", namespace, err, output)
	}
	return nil
}

// GetServiceEndpoint returns the inference service URL.
func (d *Deployer) GetServiceEndpoint(ctx context.Context, tc *config.TestCase) (string, error) {
	ns := d.resolveNamespace(tc)
	name := extractLLMISVCName(tc)

	output, err := d.kubectl(ctx, "get", "llmisvc", name, "-n", ns, "-o", "jsonpath={.status.url}")
	if err != nil {
		return "", fmt.Errorf("getting service endpoint: %w", err)
	}

	url := strings.TrimSpace(output)
	if url == "" {
		// Fallback: try to get the service ClusterIP
		svcOutput, err := d.kubectl(ctx, "get", "svc", "-n", ns, "-l",
			fmt.Sprintf("app.kubernetes.io/instance=%s", name),
			"-o", "jsonpath={.items[0].spec.clusterIP}")
		if err != nil || strings.TrimSpace(svcOutput) == "" {
			return "", fmt.Errorf("no endpoint found for llmisvc %s", name)
		}
		port := tc.Validation.HealthPort
		if port == 0 {
			port = 8000
		}
		scheme := "https"
		if tc.Validation.HealthScheme == "HTTP" {
			scheme = "http"
		}
		url = fmt.Sprintf("%s://%s:%d", scheme, strings.TrimSpace(svcOutput), port)
	}

	return url, nil
}

// GetPlatformInfo gathers cluster version info for reporting.
func (d *Deployer) GetPlatformInfo(ctx context.Context) map[string]string {
	info := make(map[string]string)

	if output, err := d.kubectl(ctx, "version", "--short", "-o", "json"); err == nil {
		info["kubernetesVersionRaw"] = output
	}

	// Detect platform
	switch d.Platform {
	case PlatformOCP:
		if output, err := d.runCommand(ctx, "oc", "version"); err == nil {
			info["ocpVersion"] = strings.TrimSpace(output)
		}
	case PlatformAKS:
		info["platform"] = "aks"
	case PlatformGKS:
		info["platform"] = "gks"
	}

	if output, err := d.kubectl(ctx, "get", "nodes", "-o",
		"jsonpath={.items[0].status.nodeInfo.kubeletVersion}"); err == nil {
		info["kubeletVersion"] = strings.TrimSpace(output)
	}

	return info
}

// CheckCRDExists verifies a CRD is installed on the cluster.
func (d *Deployer) CheckCRDExists(ctx context.Context, crdName string) (bool, error) {
	output, err := d.kubectl(ctx, "get", "crd", crdName, "--ignore-not-found=true")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) != "", nil
}

func (d *Deployer) ensureNamespace(ctx context.Context, ns string) error {
	_, err := d.kubectl(ctx, "get", "namespace", ns)
	if err == nil {
		return nil // namespace exists
	}
	_, err = d.kubectl(ctx, "create", "namespace", ns)
	return err
}

func (d *Deployer) kubectl(ctx context.Context, args ...string) (string, error) {
	cmdArgs := make([]string, 0, len(args)+2)
	if d.Kubeconfig != "" {
		cmdArgs = append(cmdArgs, "--kubeconfig", d.Kubeconfig)
	}
	cmdArgs = append(cmdArgs, args...)

	bin := "kubectl"
	if d.Platform == PlatformOCP {
		// Prefer oc on OpenShift, fall back to kubectl
		if _, err := exec.LookPath("oc"); err == nil {
			bin = "oc"
		}
	}

	return d.runCommand(ctx, bin, cmdArgs...)
}

func (d *Deployer) runCommand(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func (d *Deployer) resolveNamespace(tc *config.TestCase) string {
	if tc.Deployment.Namespace != "" {
		return tc.Deployment.Namespace
	}
	if d.Namespace != "" {
		return d.Namespace
	}
	return "llm-test"
}

// extractLLMISVCName extracts the LLMInferenceService name from the test case.
// It derives it from the model display name or test case name.
func extractLLMISVCName(tc *config.TestCase) string {
	if tc.Model.DisplayName != "" {
		return sanitizeK8sName(tc.Model.DisplayName)
	}
	return sanitizeK8sName(tc.Name)
}

func sanitizeK8sName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, " ", "-")
	if len(name) > 63 {
		name = name[:63]
	}
	return strings.Trim(name, "-")
}
