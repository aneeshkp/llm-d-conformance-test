// Package smoke provides a standalone smoke test that validates the framework works
// without requiring a real Kubernetes cluster or GPU resources.
package smoke

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/aneeshkp/llm-d-conformance-test/framework/config"
	"github.com/aneeshkp/llm-d-conformance-test/framework/reporter"
)

func TestSmoke(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Smoke Test Suite - Framework Validation")
}

var _ = Describe("Framework Smoke Tests", func() {
	Context("Config Loading", func() {
		It("should load all test case configs without error", func() {
			testCaseDir := findTestCaseDir()
			cases, err := config.LoadTestCasesFromDir(testCaseDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(cases).NotTo(BeEmpty(), "Expected at least one test case")

			By("Verifying each test case has required fields")
			for _, tc := range cases {
				Expect(tc.Name).NotTo(BeEmpty(), "Test case missing name")
				Expect(tc.Model.Name).NotTo(BeEmpty(), "Test case %s missing model name", tc.Name)
				Expect(tc.Deployment.ManifestPath).NotTo(BeEmpty(), "Test case %s missing manifest path", tc.Name)
				Expect(tc.Model.Category).NotTo(BeEmpty(), "Test case %s missing model category", tc.Name)

				GinkgoWriter.Printf("  Loaded: %s (category=%s, gpus=%d)\n", tc.Name, tc.Model.Category, tc.Deployment.Resources.GPUs)
			}
		})

		It("should load all profile configs without error", func() {
			profileDir := findProfileDir()
			entries, err := os.ReadDir(profileDir)
			Expect(err).NotTo(HaveOccurred())

			profiles := 0
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				profile, err := config.LoadProfile(filepath.Join(profileDir, entry.Name()))
				Expect(err).NotTo(HaveOccurred(), "Failed to load profile %s", entry.Name())
				Expect(profile.Name).NotTo(BeEmpty())
				Expect(profile.TestCases).NotTo(BeEmpty(), "Profile %s has no test cases", profile.Name)
				profiles++

				GinkgoWriter.Printf("  Profile: %s (%d test cases, timeout=%s)\n",
					profile.Name, len(profile.TestCases), profile.Timeout.Duration)
			}
			Expect(profiles).To(BeNumerically(">", 0), "No profiles found")
		})

		It("should resolve smoke profile test cases", func() {
			profileDir := findProfileDir()
			testCaseDir := findTestCaseDir()

			profile, err := config.LoadProfile(filepath.Join(profileDir, "smoke.yaml"))
			Expect(err).NotTo(HaveOccurred())

			cases, err := config.ResolveProfileTestCases(profile, testCaseDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(cases).To(HaveLen(len(profile.TestCases)),
				"Expected %d test cases for smoke profile, got %d", len(profile.TestCases), len(cases))

			for _, tc := range cases {
				GinkgoWriter.Printf("  Resolved: %s -> %s\n", tc.Name, tc.Deployment.ManifestPath)
			}
		})
	})

	Context("Config Filtering", func() {
		It("should filter test cases by name", func() {
			testCaseDir := findTestCaseDir()
			allCases, err := config.LoadTestCasesFromDir(testCaseDir)
			Expect(err).NotTo(HaveOccurred())

			filtered := config.FilterTestCasesByNames(allCases, []string{"single-gpu"})
			Expect(filtered).To(HaveLen(1))
			Expect(filtered[0].Name).To(Equal("single-gpu"))
		})
	})

	Context("Manifest Validation", func() {
		It("should have manifests that exist on disk for all test cases", func() {
			rootDir := findRootDir()
			manifestDir := filepath.Join(rootDir, "deploy", "manifests")
			if _, err := os.Stat(manifestDir); os.IsNotExist(err) {
				Skip("deploy/manifests/ not found — run 'make setup' to clone manifest repo")
			}

			testCaseDir := findTestCaseDir()
			cases, err := config.LoadTestCasesFromDir(testCaseDir)
			Expect(err).NotTo(HaveOccurred())

			for _, tc := range cases {
				manifestPath := filepath.Join(manifestDir, tc.Deployment.ManifestPath)
				_, err := os.Stat(manifestPath)
				Expect(err).NotTo(HaveOccurred(),
					"Manifest %s for test case %s does not exist — run 'make setup'", tc.Deployment.ManifestPath, tc.Name)
				GinkgoWriter.Printf("  Verified manifest: %s\n", tc.Deployment.ManifestPath)
			}
		})
	})

	Context("Reporter", func() {
		It("should generate a valid JSON report", func() {
			tmpDir, err := os.MkdirTemp("", "llm-d-test-report-*")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(tmpDir) }()

			rep := reporter.New(tmpDir, "smoke-test", "smoke", "any")
			rep.SetEnvironment(reporter.EnvironmentInfo{
				Platform:          "any",
				KubernetesVersion: "v1.28.0",
				Namespace:         "test-ns",
			})

			rep.AddResult(reporter.TestResult{
				Name:     "test-pass",
				Category: "smoke",
				Status:   reporter.StatusPass,
				Duration: "1s",
				Model: reporter.ModelInfo{
					Name:     "test-model",
					URI:      "hf://test",
					Category: "smoke",
				},
			})

			rep.AddResult(reporter.TestResult{
				Name:     "test-fail",
				Category: "smoke",
				Status:   reporter.StatusFail,
				Duration: "2s",
				Error:    "simulated failure",
				Model: reporter.ModelInfo{
					Name:     "test-model-2",
					URI:      "hf://test2",
					Category: "smoke",
				},
			})

			htmlPath, err := rep.Finalize()
			Expect(err).NotTo(HaveOccurred())
			Expect(htmlPath).To(BeAnExistingFile())

			// Verify JSON report (same dir, .json extension)
			jsonPath := strings.TrimSuffix(htmlPath, ".html") + ".json"
			Expect(jsonPath).To(BeAnExistingFile())
			data, err := os.ReadFile(jsonPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(ContainSubstring(`"total": 2`))
			Expect(string(data)).To(ContainSubstring(`"passed": 1`))
			Expect(string(data)).To(ContainSubstring(`"failed": 1`))

			// Verify HTML report
			htmlData, err := os.ReadFile(htmlPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(htmlData)).To(ContainSubstring("LLM-D Conformance Report"))

			GinkgoWriter.Printf("  JSON report: %s\n", jsonPath)
			GinkgoWriter.Printf("  HTML report: %s\n", htmlPath)
		})
	})
})

// findRootDir walks up from the current working directory to find the project root.
func findRootDir() string {
	// Try relative paths from the test directory
	candidates := []string{
		"../..",
		"..",
		".",
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "go.mod")); err == nil {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	// Fallback
	return "."
}

func findTestCaseDir() string {
	return filepath.Join(findRootDir(), "configs", "testcases")
}

func findProfileDir() string {
	return filepath.Join(findRootDir(), "configs", "profiles")
}
