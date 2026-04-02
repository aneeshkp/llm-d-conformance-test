package model

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aneeshkp/llm-d-conformance-test/framework/config"
	"github.com/aneeshkp/llm-d-conformance-test/framework/retry"
)

// Downloader manages model downloads using kubectl apply with inline manifests.
type Downloader struct {
	kubeconfig          string
	namespace           string
	storageClass        string
	storageSizeOverride string // if set, overrides test case config
	platform            string
	modelSource         string // "pvc" (flat layout) or "pvc-snapshot" (HF cache layout)
	// LogFunc is called with progress messages during download. If nil, progress is silent.
	LogFunc func(format string, args ...interface{})
}

// NewDownloader creates a Downloader.
func NewDownloader(kubeconfig, namespace, storageClass, storageSize, platform, modelSource string) *Downloader {
	return &Downloader{
		kubeconfig:          kubeconfig,
		namespace:           namespace,
		storageClass:        storageClass,
		storageSizeOverride: storageSize,
		platform:            platform,
		modelSource:         modelSource,
	}
}

func (dl *Downloader) logProgress(format string, args ...interface{}) {
	if dl.LogFunc != nil {
		dl.LogFunc(format, args...)
	}
}

// DownloadModel creates a PVC and a download Job to cache the model.
// If the PVC already exists and the download job has completed, it skips the download.
func (dl *Downloader) DownloadModel(ctx context.Context, tc *config.TestCase) *CacheResult {
	start := time.Now()
	pvcName := dl.pvcName(tc)
	jobName := dl.jobName(pvcName)
	result := &CacheResult{
		ModelName: tc.Model.Name,
		PVCName:   pvcName,
	}

	// Skip if model URI is already a PVC
	if strings.HasPrefix(tc.Model.URI, "pvc://") {
		existingPVC := strings.TrimPrefix(tc.Model.URI, "pvc://")
		// Strip subpath if present (pvc://name/subpath → name)
		if idx := strings.Index(existingPVC, "/"); idx >= 0 {
			existingPVC = existingPVC[:idx]
		}
		dl.logProgress("[%s] Model URI is pvc://, checking PVC %s exists", tc.Name, existingPVC)
		ok, err := dl.resourceExists(ctx, "pvc", existingPVC)
		if err != nil || !ok {
			result.Status = CacheStatusNotFound
			result.Error = fmt.Errorf("PVC %s not found: %w", existingPVC, err)
			result.Duration = time.Since(start)
			return result
		}
		result.PVCName = existingPVC
		result.Status = CacheStatusReady
		result.Logs = append(result.Logs, fmt.Sprintf("Using existing PVC %s", existingPVC))
		dl.logProgress("[%s] SKIP: PVC %s already exists", tc.Name, existingPVC)
		result.Duration = time.Since(start)
		return result
	}

	// Check if download job already completed
	ok, _ := dl.resourceExists(ctx, "job", jobName)
	if ok {
		succeeded := dl.getField(ctx, "job", jobName, "{.status.succeeded}")
		if succeeded == "1" {
			result.Status = CacheStatusReady
			result.Logs = append(result.Logs, fmt.Sprintf("Model already cached in PVC %s (download job completed)", pvcName))
			dl.logProgress("[%s] SKIP: model already cached in PVC %s (job completed)", tc.Name, pvcName)
			result.Duration = time.Since(start)
			return result
		}
	} else {
		// Job gone (TTL expired or deleted). Check if PVC exists and has a model subdir.
		pvcExists, _ := dl.resourceExists(ctx, "pvc", pvcName)
		if pvcExists {
			result.Status = CacheStatusReady
			result.PVCName = pvcName
			result.Logs = append(result.Logs, fmt.Sprintf("PVC %s exists with model data (download job was cleaned up)", pvcName))
			dl.logProgress("[%s] SKIP: PVC %s already has model data", tc.Name, pvcName)
			result.Duration = time.Since(start)
			return result
		}
	}

	// Ensure namespace exists before creating resources
	if err := dl.ensureNamespace(ctx); err != nil {
		result.Status = CacheStatusFailed
		result.Error = fmt.Errorf("ensuring namespace %s exists: %w", dl.namespace, err)
		result.Duration = time.Since(start)
		return result
	}

	// Create PVC if needed
	pvcOK, _ := dl.resourceExists(ctx, "pvc", pvcName)
	if !pvcOK {
		result.Logs = append(result.Logs, fmt.Sprintf("Creating PVC %s", pvcName))
		if err := dl.createPVC(ctx, pvcName, tc); err != nil {
			result.Status = CacheStatusFailed
			result.Error = fmt.Errorf("creating PVC: %w", err)
			result.Duration = time.Since(start)
			return result
		}
	}

	// Create download job if needed
	if !ok {
		result.Logs = append(result.Logs, fmt.Sprintf("Starting download job %s for model %s", jobName, tc.Model.Name))
		if err := dl.createDownloadJob(ctx, pvcName, tc); err != nil {
			result.Status = CacheStatusFailed
			result.Error = fmt.Errorf("creating download job: %w", err)
			result.Duration = time.Since(start)
			return result
		}
	}

	// Wait for completion
	result.Status = CacheStatusDownloading
	downloadTimeout := 90 * time.Minute
	if tc.Deployment.ReadyTimeout.Duration > downloadTimeout {
		downloadTimeout = tc.Deployment.ReadyTimeout.Duration
	}

	result.Logs = append(result.Logs, fmt.Sprintf("Waiting up to %s for download", downloadTimeout))
	dl.logProgress("[%s] Downloading model %s (timeout=%s)...", tc.Name, tc.Model.Name, downloadTimeout)
	lastLogLen := 0
	err := retry.UntilSuccess(ctx, retry.Options{
		Timeout:  downloadTimeout,
		Interval: 15 * time.Second,
		Name:     fmt.Sprintf("model-download-%s", tc.Name),
	}, func() error {
		// Check PVC status — fail fast if provisioning failed
		pvcPhase, _ := dl.kubectl(ctx, "get", "pvc", pvcName, "-o", "jsonpath={.status.phase}")
		if strings.TrimSpace(pvcPhase) == "" || strings.TrimSpace(pvcPhase) == "Pending" {
			// Check for provisioning errors
			pvcEvents, _ := dl.kubectl(ctx, "get", "events",
				"--field-selector", fmt.Sprintf("involvedObject.name=%s,reason=ProvisioningFailed", pvcName),
				"-o", "jsonpath={.items[0].message}")
			if pvcErr := strings.TrimSpace(pvcEvents); pvcErr != "" {
				return fmt.Errorf("PVC %s provisioning failed: %s", pvcName, pvcErr)
			}
		}

		succeeded := dl.getField(ctx, "job", jobName, "{.status.succeeded}")
		if succeeded == "1" {
			return nil
		}
		failed := dl.getField(ctx, "job", jobName, "{.status.failed}")
		if failed != "" && failed != "0" && failed != "<none>" {
			logs := dl.getPodLogs(ctx, jobName)
			return fmt.Errorf("download job failed:\n%s", logs)
		}
		// Show pod status
		podStatus, _ := dl.kubectl(ctx, "get", "pods", "-l", fmt.Sprintf("job-name=%s", jobName),
			"-o", "jsonpath={range .items[*]}{.metadata.name} {.status.phase}{end}")
		podStatus = strings.TrimSpace(podStatus)

		// Show download progress from pod logs
		logs := dl.getPodLogs(ctx, jobName)
		if len(logs) > 0 && len(logs) != lastLogLen {
			lastLogLen = len(logs)
			lines := strings.Split(strings.TrimSpace(logs), "\n")
			tailStart := 0
			if len(lines) > 3 {
				tailStart = len(lines) - 3
			}
			for _, line := range lines[tailStart:] {
				if line != "" {
					dl.logProgress("  [download] %s", line)
				}
			}
		}
		dl.logProgress("[%s] Download in progress (pod=%s, elapsed=%s)", tc.Name, podStatus, time.Since(start).Truncate(time.Second))
		return fmt.Errorf("download in progress")
	})

	if err != nil {
		result.Status = CacheStatusFailed
		result.Error = err
		result.Logs = append(result.Logs, fmt.Sprintf("Download failed: %v", err))
		result.Duration = time.Since(start)
		return result
	}

	result.Status = CacheStatusReady
	result.Logs = append(result.Logs, fmt.Sprintf("Model %s downloaded and cached in PVC %s", tc.Model.Name, pvcName))
	result.Duration = time.Since(start)
	return result
}

