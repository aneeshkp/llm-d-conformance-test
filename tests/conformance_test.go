package tests

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/aneeshkp/llm-d-conformance-test/framework/cleanup"
	"github.com/aneeshkp/llm-d-conformance-test/framework/client"
	"github.com/aneeshkp/llm-d-conformance-test/framework/config"
	"github.com/aneeshkp/llm-d-conformance-test/framework/deployer"
	"github.com/aneeshkp/llm-d-conformance-test/framework/model"
	"github.com/aneeshkp/llm-d-conformance-test/framework/reporter"
	"github.com/aneeshkp/llm-d-conformance-test/framework/retry"
)

var _ = Describe("LLM-D Conformance", func() {
	var (
		ctx        context.Context
		dep        *deployer.Deployer
		dl         *model.Downloader
		cleanupMgr *cleanup.Manager
		rep        *reporter.Reporter
		testCases  []*config.TestCase
	)

	BeforeSuite(func() {
		ctx = context.Background()

		// Resolve platform
		p := deployer.Platform(platform)

		// Create deployer
		dep = deployer.New(kubeconfig, p, namespace)

		// Create model downloader for PVC-based caching
		dl = model.NewDownloader(kubeconfig, namespace, storageClass, platform)

		// Create cleanup manager
		cleanupMgr = cleanup.NewManager(dep)

		// Load test cases based on flags
		var err error
		testCases, err = resolveTestCases()
		Expect(err).NotTo(HaveOccurred(), "Failed to load test cases")
		Expect(testCases).NotTo(BeEmpty(), "No test cases found to run")

		// Determine profile name for reporting
		profileName := "custom"
		if profilePath != "" {
			profile, err := config.LoadProfile(profilePath)
			if err == nil {
				profileName = profile.Name
			}
		}

		// Create reporter
		rep = reporter.New(reportDir, "llm-d-conformance", profileName, string(p))

		// Collect environment info
		info := dep.GetPlatformInfo(ctx)
		rep.SetEnvironment(reporter.EnvironmentInfo{
			Platform:          string(p),
			KubernetesVersion: info["kubeletVersion"],
			Namespace:         namespace,
			Extra:             info,
		})

		GinkgoWriter.Printf("Loaded %d test cases for platform %s\n", len(testCases), p)
	})

	AfterSuite(func() {
		// Cleanup all remaining resources
		if cleanupMgr != nil {
			errs := cleanupMgr.CleanupAll(ctx)
			for _, err := range errs {
				GinkgoWriter.Printf("Cleanup error: %v\n", err)
			}
		}

		// Finalize report
		if rep != nil {
			path, err := rep.Finalize()
			if err != nil {
				GinkgoWriter.Printf("Failed to write report: %v\n", err)
			} else {
				GinkgoWriter.Printf("Report written to: %s\n", path)
			}
		}
	})

	// Dynamically generate test entries for each test case
	When("deploying LLM inference services", func() {
		It("should validate all configured test cases", func() {
			for _, tc := range testCases {
				tc := tc // capture range variable
				runTestCase(ctx, dep, dl, cleanupMgr, rep, tc)
			}
		})
	})
})

// ──────────────────────────────────────────────────────────────────
// Test Lifecycle
//
// Each test case goes through these phases in order. A failure at any
// phase fails the test with a clear error and triggers cleanup.
//
// Phase 1: PREP      — Download model to PVC (if cache.enabled=true)
//                       Pass criteria: download Job succeeds (.status.succeeded=1)
//                       Fail: Job fails or times out
//
// Phase 2: PREREQ    — Check that LLMInferenceService CRD exists
//                       Pass criteria: CRD is installed on the cluster
//                       Fail: CRD not found (cluster not set up correctly)
//
// Phase 3: DEPLOY    — kubectl apply the LLMInferenceService manifest
//                       Pass criteria: kubectl apply exits 0
//                       Fail: manifest not found, invalid YAML, API error
//
// Phase 4: READY     — Wait for llmisvc .status.ready=True
//                       Pass criteria: status becomes True within readyTimeout
//                       Fail: timeout (model download in-pod, OOM, scheduling failure)
//
// Phase 5: HEALTH    — GET /health on the vLLM endpoint
//                       Pass criteria: HTTP 200 within validation.timeout
//                       Fail: non-200 response or connection refused after retries
//
// Phase 6: MODEL     — GET /v1/models, verify model name is listed
//                       Pass criteria: model.name appears in response (warning only if missing)
//
// Phase 7: INFERENCE — POST /v1/chat/completions with test prompts
//                       Pass criteria: non-empty response with choices[].message.content
//                       Fallback: tries /v1/completions if chat fails
//                       Fail: empty response or HTTP error on both APIs
//
// Phase 8: CLEANUP   — kubectl delete the manifest, wait for pod termination
// ──────────────────────────────────────────────────────────────────

