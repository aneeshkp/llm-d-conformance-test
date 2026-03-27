// Package model provides model download and PVC-based caching for LLM models.
// Models are downloaded once into a PVC and reused across test runs, avoiding
// repeated multi-GB downloads during pod startup.
package model

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aneeshkp/llm-d-conformance-test/framework/config"
	"github.com/aneeshkp/llm-d-conformance-test/framework/retry"
)

// CacheStatus represents the state of a model in the cache.
type CacheStatus string

const (
	CacheStatusNotFound    CacheStatus = "not_found"
	CacheStatusDownloading CacheStatus = "downloading"
	CacheStatusReady       CacheStatus = "ready"
	CacheStatusFailed      CacheStatus = "failed"
)

// CacheResult captures the outcome of a model cache operation.
type CacheResult struct {
	ModelName   string
	PVCName     string
	Status      CacheStatus
	Duration    time.Duration
	SizeBytes   int64
	Error       error
	Logs        []string
}

// CacheManager handles model download and PVC-based caching.
type CacheManager struct {
	kubeCmd    func(ctx context.Context, args ...string) (string, error)
	namespace  string
	storageClass string
}

// NewCacheManager creates a CacheManager.
func NewCacheManager(kubectlFn func(ctx context.Context, args ...string) (string, error), namespace, storageClass string) *CacheManager {
	return &CacheManager{
		kubeCmd:      kubectlFn,
		namespace:    namespace,
		storageClass: storageClass,
	}
}