// Cleanup removes the download job (but keeps the PVC for reuse).
func (dl *Downloader) Cleanup(ctx context.Context, tc *config.TestCase) {
	pvcName := dl.pvcName(tc)
	jobName := dl.jobName(pvcName)
	_, _ = dl.kubectl(ctx, "delete", "job", jobName, "--ignore-not-found=true")
}

// CleanupAll removes both the download job and the PVC.
func (dl *Downloader) CleanupAll(ctx context.Context, tc *config.TestCase) {
	pvcName := dl.pvcName(tc)
	jobName := dl.jobName(pvcName)
	_, _ = dl.kubectl(ctx, "delete", "job", jobName, "--ignore-not-found=true")
	_, _ = dl.kubectl(ctx, "delete", "pvc", pvcName, "--ignore-not-found=true")
}

func (dl *Downloader) createPVC(ctx context.Context, pvcName string, tc *config.TestCase) error {
	storageSize := "50Gi"
	switch {
	case dl.storageSizeOverride != "":
		storageSize = dl.storageSizeOverride
	case tc.Model.Cache != nil && tc.Model.Cache.StorageSize != "":
		storageSize = tc.Model.Cache.StorageSize
	case tc.Deployment.Resources.GPUs >= 8:
		storageSize = "500Gi"
	case tc.Deployment.Resources.GPUs >= 1:
		storageSize = "100Gi"
	}

	// Use ReadWriteOnce by default (works with Azure Disk).
	// Use ReadWriteMany when a RWX-capable StorageClass is explicitly provided.
	accessMode := "ReadWriteOnce"
	if dl.storageClass != "" && dl.storageClass != "default" && dl.storageClass != "managed-csi" {
		accessMode = "ReadWriteMany"
	}

	tmpFile, err := dl.writeTempYAML(fmt.Sprintf(`apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: llm-d-conformance-test
    app.kubernetes.io/component: model-cache
spec:
  accessModes:
    - %s
  resources:
    requests:
      storage: %s%s`, pvcName, dl.namespace, accessMode, storageSize, dl.storageClassYAML()))
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpFile) }()

	_, err = dl.kubectl(ctx, "apply", "-f", tmpFile)
	return err
}

