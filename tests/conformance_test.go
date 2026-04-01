package tests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"

	"github.com/aneeshkp/llm-d-conformance-test/framework/cleanup"
	"github.com/aneeshkp/llm-d-conformance-test/framework/client"
	"github.com/aneeshkp/llm-d-conformance-test/framework/config"
	"github.com/aneeshkp/llm-d-conformance-test/framework/deployer"
	"github.com/aneeshkp/llm-d-conformance-test/framework/model"
	"github.com/aneeshkp/llm-d-conformance-test/framework/reporter"
	"github.com/aneeshkp/llm-d-conformance-test/framework/retry"
)

var (
	ctx        context.Context
	dep        *deployer.Deployer
	dl         *model.Downloader
	cleanupMgr *cleanup.Manager
	rep        *reporter.Reporter
	testCases  []*config.TestCase
)

var _ = BeforeSuite(func() {
	ctx = context.Background()

	// Resolve platform
	p := deployer.Platform(platform)

	// Create deployer
	dep = deployer.New(kubeconfig, p, namespace)
	dep.LogFunc = func(format string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}

	// Create model downloader for PVC-based caching
	dl = model.NewDownloader(kubeconfig, namespace, storageClass, platform)
	dl.LogFunc = func(format string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}

	// Create cleanup manager
	cleanupMgr = cleanup.NewManager(dep)

	// Load filtered test cases (for profile support and reporter)
	var err error
	testCases, err = resolveTestCases()
	if err != nil {
		GinkgoWriter.Printf("Warning: failed to resolve test cases: %v\n", err)
	}
	// Build O(1) lookup set for profile filtering
	resolvedTestCaseSet = make(map[string]bool, len(testCases))
	for _, tc := range testCases {
		resolvedTestCaseSet[tc.Name] = true
	}

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

	GinkgoWriter.Printf("Loaded %d test cases for platform %s (mode=%s)\n", len(testCases), p, testMode)
	if testMode == "discover" {
		GinkgoWriter.Printf("Discover mode: will validate existing deployment at endpoint %s\n", endpoint)
	}
})