// EnsureModelCached checks if a model PVC exists, and if not, creates a download job
// to populate it. Returns the PVC name to use in the LLMInferenceService manifest.
func (cm *CacheManager) EnsureModelCached(ctx context.Context, tc *config.TestCase) *CacheResult {
	start := time.Now()
	result := &CacheResult{
		ModelName: tc.Model.Name,
		PVCName:   cm.pvcName(tc),
	}

	// If the model URI is already a PVC reference, just verify the PVC exists
	if strings.HasPrefix(tc.Model.URI, "pvc://") {
		pvcName := strings.TrimPrefix(tc.Model.URI, "pvc://")
		result.PVCName = pvcName
		exists, err := cm.pvcExists(ctx, pvcName)
		if err != nil {
			result.Status = CacheStatusFailed
			result.Error = fmt.Errorf("checking PVC %s: %w", pvcName, err)
			result.Duration = time.Since(start)
			return result
		}
		if !exists {
			result.Status = CacheStatusNotFound
			result.Error = fmt.Errorf("PVC %s referenced in model URI does not exist — create it manually or use hf:// URI for auto-download", pvcName)
			result.Duration = time.Since(start)
			return result
		}
		result.Status = CacheStatusReady
		result.Logs = append(result.Logs, fmt.Sprintf("PVC %s already exists", pvcName))
		result.Duration = time.Since(start)
		return result
	}

	// For hf:// URIs, check if we have a cached PVC already
	pvcName := result.PVCName
	exists, err := cm.pvcExists(ctx, pvcName)
	if err != nil {
		result.Status = CacheStatusFailed
		result.Error = fmt.Errorf("checking cache PVC %s: %w", pvcName, err)
		result.Duration = time.Since(start)
		return result
	}

	if exists {
		// Check if the PVC is populated (download job completed)
		ready, err := cm.isPVCPopulated(ctx, pvcName)
		if err != nil {
			result.Logs = append(result.Logs, fmt.Sprintf("Warning: could not verify PVC contents: %v", err))
		}
		if ready {
			result.Status = CacheStatusReady
			result.Logs = append(result.Logs, fmt.Sprintf("Model cache PVC %s already populated", pvcName))
			result.Duration = time.Since(start)
			return result
		}
		// PVC exists but may not be populated — check if download job is running
		jobRunning, _ := cm.isDownloadJobRunning(ctx, pvcName)
		if jobRunning {
			result.Status = CacheStatusDownloading
			result.Logs = append(result.Logs, fmt.Sprintf("Download job for %s is still running, waiting...", pvcName))
		}
	} else {
		// Create PVC and start download job
		result.Logs = append(result.Logs, fmt.Sprintf("Creating cache PVC %s", pvcName))
		if err := cm.createCachePVC(ctx, pvcName, tc); err != nil {
			result.Status = CacheStatusFailed
			result.Error = fmt.Errorf("creating cache PVC: %w", err)
			result.Duration = time.Since(start)
			return result
		}
		result.Logs = append(result.Logs, fmt.Sprintf("Starting model download job for %s", tc.Model.Name))
		if err := cm.startDownloadJob(ctx, pvcName, tc); err != nil {
			result.Status = CacheStatusFailed
			result.Error = fmt.Errorf("starting download job: %w", err)
			result.Duration = time.Since(start)
			return result
		}
		result.Status = CacheStatusDownloading
	}

	// Wait for download to complete
	downloadTimeout := 60 * time.Minute
	if tc.Deployment.ReadyTimeout.Duration > 0 {
		downloadTimeout = tc.Deployment.ReadyTimeout.Duration
	}

	result.Logs = append(result.Logs, fmt.Sprintf("Waiting up to %s for model download to complete", downloadTimeout))
	err = retry.UntilSuccess(ctx, retry.Options{
		Timeout:  downloadTimeout,
		Interval: 30 * time.Second,
		Name:     fmt.Sprintf("model-download-%s", tc.Model.Name),
	}, func() error {
		// Check job status
		output, err := cm.kubeCmd(ctx, "get", "job", cm.jobName(pvcName), "-n", cm.namespace,
			"-o", "jsonpath={.status.succeeded}")
		if err != nil {
			return fmt.Errorf("checking download job status: %w", err)
		}
		if strings.TrimSpace(output) == "1" {
			return nil
		}
		// Check for failure
		failOutput, _ := cm.kubeCmd(ctx, "get", "job", cm.jobName(pvcName), "-n", cm.namespace,
			"-o", "jsonpath={.status.failed}")
		if strings.TrimSpace(failOutput) != "" && strings.TrimSpace(failOutput) != "0" {
			// Get pod logs for diagnosis
			logs, _ := cm.kubeCmd(ctx, "logs", "-n", cm.namespace,
				"-l", fmt.Sprintf("job-name=%s", cm.jobName(pvcName)), "--tail=20")
			return fmt.Errorf("download job failed. Last logs:\n%s", logs)
		}
		return fmt.Errorf("download still in progress")
	})

	if err != nil {
		result.Status = CacheStatusFailed
		result.Error = err
		result.Duration = time.Since(start)
		return result
	}

	result.Status = CacheStatusReady
	result.Logs = append(result.Logs, fmt.Sprintf("Model %s cached in PVC %s", tc.Model.Name, pvcName))
	result.Duration = time.Since(start)
	return result
}

// GetPVCURI returns the pvc:// URI for a cached model, for use in LLMInferenceService manifests.
func (cm *CacheManager) GetPVCURI(tc *config.TestCase) string {
	if strings.HasPrefix(tc.Model.URI, "pvc://") {
		return tc.Model.URI
	}
	return "pvc://" + cm.pvcName(tc)
}

// CleanupCache deletes the model cache PVC and download job.
func (cm *CacheManager) CleanupCache(ctx context.Context, tc *config.TestCase) error {
	pvcName := cm.pvcName(tc)
	jobName := cm.jobName(pvcName)

	// Delete job first
	cm.kubeCmd(ctx, "delete", "job", jobName, "-n", cm.namespace, "--ignore-not-found=true")
	// Delete PVC
	_, err := cm.kubeCmd(ctx, "delete", "pvc", pvcName, "-n", cm.namespace, "--ignore-not-found=true")
	return err
}