func (dl *Downloader) createDownloadJob(ctx context.Context, pvcName string, tc *config.TestCase) error {
	if dl.modelSource == "pvc-snapshot" {
		return dl.createSnapshotDownloadJob(ctx, pvcName, tc)
	}
	return dl.createFlatDownloadJob(ctx, pvcName, tc)
}

// createFlatDownloadJob downloads model to a flat layout at /mnt/models/<ModelName>/
// Used with pvc://<name>/<subpath> URIs — KServe mounts subpath at /mnt/models.
func (dl *Downloader) createFlatDownloadJob(ctx context.Context, pvcName string, tc *config.TestCase) error {
	hfModel := strings.TrimPrefix(tc.Model.URI, "hf://")
	jobName := dl.jobName(pvcName)

	storageInitImage := dl.getStorageInitImage(ctx)
	dl.logProgress("[%s] Using storage initializer image: %s", tc.Name, storageInitImage)

	modelSubdir := hfModel
	if idx := strings.LastIndex(hfModel, "/"); idx >= 0 {
		modelSubdir = hfModel[idx+1:]
	}
	downloadTarget := fmt.Sprintf("/mnt/models/%s", modelSubdir)
	dl.logProgress("[%s] Download target: %s (flat layout)", tc.Name, downloadTarget)

	tmpFile, err := dl.writeTempYAML(fmt.Sprintf(`apiVersion: batch/v1
kind: Job
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: llm-d-conformance-test
    app.kubernetes.io/component: model-download
spec:
  backoffLimit: 3
  ttlSecondsAfterFinished: 604800
  template:
    metadata:
      labels:
        job-name: %s
    spec:
      restartPolicy: OnFailure
      securityContext:
        fsGroup: 1000
      containers:
        - name: storage-initializer
          image: %s
          args:
            - "hf://%s"
            - "%s"
          env:
            - name: HF_HOME
              value: /tmp/hf
            - name: HF_HUB_ENABLE_HF_TRANSFER
              value: "1"
            - name: HF_HUB_DOWNLOAD_CONCURRENCY
              value: "8"
            - name: HF_HUB_DISABLE_TELEMETRY
              value: "1"
          volumeMounts:
            - name: model-storage
              mountPath: /mnt/models
            - name: tmp
              mountPath: /tmp
          resources:
            requests:
              cpu: "1"
              memory: 4Gi
            limits:
              cpu: "2"
              memory: 20Gi
      volumes:
        - name: model-storage
          persistentVolumeClaim:
            claimName: %s
        - name: tmp
          emptyDir: {}`, jobName, dl.namespace, jobName, storageInitImage, hfModel, downloadTarget, pvcName))
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpFile) }()

	_, err = dl.kubectl(ctx, "apply", "-f", tmpFile)
	return err
}