var _ = AfterSuite(func() {
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

// loadAllTestCases loads test cases at spec construction time.
// This runs during Describe (before BeforeSuite), so we use findRootDir directly.
func loadAllTestCases() []*config.TestCase {
	rootDir := findRootDir()
	dir := filepath.Join(rootDir, "configs", "testcases")
	cases, err := config.LoadTestCasesFromDir(dir)
	if err != nil {
		// Can't use Expect here (not in a spec), just return empty
		fmt.Fprintf(os.Stderr, "WARNING: failed to load test cases: %v\n", err)
		return nil
	}
	return cases
}

// shouldRunTestCase checks if a test case matches the runtime filters.
func shouldRunTestCase(tc *config.TestCase) bool {
	if testCaseName != "" && tc.Name != testCaseName {
		return false
	}
	if labels != "" {
		filtered := config.FilterTestCasesByLabels([]*config.TestCase{tc}, splitLabels(labels))
		if len(filtered) == 0 {
			return false
		}
	}
	if profilePath != "" && len(testCases) > 0 {
		if _, ok := resolvedTestCaseSet[tc.Name]; !ok {
			return false
		}
	}
	return true
}

// splitLabels splits and trims a comma-separated label string.
func splitLabels(s string) []string {
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// skipIfDiscover skips the current spec if running in discover mode.
func skipIfDiscover() {
	if testMode == "discover" {
		Skip("discover mode — using existing deployment")
	}
}

// resolvedTestCaseSet is populated in BeforeSuite for O(1) profile filtering.
var resolvedTestCaseSet map[string]bool

var _ = Describe("LLM-D Conformance", func() {
	allCases := loadAllTestCases()

	for _, tc := range allCases {
		tc := tc // capture range variable

		// Convert labels to Ginkgo Labels (safe copy to avoid slice mutation)
		allLabels := make([]string, 0, len(tc.Labels)+1)
		allLabels = append(allLabels, tc.Labels...)
		allLabels = append(allLabels, tc.Model.Category)
		ginkgoLabels := Label(allLabels...)

		// Each test case is an Ordered container — phases run sequentially,
		// and if one fails, subsequent phases are skipped.
		Describe(tc.Name, Ordered, ginkgoLabels, func() {
			var (
				svcEndpoint  string
				llmClient    *client.LLMClient
				start        time.Time
				shouldRun    bool
				modelInfo    reporter.ModelInfo
			)

			BeforeAll(func() {
				shouldRun = shouldRunTestCase(tc)
				if !shouldRun {
					Skip(fmt.Sprintf("filtered out by flags (testcase=%s, labels=%s)", testCaseName, labels))
				}
				// Resolve manifest path
				if tc.Deployment.ManifestPath != "" && !filepath.IsAbs(tc.Deployment.ManifestPath) {
					rootDir := findRootDir()
					tc.Deployment.ManifestPath = filepath.Join(rootDir, "deploy", "manifests", modelSource, tc.Deployment.ManifestPath)
				}
				start = time.Now()
				// Determine the actual model URI based on model source
				modelURI := tc.Model.URI
				if modelSource == "pvc" && !strings.HasPrefix(tc.Model.URI, "pvc://") {
					// Read the URI from the pvc manifest to get the actual pvc:// path
					manifestPath := tc.Deployment.ManifestPath
					if manifestPath != "" {
						data, err := os.ReadFile(manifestPath)
						if err == nil {
							for _, line := range strings.Split(string(data), "\n") {
								trimmed := strings.TrimSpace(line)
								if strings.HasPrefix(trimmed, "uri:") {
									modelURI = strings.TrimSpace(strings.TrimPrefix(trimmed, "uri:"))
									break
								}
							}
						}
					}
				}
				modelInfo = reporter.ModelInfo{
					Name:     tc.Model.Name,
					URI:      modelURI,
					Category: tc.Model.Category,
				}
				logStep("Testing: %s (%s) [mode=%s, model-source=%s]", tc.Name, tc.Description, testMode, modelSource)
			})

			// Record each phase result directly to reporter
			AfterEach(func() {
				if !shouldRun {
					return
				}
				report := CurrentSpecReport()
				status := reporter.StatusPass
				if report.Failed() {
					status = reporter.StatusFail
				} else if report.State.String() == "skipped" {
					status = reporter.StatusSkip
				}
				if rep != nil {
					rep.AddResult(reporter.TestResult{
						Name:      fmt.Sprintf("%s/%s", tc.Name, report.LeafNodeText),
						Category:  tc.Model.Category,
						Status:    status,
						StartTime: start,
						EndTime:   time.Now(),
						Duration:  time.Since(start).String(),
						Model:     modelInfo,
					})
				}
			})

			// ── Phase 1: PREP ──────────────────────────────────────
			It("should download model to PVC cache", func() {
				skipIfDiscover()
				if modelSource == "hf" {
					Skip("model-source=hf — vLLM downloads at startup")
				}
				if strings.HasPrefix(tc.Model.URI, "pvc://") {
					Skip(fmt.Sprintf("model URI is already pvc:// (%s)", tc.Model.URI))
				}
				if tc.Model.Cache == nil || !tc.Model.Cache.Enabled {
					Skip("cache not enabled for this test case")
				}

				logStep("[%s] Phase 1: PREP — Downloading model to PVC cache", tc.Name)
				cacheResult := dl.DownloadModel(ctx, tc)
				if cacheResult.Status == model.CacheStatusFailed {
					Fail(fmt.Sprintf("Model download failed: %v", cacheResult.Error))
				}
				if cacheResult.Status == model.CacheStatusNotFound {
					Fail(fmt.Sprintf("Model PVC not found: %v", cacheResult.Error))
				}
				logStep("[%s] PREP PASSED: model cached in PVC %s (%s)", tc.Name, cacheResult.PVCName, cacheResult.Duration)
			})

			// ── Phase 2: PREREQ ────────────────────────────────────
			It("should have LLMInferenceService CRD installed", func() {
				skipIfDiscover()
				logStep("[%s] Phase 2: PREREQ — Checking CRD", tc.Name)
				crdExists, err := dep.CheckCRDExists(ctx, "llminferenceservices.serving.kserve.io")
				if err != nil || !crdExists {
					Fail(fmt.Sprintf("LLMInferenceService CRD not found: %v", err))
				}
			})

			// ── Phase 3: DEPLOY ────────────────────────────────────
			It("should deploy LLMInferenceService manifest", func() {
				skipIfDiscover()
				logStep("[%s] Phase 3: DEPLOY — Applying manifest (model-source=%s)", tc.Name, modelSource)
				deployResult := dep.Deploy(ctx, tc)
				if !deployResult.Success {
					Fail(fmt.Sprintf("kubectl apply failed: %v", deployResult.Error))
				}
				cleanupMgr.Track(tc)
				logStep("[%s] DEPLOY PASSED in %s", tc.Name, deployResult.Duration)

				// Update modelInfo with the actual deployed URI (pvc:// or hf://)
				name := deployer.ExtractLLMISVCName(tc)
				deployedURI, _ := dep.Kubectl(ctx, "get", "llmisvc", name, "-n", dep.Namespace,
					"-o", "jsonpath={.spec.model.uri}")
				if uri := strings.TrimSpace(deployedURI); uri != "" {
					modelInfo.URI = uri
					logStep("[%s]   Model URI: %s", tc.Name, uri)
				}
			})

			// ── Phase 4a: Service created ──────────────────────────
			It("should create Service", func() {
				skipIfDiscover()
				name := deployer.ExtractLLMISVCName(tc)
				logStep("[%s] Checking Service", tc.Name)

				err := retry.UntilSuccess(ctx, retry.Options{
					Timeout:  2 * time.Minute,
					Interval: 5 * time.Second,
					Name:     "service-" + name,
				}, func() error {
					ok, _ := dep.CheckResourceExists(ctx, "svc", name, dep.Namespace)
					if !ok {
						return fmt.Errorf("no Service found for %s", name)
					}
					return nil
				})
				if err != nil {
					Fail(fmt.Sprintf("Service not created: %v", err))
				}
			})

			// ── Phase 4b: HTTPRoute created ────────────────────────
			It("should create HTTPRoute", func() {
				skipIfDiscover()
				name := deployer.ExtractLLMISVCName(tc)
				logStep("[%s] Checking HTTPRoute", tc.Name)

				err := retry.UntilSuccess(ctx, retry.Options{
					Timeout:  2 * time.Minute,
					Interval: 5 * time.Second,
					Name:     "httproute-" + name,
				}, func() error {
					ok, _ := dep.CheckResourceExists(ctx, "httproute", name, dep.Namespace)
					if !ok {
						return fmt.Errorf("no HTTPRoute found for %s", name)
					}
					return nil
				})
				if err != nil {
					Fail(fmt.Sprintf("HTTPRoute not created: %v", err))
				}
			})

			// ── Phase 4c: InferencePool created ────────────────────
			It("should create InferencePool", func() {
				skipIfDiscover()
				logStep("[%s] Checking InferencePool", tc.Name)

				err := retry.UntilSuccess(ctx, retry.Options{
					Timeout:  2 * time.Minute,
					Interval: 5 * time.Second,
					Name:     "inferencepool-" + tc.Name,
				}, func() error {
					ok, _ := dep.CheckInferencePoolExists(ctx, dep.Namespace)
					if !ok {
						return fmt.Errorf("no InferencePool found")
					}
					return nil
				})
				if err != nil {
					Fail(fmt.Sprintf("InferencePool not created: %v", err))
				}
			})

			// ── Phase 4d: Pods running ─────────────────────────────
			It("should have pods running without crashes", func() {
				skipIfDiscover()
				name := deployer.ExtractLLMISVCName(tc)
				logStep("[%s] Checking pods", tc.Name)

				err := retry.UntilSuccess(ctx, retry.Options{
					Timeout:  3 * time.Minute,
					Interval: 10 * time.Second,
					Name:     "pods-" + name,
				}, func() error {
					out, _ := dep.GetPodStatus(ctx, name, dep.Namespace)
					pods := strings.TrimSpace(out)
					if pods == "" {
						return fmt.Errorf("no pods found for %s", name)
					}
					for _, line := range strings.Split(pods, "\n") {
						if strings.Contains(line, "restarts=") {
							parts := strings.Split(line, "restarts=")
							if len(parts) > 1 && parts[1] != "0" && parts[1] != "" {
								logs, _ := dep.GetPodLogs(ctx, name, dep.Namespace, 10)
								Fail(fmt.Sprintf("Pod crash detected: %s\nLogs:\n%s", line, logs))
							}
						}
						logStep("[%s]   %s", tc.Name, line)
					}
					return nil
				})
				if err != nil {
					Fail(fmt.Sprintf("Pods not running: %v", err))
				}
			})

			// ── Phase 4e: Model downloaded (HF mode) ───────────────
			It("should download model via storage initializer", func() {
				skipIfDiscover()
				if modelSource != "hf" {
					Skip("model-source=pvc — model pre-cached, no init container download")
				}
				name := deployer.ExtractLLMISVCName(tc)
				logStep("[%s] Checking storage-initializer completed", tc.Name)

				err := retry.UntilSuccess(ctx, retry.Options{
					Timeout:  tc.Deployment.ReadyTimeout.Duration,
					Interval: 15 * time.Second,
					Name:     "storage-init-" + name,
				}, func() error {
					// Check init container status
					out, _ := dep.Kubectl(ctx, "get", "pods", "-n", dep.Namespace, "-l",
						fmt.Sprintf("app.kubernetes.io/name=%s,app.kubernetes.io/component=llminferenceservice-workload", name),
						"-o", "jsonpath={range .items[*]}{range .status.initContainerStatuses[*]}{.name}={.ready} {end}{end}")
					initStatus := strings.TrimSpace(out)
					if initStatus == "" {
						return fmt.Errorf("no init containers found yet")
					}
					// Check if storage-initializer completed
					if !strings.Contains(initStatus, "storage-initializer=true") {
						// Show init container logs for progress
						initLogs, _ := dep.Kubectl(ctx, "logs", "-n", dep.Namespace, "-l",
							fmt.Sprintf("app.kubernetes.io/name=%s,app.kubernetes.io/component=llminferenceservice-workload", name),
							"-c", "storage-initializer", "--tail=1")
						initLogs = strings.TrimSpace(initLogs)
						if initLogs != "" &&
							!strings.Contains(initLogs, "Defaulted container") &&
							!strings.Contains(initLogs, "Error from server") {
							logStep("[%s]   storage-initializer: %s", tc.Name, initLogs)
						}
						return fmt.Errorf("storage-initializer not ready: %s", initStatus)
					}
					logStep("[%s]   storage-initializer completed (model downloaded from HuggingFace)", tc.Name)
					return nil
				})
				if err != nil {
					Fail(fmt.Sprintf("Model download via storage-initializer failed: %v", err))
				}
			})

			// ── Phase 4f: READY ────────────────────────────────────
			It("should become READY", func() {
				skipIfDiscover()
				logStep("[%s] Phase 4: READY — Waiting for service readiness (timeout=%s)", tc.Name, tc.Deployment.ReadyTimeout.Duration)
				err := dep.WaitForReady(ctx, tc)
				if err != nil {
					Fail(fmt.Sprintf("Service did not become ready: %v", err))
				}
				// Capture the actual vLLM image and version from the deployed pod
				name := deployer.ExtractLLMISVCName(tc)
				label := fmt.Sprintf("app.kubernetes.io/name=%s,app.kubernetes.io/component=llminferenceservice-workload", name)

				// Get image
				vllmImage, _ := dep.Kubectl(ctx, "get", "pods", "-n", dep.Namespace, "-l", label,
					"-o", "jsonpath={.items[0].spec.containers[?(@.name==\"main\")].image}")
				if img := strings.TrimSpace(vllmImage); img != "" {
					modelInfo.VLLMImage = img
					logStep("[%s]   vLLM image: %s", tc.Name, img)
				}

				// Get version from inside the running pod
				podName, _ := dep.Kubectl(ctx, "get", "pods", "-n", dep.Namespace, "-l", label,
					"-o", "jsonpath={.items[0].metadata.name}")
				if pod := strings.TrimSpace(podName); pod != "" {
					ver, _ := dep.Kubectl(ctx, "exec", pod, "-n", dep.Namespace, "-c", "main",
						"--", "python3", "-c", "import vllm; print(vllm.__version__)")
					// Extract just the version line (filter warnings/noise)
					for _, line := range strings.Split(ver, "\n") {
						line = strings.TrimSpace(line)
						if line != "" &&
							!strings.Contains(line, "Warning") &&
							!strings.Contains(line, "Error") &&
							!strings.Contains(line, "import") &&
							!strings.Contains(line, "Traceback") {
							modelInfo.VLLMVersion = line
							logStep("[%s]   vLLM version: %s", tc.Name, line)
							break
						}
					}
				}
			})

			// ── Phase 5: Model files at /mnt/models ────────────────
			It("should have model files at /mnt/models", func() {
				skipIfDiscover()
				name := deployer.ExtractLLMISVCName(tc)
				logStep("[%s] Checking model files at /mnt/models", tc.Name)

				// Get pod name first, then exec into it
				label := fmt.Sprintf("app.kubernetes.io/name=%s,app.kubernetes.io/component=llminferenceservice-workload", name)
				podName, _ := dep.Kubectl(ctx, "get", "pods", "-n", dep.Namespace, "-l", label,
					"-o", "jsonpath={.items[0].metadata.name}")
				pod := strings.TrimSpace(podName)
				if pod == "" {
					Fail("No vLLM pod found to check /mnt/models")
				}

				out, err := dep.Kubectl(ctx, "exec", pod, "-n", dep.Namespace, "-c", "main",
					"--", "ls", "/mnt/models/")
				if err != nil {
					Fail(fmt.Sprintf("Failed to list /mnt/models: %v", err))
				}
				files := strings.TrimSpace(out)
				if files == "" {
					Fail("/mnt/models is empty — model not found")
				}

				// Check for config.json (required by vLLM)
				if !strings.Contains(files, "config.json") {
					Fail(fmt.Sprintf("/mnt/models missing config.json, found: %s", files))
				}

				// Check for model weights
				hasWeights := strings.Contains(files, ".safetensors") ||
					strings.Contains(files, ".bin") ||
					strings.Contains(files, ".gguf")
				if !hasWeights {
					Fail(fmt.Sprintf("/mnt/models missing model weights (.safetensors/.bin/.gguf), found: %s", files))
				}

				logStep("[%s]   /mnt/models contents: %s", tc.Name, strings.ReplaceAll(files, "\n", ", "))
			})

			// ── Phase 6: HEALTH ────────────────────────────────────
			It("should pass health check", func() {
				logStep("[%s] Phase 5: HEALTH — Validating /health endpoint", tc.Name)

				if testMode == "discover" {
					svcEndpoint = endpoint
				} else {
					var err error
					svcEndpoint, err = dep.GetServiceEndpoint(ctx, tc)
					if err != nil {
						Fail(fmt.Sprintf("Could not get service endpoint: %v", err))
					}
				}
				logStep("[%s]   Endpoint: %s", tc.Name, svcEndpoint)

				llmClient = client.New(svcEndpoint)
				err := retry.UntilSuccess(ctx, retry.Options{
					Timeout:  tc.Validation.Timeout.Duration,
					Interval: tc.Validation.RetryInterval.Duration,
					Name:     "health-" + tc.Name,
				}, func() error {
					return llmClient.HealthCheck(ctx)
				})
				if err != nil {
					Fail(fmt.Sprintf("/health failed: %v", err))
				}
			})

			// ── Phase 6: MODEL ─────────────────────────────────────
			It("should list model in /v1/models", func() {
				logStep("[%s] Phase 6: MODEL — Validating model listing", tc.Name)
				models, err := llmClient.ListModels(ctx)
				if err != nil {
					Fail(fmt.Sprintf("/v1/models request failed: %v", err))
				}
				found := false
				var listedModels []string
				for _, m := range models.Data {
					listedModels = append(listedModels, m.ID)
					if m.ID == tc.Model.Name {
						found = true
					}
				}
				if !found {
					logStep("[%s]   WARNING: expected %s, got %v", tc.Name, tc.Model.Name, listedModels)
				} else {
					logStep("[%s]   Model %s found", tc.Name, tc.Model.Name)
				}
			})

			// ── Phase 7: INFERENCE ─────────────────────────────────
			It("should return inference response", func() {
				if !tc.Validation.InferenceCheck || len(tc.Validation.TestPrompts) == 0 {
					Skip("inferenceCheck=false or no testPrompts")
				}
				logStep("[%s] Phase 7: INFERENCE — Running %d test prompt(s)", tc.Name, len(tc.Validation.TestPrompts))

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
							Fail(fmt.Sprintf("prompt[%d] %q failed: chat=%v, completions=%v", i, prompt, err, compErr))
						}
						if len(compResp.Choices) == 0 || compResp.Choices[0].Text == "" {
							Fail(fmt.Sprintf("prompt[%d] %q returned empty response", i, prompt))
						}
						logStep("[%s]   prompt[%d] PASSED via /v1/completions (tokens=%d)", tc.Name, i, compResp.Usage.TotalTokens)
					} else {
						if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
							Fail(fmt.Sprintf("prompt[%d] %q returned empty response", i, prompt))
						}
						logStep("[%s]   prompt[%d] PASSED via /v1/chat/completions (tokens=%d)", tc.Name, i, resp.Usage.TotalTokens)
					}
				}
			})

			// ── Phase 8: CLEANUP ───────────────────────────────────
			AfterAll(func() {
				if !shouldRun {
					return
				}
				if testMode == "discover" || noCleanup {
					if noCleanup {
						logStep("[%s] CLEANUP SKIPPED: --no-cleanup flag set (resources left running)", tc.Name)
					}
					return
				}
				if tc.Cleanup {
					logStep("[%s] Phase 8: CLEANUP — Removing deployed resources", tc.Name)
					cleanupMgr.CleanupOne(ctx, tc.Name)
					if tc.Model.Cache != nil && tc.Model.Cache.Enabled {
						dl.Cleanup(ctx, tc)
					}
				}
			})
		})
	}
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

// logStep logs a phase step to both GinkgoWriter and stderr for immediate visibility.
func logStep(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	By(msg)
	fmt.Fprintln(os.Stderr, msg)
}

// resolveTestCases loads filtered test cases for BeforeSuite (profile support, reporter).
func resolveTestCases() ([]*config.TestCase, error) {
	allCases, err := config.LoadTestCasesFromDir(testCaseDir)
	if err != nil {
		return nil, fmt.Errorf("loading test cases from %s: %w", testCaseDir, err)
	}
	if testCaseName != "" {
		return config.FilterTestCasesByNames(allCases, []string{testCaseName}), nil
	}
	if labels != "" {
		labelList := strings.Split(labels, ",")
		for i := range labelList {
			labelList[i] = strings.TrimSpace(labelList[i])
		}
		return config.FilterTestCasesByLabels(allCases, labelList), nil
	}
	if profilePath != "" {
		profile, err := config.LoadProfile(profilePath)
		if err != nil {
			return nil, fmt.Errorf("loading profile: %w", err)
		}
		return config.ResolveProfileTestCases(profile, testCaseDir)
	}
	return allCases, nil
}
