// Package happy_path provides happy-path tests that deploy and validate LLM inference services.
package happy_path

import (
	"context"
	"flag"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/aneeshkp/llm-d-conformance-test/framework/client"
	"github.com/aneeshkp/llm-d-conformance-test/framework/config"
	"github.com/aneeshkp/llm-d-conformance-test/framework/deployer"
	"github.com/aneeshkp/llm-d-conformance-test/framework/retry"
)

var (
	profilePath string
	testCaseDir string
	platform    string
	namespace   string
	kubeconfig  string
)

func init() {
	flag.StringVar(&profilePath, "profile", "", "Path to test profile YAML")
	flag.StringVar(&testCaseDir, "testcase-dir", "configs/testcases", "Test case directory")
	flag.StringVar(&platform, "platform", "any", "Target platform: ocp, aks, gks, any")
	flag.StringVar(&namespace, "namespace", "llm-conformance-test", "Default namespace")
	flag.StringVar(&kubeconfig, "kubeconfig", os.Getenv("KUBECONFIG"), "Kubeconfig path")
}

func TestHappyPath(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Happy Path Test Suite")
}

var _ = Describe("Happy Path - Deploy and Validate", Ordered, func() {
	var (
		ctx context.Context
		dep *deployer.Deployer
	)

	BeforeAll(func() {
		ctx = context.Background()
		dep = deployer.New(kubeconfig, deployer.Platform(platform), namespace)
	})

	Context("CPU Models", Label("cpu"), func() {
		It("should deploy and validate OPT-125M on CPU", func() {
			tc := loadTestCase("opt-125m-cpu")
			deployAndValidate(ctx, dep, tc)
		})

		It("should deploy OPT-125M with prefill/decode disaggregation", Label("pd"), func() {
			tc := loadTestCase("opt-125m-cpu-pd")
			deployAndValidate(ctx, dep, tc)
		})
	})

	Context("Single-Node GPU Models", Label("gpu"), func() {
		It("should deploy and validate Qwen2.5-7B on GPU", func() {
			tc := loadTestCase("qwen2-7b-gpu")
			deployAndValidate(ctx, dep, tc)
		})

		It("should deploy Qwen2.5-7B with PD disaggregation", Label("pd", "rdma"), func() {
			tc := loadTestCase("qwen2-7b-gpu-pd")
			deployAndValidate(ctx, dep, tc)
		})
	})

	Context("Cache-Aware Routing", Label("cache-aware"), func() {
		It("should deploy simulator with KV cache routing", Label("cpu"), func() {
			tc := loadTestCase("cache-aware-sim-cpu")
			deployAndValidate(ctx, dep, tc)
		})

		It("should deploy Qwen2.5-7B with KV cache routing", Label("gpu"), func() {
			tc := loadTestCase("cache-aware-qwen2-7b-gpu")
			deployAndValidate(ctx, dep, tc)
		})
	})

	Context("DeepSeek MoE Models", Label("deepseek", "moe"), func() {
		It("should deploy DeepSeek-R1 with DP/EP high throughput", Label("rdma"), func() {
			tc := loadTestCase("deepseek-r1-dp-ep-ht")
			deployAndValidate(ctx, dep, tc)
		})

		It("should deploy DeepSeek-Coder-V2 with naive backend", func() {
			tc := loadTestCase("deepseek-coder-v2-dp-ep-naive")
			deployAndValidate(ctx, dep, tc)
		})
	})
})

func loadTestCase(name string) *config.TestCase {
	dir := testCaseDir
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		// Try from project root
		for _, candidate := range []string{"../../configs/testcases", "../configs/testcases"} {
			if _, err := os.Stat(candidate); err == nil {
				dir = candidate
				break
			}
		}
	}
	cases, err := config.LoadTestCasesFromDir(dir)
	Expect(err).NotTo(HaveOccurred())
	filtered := config.FilterTestCasesByNames(cases, []string{name})
	Expect(filtered).To(HaveLen(1), "Test case %s not found", name)
	return filtered[0]
}

func deployAndValidate(ctx context.Context, dep *deployer.Deployer, tc *config.TestCase) {
	// Deploy
	result := dep.Deploy(ctx, tc)
	Expect(result.Success).To(BeTrue(), "Deployment failed: %v", result.Error)

	defer func() {
		if tc.Cleanup {
			err := dep.Cleanup(ctx, tc)
			Expect(err).NotTo(HaveOccurred(), "Cleanup failed")
		}
	}()

	// Wait ready
	err := dep.WaitForReady(ctx, tc)
	Expect(err).NotTo(HaveOccurred(), "Service did not become ready")

	// Get endpoint
	endpoint, err := dep.GetServiceEndpoint(ctx, tc)
	Expect(err).NotTo(HaveOccurred(), "Could not get service endpoint")

	// Health check
	llmClient := client.New(endpoint)
	err = retry.UntilSuccess(ctx, retry.Options{
		Timeout:  tc.Validation.Timeout.Duration,
		Interval: 10 * time.Second,
		Name:     "health-check",
	}, func() error {
		return llmClient.HealthCheck(ctx)
	})
	Expect(err).NotTo(HaveOccurred(), "Health check failed")

	// Inference check
	if tc.Validation.InferenceCheck && len(tc.Validation.TestPrompts) > 0 {
		resp, err := llmClient.ChatCompletions(ctx, client.ChatRequest{
			Model: tc.Model.Name,
			Messages: []client.ChatMessage{
				{Role: "user", Content: tc.Validation.TestPrompts[0]},
			},
			MaxTokens: 50,
		})
		Expect(err).NotTo(HaveOccurred(), "Inference request failed")
		Expect(resp.Choices).NotTo(BeEmpty(), "No choices in response")
		Expect(resp.Choices[0].Message.Content).NotTo(BeEmpty(), "Empty response content")
	}
}