// createSnapshotDownloadJob downloads model using HF cache/snapshot layout on PVC.
// Sets HF_HUB_CACHE=/mnt/models so snapshot_download() uses the HF cache directory structure.
// After download, the model lives at: /mnt/models/models--Org--Model/snapshots/<hash>/
// The manifest uses pvc://pvc-name/models--Org--Model/snapshots/<hash> as subpath (Workaround A).
func (dl *Downloader) createSnapshotDownloadJob(ctx context.Context, pvcName string, tc *config.TestCase) error {
	hfModel := strings.TrimPrefix(tc.Model.URI, "hf://")
	jobName := dl.jobName(pvcName)

	storageInitImage := dl.getStorageInitImage(ctx)
	dl.logProgress("[%s] Using storage initializer image: %s", tc.Name, storageInitImage)
	dl.logProgress("[%s] Download target: /mnt/models (HF snapshot/cache layout)", tc.Name)

	// Download using snapshot_download() without local_dir — HF_HUB_CACHE controls location.
	tmpFile, err := dl.writeTempYAML(fmt.Sprintf(`apiVersion: batch/v1
kind: Job
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: llm-d-conformance-test
    app.kubernetes.io/component: model-download
spec:
  backoffLimit: 3
  ttlSecondsAfterFinished: 604800
  template:
    metadata:
      labels:
        job-name: %s
    spec:
      restartPolicy: OnFailure
      securityContext:
        fsGroup: 1000
      containers:
        - name: storage-initializer
          image: %s
          command:
            - python3
            - -c
            - "from huggingface_hub import snapshot_download; snapshot_download(repo_id='%s')"
          env:
            - name: HF_HUB_CACHE
              value: /mnt/models
            - name: HF_HUB_ENABLE_HF_TRANSFER
              value: "1"
            - name: HF_HUB_DOWNLOAD_CONCURRENCY
              value: "8"
            - name: HF_HUB_DISABLE_TELEMETRY
              value: "1"
          volumeMounts:
            - name: model-storage
              mountPath: /mnt/models
            - name: tmp
              mountPath: /tmp
          resources:
            requests:
              cpu: "1"
              memory: 4Gi
            limits:
              cpu: "2"
              memory: 20Gi
      volumes:
        - name: model-storage
          persistentVolumeClaim:
            claimName: %s
        - name: tmp
          emptyDir: {}`, jobName, dl.namespace, jobName, storageInitImage, hfModel, pvcName))
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpFile) }()

	_, err = dl.kubectl(ctx, "apply", "-f", tmpFile)
	return err
}