func runTestCase(ctx context.Context, dep *deployer.Deployer, dl *model.Downloader, cleanupMgr *cleanup.Manager, rep *reporter.Reporter, tc *config.TestCase) {
	start := time.Now()
	result := reporter.TestResult{
		Name:        tc.Name,
		Description: tc.Description,
		Category:    tc.Model.Category,
		StartTime:   start,
		Model: reporter.ModelInfo{
			Name:           tc.Model.Name,
			URI:            tc.Model.URI,
			Category:       tc.Model.Category,
			ContainerImage: tc.Deployment.ContainerImage,
		},
	}

	// Helper to record failure and clean up
	failTest := func(phase, msg string) {
		result.Status = reporter.StatusFail
		result.Error = fmt.Sprintf("[%s] %s", phase, msg)
		result.EndTime = time.Now()
		result.Duration = time.Since(start).String()
		result.Logs = append(result.Logs, fmt.Sprintf("FAILED at phase %s: %s", phase, msg))
		rep.AddResult(result)
		if tc.Cleanup {
			cleanupMgr.CleanupOne(ctx, tc.Name)
		}
		Fail(fmt.Sprintf("[%s][%s] %s", tc.Name, phase, msg))
	}

	By(fmt.Sprintf("Testing: %s (%s)", tc.Name, tc.Description))

	// ── Phase 1: PREP — Model download/cache ────────────────────
	if tc.Model.Cache != nil && tc.Model.Cache.Enabled {
		By(fmt.Sprintf("[%s] Phase 1: PREP — Downloading model to PVC cache", tc.Name))
		cacheResult := dl.DownloadModel(ctx, tc)
		result.Logs = append(result.Logs, cacheResult.Logs...)

		switch cacheResult.Status {
		case model.CacheStatusReady:
			result.Logs = append(result.Logs, fmt.Sprintf("PREP PASSED: model cached in PVC %s (%s)", cacheResult.PVCName, cacheResult.Duration))
		case model.CacheStatusFailed:
			failTest("PREP", fmt.Sprintf("Model download failed: %v", cacheResult.Error))
			return
		case model.CacheStatusNotFound:
			failTest("PREP", fmt.Sprintf("Model PVC not found: %v", cacheResult.Error))
			return
		default:
			failTest("PREP", fmt.Sprintf("Unexpected cache status: %s", cacheResult.Status))
			return
		}
	} else {
		result.Logs = append(result.Logs, "PREP SKIPPED: model.cache.enabled=false, vLLM will download model at pod startup")
	}

	// ── Phase 2: PREREQ — CRD check ─────────────────────────────
	By(fmt.Sprintf("[%s] Phase 2: PREREQ — Checking CRD prerequisites", tc.Name))
	crdExists, err := dep.CheckCRDExists(ctx, "llminferenceservices.serving.kserve.io")
	if err != nil || !crdExists {
		failTest("PREREQ", fmt.Sprintf("LLMInferenceService CRD not found on cluster: %v", err))
		return
	}
	result.Logs = append(result.Logs, "PREREQ PASSED: LLMInferenceService CRD found")

	// ── Phase 3: DEPLOY — Apply manifest ─────────────────────────
	By(fmt.Sprintf("[%s] Phase 3: DEPLOY — Applying LLMInferenceService manifest", tc.Name))
	deployResult := dep.Deploy(ctx, tc)
	result.Logs = append(result.Logs, deployResult.Logs...)

	if !deployResult.Success {
		failTest("DEPLOY", fmt.Sprintf("kubectl apply failed: %v", deployResult.Error))
		return
	}
	cleanupMgr.Track(tc)
	result.Logs = append(result.Logs, fmt.Sprintf("DEPLOY PASSED: manifest applied in %s", deployResult.Duration))

	// ── Phase 4: READY — Wait for service readiness ──────────────
	By(fmt.Sprintf("[%s] Phase 4: READY — Waiting for service readiness (timeout=%s)", tc.Name, tc.Deployment.ReadyTimeout.Duration))
	err = dep.WaitForReady(ctx, tc)
	if err != nil {
		failTest("READY", fmt.Sprintf("Service did not become ready: %v", err))
		return
	}
	result.Logs = append(result.Logs, "READY PASSED: llmisvc status.ready=True")

	// ── Phase 5: HEALTH — Validate /health endpoint ──────────────
	By(fmt.Sprintf("[%s] Phase 5: HEALTH — Validating /health endpoint", tc.Name))
	endpoint, err := dep.GetServiceEndpoint(ctx, tc)
	if err != nil {
		failTest("HEALTH", fmt.Sprintf("Could not get service endpoint: %v", err))
		return
	}
	result.Logs = append(result.Logs, fmt.Sprintf("Endpoint: %s", endpoint))

	llmClient := client.New(endpoint)
	err = retry.UntilSuccess(ctx, retry.Options{
		Timeout:  tc.Validation.Timeout.Duration,
		Interval: tc.Validation.RetryInterval.Duration,
		Name:     fmt.Sprintf("health-%s", tc.Name),
	}, func() error {
		return llmClient.HealthCheck(ctx)
	})
	if err != nil {
		failTest("HEALTH", fmt.Sprintf("/health returned error after retries: %v", err))
		return
	}
	result.Logs = append(result.Logs, "HEALTH PASSED: /health returned 200 OK")

	// ── Phase 6: MODEL — Validate /v1/models ─────────────────────
	By(fmt.Sprintf("[%s] Phase 6: MODEL — Validating model listing", tc.Name))
	models, err := llmClient.ListModels(ctx)
	if err != nil {
		result.Logs = append(result.Logs, fmt.Sprintf("MODEL WARNING: /v1/models request failed: %v", err))
	} else {
		found := false
		var listedModels []string
		for _, m := range models.Data {
			listedModels = append(listedModels, m.ID)
			if m.ID == tc.Model.Name {
				found = true
			}
		}
		if !found {
			result.Logs = append(result.Logs, fmt.Sprintf("MODEL WARNING: expected %s, got %v", tc.Model.Name, listedModels))
		} else {
			result.Logs = append(result.Logs, fmt.Sprintf("MODEL PASSED: %s found in /v1/models", tc.Model.Name))
		}
	}

	// ── Phase 7: INFERENCE — Test prompts ────────────────────────
	if tc.Validation.InferenceCheck && len(tc.Validation.TestPrompts) > 0 {
		By(fmt.Sprintf("[%s] Phase 7: INFERENCE — Running %d test prompt(s)", tc.Name, len(tc.Validation.TestPrompts)))
		for i, prompt := range tc.Validation.TestPrompts {
			resp, err := llmClient.ChatCompletions(ctx, client.ChatRequest{
				Model: tc.Model.Name,
				Messages: []client.ChatMessage{
					{Role: "user", Content: prompt},
				},
				MaxTokens: 50,
			})
			if err != nil {
				// Fallback to completions API
				compResp, compErr := llmClient.Completions(ctx, client.CompletionRequest{
					Model:     tc.Model.Name,
					Prompt:    prompt,
					MaxTokens: 50,
				})
				if compErr != nil {
					failTest("INFERENCE", fmt.Sprintf("prompt[%d] %q failed on both APIs: chat=%v, completions=%v", i, prompt, err, compErr))
					return
				}
				if len(compResp.Choices) == 0 || compResp.Choices[0].Text == "" {
					failTest("INFERENCE", fmt.Sprintf("prompt[%d] %q returned empty completion response", i, prompt))
					return
				}
				result.Logs = append(result.Logs, fmt.Sprintf("INFERENCE[%d] PASSED via /v1/completions (tokens=%d)", i, compResp.Usage.TotalTokens))
			} else {
				if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
					failTest("INFERENCE", fmt.Sprintf("prompt[%d] %q returned empty chat response", i, prompt))
					return
				}
				result.Logs = append(result.Logs, fmt.Sprintf("INFERENCE[%d] PASSED via /v1/chat/completions (tokens=%d)", i, resp.Usage.TotalTokens))
			}
		}
	} else {
		result.Logs = append(result.Logs, "INFERENCE SKIPPED: inferenceCheck=false or no testPrompts")
	}

	// ── Phase 8: CLEANUP ─────────────────────────────────────────
	if tc.Cleanup {
		By(fmt.Sprintf("[%s] Phase 8: CLEANUP — Removing deployed resources", tc.Name))
		err = cleanupMgr.CleanupOne(ctx, tc.Name)
		if err != nil {
			result.Logs = append(result.Logs, fmt.Sprintf("CLEANUP WARNING: %v", err))
		} else {
			result.Logs = append(result.Logs, "CLEANUP PASSED: all resources removed")
		}
		// Clean up download job but keep PVC for reuse
		if tc.Model.Cache != nil && tc.Model.Cache.Enabled {
			dl.Cleanup(ctx, tc)
			result.Logs = append(result.Logs, "Model cache PVC retained for reuse")
		}
	}

	// ── ALL PHASES PASSED ─────────────────────────────────────────
	result.Status = reporter.StatusPass
	result.EndTime = time.Now()
	result.Duration = time.Since(start).String()
	rep.AddResult(result)

	GinkgoWriter.Printf("[PASS] %s (%s)\n", tc.Name, result.Duration)
}

// resolveTestCases loads and filters test cases based on command-line flags.
func resolveTestCases() ([]*config.TestCase, error) {
	allCases, err := config.LoadTestCasesFromDir(testCaseDir)
	if err != nil {
		return nil, fmt.Errorf("loading test cases from %s: %w", testCaseDir, err)
	}

	// Single test case override
	if testCaseName != "" {
		filtered := config.FilterTestCasesByNames(allCases, []string{testCaseName})
		if len(filtered) == 0 {
			return nil, fmt.Errorf("test case %q not found in %s", testCaseName, testCaseDir)
		}
		return filtered, nil
	}

	// Labels override
	if labels != "" {
		labelList := strings.Split(labels, ",")
		for i := range labelList {
			labelList[i] = strings.TrimSpace(labelList[i])
		}
		return config.FilterTestCasesByLabels(allCases, labelList), nil
	}

	// Profile-based filtering
	if profilePath != "" {
		profile, err := config.LoadProfile(profilePath)
		if err != nil {
			return nil, fmt.Errorf("loading profile: %w", err)
		}
		return config.ResolveProfileTestCases(profile, testCaseDir)
	}

	// Default: run all
	return allCases, nil
}