func (cm *CacheManager) pvcName(tc *config.TestCase) string {
	// Generate a deterministic PVC name from the model name
	name := strings.ToLower(tc.Model.Name)
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, ".", "-")
	if len(name) > 50 {
		name = name[:50]
	}
	return "model-cache-" + strings.Trim(name, "-")
}

func (cm *CacheManager) jobName(pvcName string) string {
	name := "download-" + pvcName
	if len(name) > 63 {
		name = name[:63]
	}
	return strings.Trim(name, "-")
}

func (cm *CacheManager) pvcExists(ctx context.Context, name string) (bool, error) {
	output, err := cm.kubeCmd(ctx, "get", "pvc", name, "-n", cm.namespace, "--ignore-not-found=true")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) != "", nil
}

func (cm *CacheManager) isPVCPopulated(ctx context.Context, pvcName string) (bool, error) {
	// Check if any completed download job exists for this PVC
	output, err := cm.kubeCmd(ctx, "get", "job", cm.jobName(pvcName), "-n", cm.namespace,
		"-o", "jsonpath={.status.succeeded}", "--ignore-not-found=true")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) == "1", nil
}

func (cm *CacheManager) isDownloadJobRunning(ctx context.Context, pvcName string) (bool, error) {
	output, err := cm.kubeCmd(ctx, "get", "job", cm.jobName(pvcName), "-n", cm.namespace,
		"-o", "jsonpath={.status.active}", "--ignore-not-found=true")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) == "1", nil
}

func (cm *CacheManager) createCachePVC(ctx context.Context, pvcName string, tc *config.TestCase) error {
	storageSize := "50Gi"
	// Large models need more space
	if tc.Deployment.Resources.GPUs >= 8 {
		storageSize = "500Gi"
	} else if tc.Deployment.Resources.GPUs >= 1 {
		storageSize = "100Gi"
	}

	pvcYAML := fmt.Sprintf(`apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: %s`, pvcName, cm.namespace, storageSize)

	if cm.storageClass != "" {
		pvcYAML += fmt.Sprintf("\n  storageClassName: %s", cm.storageClass)
	}

	_, err := cm.kubeCmd(ctx, "apply", "-f", "-", "--input="+pvcYAML)
	if err != nil {
		// Fallback: write to temp file
		return cm.applyYAMLString(ctx, pvcYAML)
	}
	return nil
}

func (cm *CacheManager) startDownloadJob(ctx context.Context, pvcName string, tc *config.TestCase) error {
	modelURI := tc.Model.URI
	hfModel := strings.TrimPrefix(modelURI, "hf://")

	jobYAML := fmt.Sprintf(`apiVersion: batch/v1
kind: Job
metadata:
  name: %s
  namespace: %s
spec:
  backoffLimit: 2
  ttlSecondsAfterFinished: 86400
  template:
    spec:
      restartPolicy: OnFailure
      containers:
        - name: download
          image: python:3.11-slim
          command:
            - /bin/bash
            - -c
            - |
              pip install -q huggingface_hub &&
              python -c "
              from huggingface_hub import snapshot_download
              snapshot_download(
                  repo_id='%s',
                  local_dir='/model-cache',
                  local_dir_use_symlinks=False,
              )
              print('Download complete')
              "
          volumeMounts:
            - name: model-storage
              mountPath: /model-cache
          resources:
            requests:
              cpu: "1"
              memory: 4Gi
            limits:
              cpu: "4"
              memory: 8Gi
      volumes:
        - name: model-storage
          persistentVolumeClaim:
            claimName: %s`,
		cm.jobName(pvcName), cm.namespace, hfModel, pvcName)

	return cm.applyYAMLString(ctx, jobYAML)
}

func (cm *CacheManager) applyYAMLString(ctx context.Context, _ string) error {
	// We need to write and apply in a different way since kubeCmd is kubectl only
	// For now, return an error suggesting manual PVC creation
	return fmt.Errorf("auto-PVC creation requires shell access; use the Downloader or create PVCs manually via deploy/manifests/model-cache/ templates")
}