func (dl *Downloader) kubectl(ctx context.Context, args ...string) (string, error) {
	cmdArgs := make([]string, 0, len(args)+4)
	if dl.kubeconfig != "" {
		cmdArgs = append(cmdArgs, "--kubeconfig", dl.kubeconfig)
	}
	cmdArgs = append(cmdArgs, "-n", dl.namespace)
	cmdArgs = append(cmdArgs, args...)

	bin := "kubectl"
	if dl.platform == "ocp" {
		if _, err := exec.LookPath("oc"); err == nil {
			bin = "oc"
		}
	}
	cmd := exec.CommandContext(ctx, bin, cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%w\n%s", err, string(output))
	}
	return string(output), nil
}

func (dl *Downloader) ensureNamespace(ctx context.Context) error {
	_, err := dl.kubectl(ctx, "get", "namespace", dl.namespace)
	if err == nil {
		return nil
	}
	// Namespace doesn't exist, create it (use kubectl without -n flag)
	bin := "kubectl"
	if dl.platform == "ocp" {
		if _, lookErr := exec.LookPath("oc"); lookErr == nil {
			bin = "oc"
		}
	}
	args := []string{}
	if dl.kubeconfig != "" {
		args = append(args, "--kubeconfig", dl.kubeconfig)
	}
	args = append(args, "create", "namespace", dl.namespace)
	cmd := exec.CommandContext(ctx, bin, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, string(output))
	}
	return nil
}

func (dl *Downloader) resourceExists(ctx context.Context, kind, name string) (bool, error) {
	output, err := dl.kubectl(ctx, "get", kind, name, "--ignore-not-found=true")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) != "", nil
}

func (dl *Downloader) getField(ctx context.Context, kind, name, jsonpath string) string {
	output, err := dl.kubectl(ctx, "get", kind, name, "-o", fmt.Sprintf("jsonpath=%s", jsonpath))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

func (dl *Downloader) getPodLogs(ctx context.Context, jobName string) string {
	output, _ := dl.kubectl(ctx, "logs", "-l", fmt.Sprintf("job-name=%s", jobName), "--tail=30")
	// Filter out kubectl noise when pod has no logs yet
	if strings.Contains(output, "No resources found") || strings.Contains(output, "is waiting to start") {
		return ""
	}
	return output
}

func (dl *Downloader) pvcName(tc *config.TestCase) string {
	name := strings.ToLower(tc.Model.Name)
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, ".", "-")
	if len(name) > 50 {
		name = name[:50]
	}
	prefix := "model-cache-"
	if dl.modelSource == "pvc-snapshot" {
		prefix = "model-snapshot-"
	}
	return prefix + strings.Trim(name, "-")
}

func (dl *Downloader) jobName(pvcName string) string {
	name := "dl-" + pvcName
	if len(name) > 63 {
		name = name[:63]
	}
	return strings.Trim(name, "-")
}

// getStorageInitImage auto-detects the KServe storage initializer image from the cluster.
// Falls back to a default if the configmap is not found.
func (dl *Downloader) getStorageInitImage(ctx context.Context) string {
	// Try to read from KServe configmap (works for both opendatahub and kserve namespaces)
	for _, ns := range []string{"opendatahub", "kserve", "knative-serving"} {
		bin := "kubectl"
		args := []string{}
		if dl.kubeconfig != "" {
			args = append(args, "--kubeconfig", dl.kubeconfig)
		}
		args = append(args, "get", "configmap", "inferenceservice-config", "-n", ns,
			"-o", "jsonpath={.data.storageInitializer}")
		cmd := exec.CommandContext(ctx, bin, args...)
		output, err := cmd.Output()
		if err != nil || len(output) == 0 {
			continue
		}
		var cfg struct {
			Image string `json:"image"`
		}
		if err := json.Unmarshal(output, &cfg); err == nil && cfg.Image != "" {
			return cfg.Image
		}
	}
	// Fallback
	return "quay.io/opendatahub/kserve-storage-initializer:v0.15-latest"
}

