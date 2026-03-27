package tests

import (
	"flag"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Command-line flags for controlling test execution.
var (
	profilePath  string
	testCaseDir  string
	platform     string
	namespace    string
	kubeconfig   string
	reportDir    string
	testCaseName string
	labels       string
	storageClass string
)

func init() {
	flag.StringVar(&profilePath, "profile", "", "Path to test profile YAML (e.g., configs/profiles/smoke.yaml)")
	flag.StringVar(&testCaseDir, "testcase-dir", "configs/testcases", "Directory containing test case YAML files")
	flag.StringVar(&platform, "platform", "any", "Target platform: ocp, aks, gks, any")
	flag.StringVar(&namespace, "namespace", "llm-conformance-test", "Default Kubernetes namespace")
	flag.StringVar(&kubeconfig, "kubeconfig", os.Getenv("KUBECONFIG"), "Path to kubeconfig")
	flag.StringVar(&reportDir, "report-dir", "reports", "Directory for JSON reports")
	flag.StringVar(&testCaseName, "testcase", "", "Run a single test case by name (overrides profile)")
	flag.StringVar(&labels, "labels", "", "Comma-separated labels to filter test cases (overrides profile)")
	flag.StringVar(&storageClass, "storage-class", "", "Kubernetes StorageClass for model cache PVCs (uses cluster default if empty)")
}

func TestLLMDConformance(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "LLM-D Conformance Test Suite")
}
