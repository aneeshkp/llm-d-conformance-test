// Package failure provides negative/failure scenario tests.
package failure

import (
	"context"
	"flag"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/aneeshkp/llm-d-conformance-test/framework/config"
	"github.com/aneeshkp/llm-d-conformance-test/framework/deployer"
)

var (
	platform   string
	namespace  string
	kubeconfig string
)

func init() {
	flag.StringVar(&platform, "platform", "any", "Target platform: ocp, aks, gks, any")
	flag.StringVar(&namespace, "namespace", "llm-conformance-test-failure", "Default namespace")
	flag.StringVar(&kubeconfig, "kubeconfig", os.Getenv("KUBECONFIG"), "Kubeconfig path")
}

func TestFailureScenarios(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Failure Scenario Test Suite")
}

var _ = Describe("Failure Scenarios", Ordered, func() {
	var (
		ctx context.Context
		dep *deployer.Deployer
	)

	BeforeAll(func() {
		ctx = context.Background()
		dep = deployer.New(kubeconfig, deployer.Platform(platform), namespace)
	})

	Context("Invalid Manifest", func() {
		It("should fail gracefully when manifest does not exist", func() {
			tc := &config.TestCase{
				Name: "nonexistent-manifest",
				Model: config.ModelConfig{
					Name:     "fake/model",
					Category: "failure",
				},
				Deployment: config.DeployConfig{
					ManifestPath: "deploy/manifests/does-not-exist.yaml",
				},
				Cleanup: true,
			}

			result := dep.Deploy(ctx, tc)
			Expect(result.Success).To(BeFalse(), "Expected deployment to fail")
			Expect(result.Error).To(HaveOccurred())
			Expect(result.Error.Error()).To(ContainSubstring("not found"))
		})
	})

	Context("Missing CRD", func() {
		It("should detect when LLMInferenceService CRD is missing", func() {
			// This test is informational - on a properly configured cluster the CRD will exist.
			// On a bare cluster it validates that our CRD check works.
			exists, err := dep.CheckCRDExists(ctx, "llminferenceservices.serving.kserve.io")
			if err != nil {
				GinkgoWriter.Printf("CRD check returned error (expected if no cluster): %v\n", err)
				Skip("No cluster available to check CRD")
			}
			GinkgoWriter.Printf("LLMInferenceService CRD exists: %v\n", exists)
		})
	})

	Context("Invalid Model Configuration", func() {
		It("should fail when deploying with empty model URI", func() {
			tc := &config.TestCase{
				Name: "empty-model-uri",
				Model: config.ModelConfig{
					Name:     "",
					URI:      "",
					Category: "failure",
				},
				Deployment: config.DeployConfig{
					ManifestPath: "", // no manifest
				},
				Cleanup: true,
			}

			result := dep.Deploy(ctx, tc)
			Expect(result.Success).To(BeFalse())
		})
	})

	Context("Resource Constraints", func() {
		It("should report deployment failure when requesting unavailable resources", func() {
			// This test case uses an absurd resource request that will fail to schedule
			tc := &config.TestCase{
				Name: "impossible-resources",
				Model: config.ModelConfig{
					Name:        "facebook/opt-125m",
					URI:         "hf://facebook/opt-125m",
					DisplayName: "impossible-resource-test",
					Category:    "failure",
				},
				Deployment: config.DeployConfig{
					ManifestPath: "deploy/manifests/opt-125m-cpu.yaml",
					Namespace:    namespace,
					ReadyTimeout: config.Duration{},
					Resources: config.ResourceConfig{
						CPU:    "1000",
						Memory: "99999Gi",
						GPUs:   0,
					},
				},
				Cleanup: true,
			}
			// Note: The manifest itself has reasonable resources. This test validates
			// that if the service doesn't become ready, the timeout and error handling
			// work correctly. In a real run, you'd use a modified manifest.
			GinkgoWriter.Printf("Resource constraint test would deploy %s\n", tc.Name)
			Skip("Skipping resource constraint test - requires modified manifest")
		})
	})
})