// PVCModelURI returns the pvc:// URI for a cached model.
// For flat layout: pvc://pvc-name/Qwen2.5-7B-Instruct (with subpath)
// For snapshot layout: pvc://pvc-name (no subpath — vLLM resolves via HF_HUB_CACHE + --model flag)
func (dl *Downloader) PVCModelURI(tc *config.TestCase) string {
	pvcName := dl.pvcName(tc)
	if dl.modelSource == "pvc-snapshot" {
		return fmt.Sprintf("pvc://%s", pvcName)
	}
	modelName := tc.Model.Name
	if idx := strings.LastIndex(modelName, "/"); idx >= 0 {
		modelName = modelName[idx+1:]
	}
	return fmt.Sprintf("pvc://%s/%s", pvcName, modelName)
}

// ResolveSnapshotPath finds the snapshot subpath on the PVC for HF cache layout.
// Returns e.g. "models--Qwen--Qwen2.5-7B-Instruct/snapshots/74c44f17ba..."
func (dl *Downloader) ResolveSnapshotPath(ctx context.Context, tc *config.TestCase) (string, error) {
	pvcName := dl.pvcName(tc)
	modelCacheDir := "models--" + strings.ReplaceAll(tc.Model.Name, "/", "--")

	// Read refs/main from the PVC to get the snapshot hash
	// Use a one-shot pod since we can't exec into the download job (it's completed)
	checkName := "snapshot-ref-" + pvcName
	if len(checkName) > 63 {
		checkName = checkName[:63]
	}
	refsPath := fmt.Sprintf("/mnt/models/%s/refs/main", modelCacheDir)

	// Delete any leftover check pod from a previous run
	_, _ = dl.kubectl(ctx, "delete", "pod", checkName, "--ignore-not-found=true")

	out, err := dl.kubectl(ctx, "run", checkName, "--rm", "-i", "--restart=Never",
		"--image=busybox",
		"--overrides", fmt.Sprintf(`{"spec":{"containers":[{"name":"check","image":"busybox","command":["cat","%s"],"volumeMounts":[{"name":"pvc","mountPath":"/mnt/models"}]}],"volumes":[{"name":"pvc","persistentVolumeClaim":{"claimName":"%s"}}]}}`, refsPath, pvcName)) //nolint:gocritic // JSON template requires literal quotes
	if err != nil {
		return "", fmt.Errorf("could not read snapshot ref: %w\n%s", err, out)
	}

	// Extract the commit hash — a 40-char hex string.
	// kubectl run --rm -i merges stdout/stderr, so the hash may be glued to
	// noise like 'pod "..." deleted'. We scan for any hex substring >= 20 chars.
	hash := ""
	current := strings.Builder{}
	for _, ch := range out {
		if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F') {
			current.WriteRune(ch)
		} else {
			if current.Len() >= 20 {
				hash = current.String()
				break
			}
			current.Reset()
		}
	}
	if hash == "" && current.Len() >= 20 {
		hash = current.String()
	}
	if hash == "" {
		return "", fmt.Errorf("could not find snapshot hash in refs/main output: %q", out)
	}

	return fmt.Sprintf("%s/snapshots/%s", modelCacheDir, hash), nil
}

func (dl *Downloader) storageClassYAML() string {
	if dl.storageClass != "" {
		return fmt.Sprintf("\n  storageClassName: %s", dl.storageClass)
	}
	return ""
}

func (dl *Downloader) writeTempYAML(content string) (string, error) {
	tmpDir := os.TempDir()
	f, err := os.CreateTemp(tmpDir, "llm-d-*.yaml")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	path := f.Name()
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("writing temp file: %w", err)
	}
	_ = f.Close()
	return filepath.Clean(path), nil
}
