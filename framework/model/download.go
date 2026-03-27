package model

import (
	"context"
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
	kubeconfig   string
	namespace    string
	storageClass string
	platform     string
}

// NewDownloader creates a Downloader.
func NewDownloader(kubeconfig, namespace, storageClass, platform string) *Downloader {
	return &Downloader{
		kubeconfig:   kubeconfig,
		namespace:    namespace,
		storageClass: storageClass,
		platform:     platform,
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
		ok, err := dl.resourceExists(ctx, "pvc", existingPVC)
		if err != nil || !ok {
			result.Status = CacheStatusNotFound
			result.Error = fmt.Errorf("PVC %s not found: %v", existingPVC, err)
			result.Duration = time.Since(start)
			return result
		}
		result.PVCName = existingPVC
		result.Status = CacheStatusReady
		result.Logs = append(result.Logs, fmt.Sprintf("Using existing PVC %s", existingPVC))
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
			result.Duration = time.Since(start)
			return result
		}
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
	err := retry.UntilSuccess(ctx, retry.Options{
		Timeout:  downloadTimeout,
		Interval: 30 * time.Second,
		Name:     fmt.Sprintf("model-download-%s", tc.Name),
	}, func() error {
		succeeded := dl.getField(ctx, "job", jobName, "{.status.succeeded}")
		if succeeded == "1" {
			return nil
		}
		failed := dl.getField(ctx, "job", jobName, "{.status.failed}")
		if failed != "" && failed != "0" && failed != "<none>" {
			logs := dl.getPodLogs(ctx, jobName)
			return fmt.Errorf("download job failed:\n%s", logs)
		}
		return fmt.Errorf("download in progress (succeeded=%s, failed=%s)", succeeded, failed)
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
	dl.kubectl(ctx, "delete", "job", jobName, "--ignore-not-found=true")
}

// CleanupAll removes both the download job and the PVC.
func (dl *Downloader) CleanupAll(ctx context.Context, tc *config.TestCase) {
	pvcName := dl.pvcName(tc)
	jobName := dl.jobName(pvcName)
	dl.kubectl(ctx, "delete", "job", jobName, "--ignore-not-found=true")
	dl.kubectl(ctx, "delete", "pvc", pvcName, "--ignore-not-found=true")
}

func (dl *Downloader) createPVC(ctx context.Context, pvcName string, tc *config.TestCase) error {
	storageSize := "50Gi"
	if tc.Deployment.Resources.GPUs >= 8 {
		storageSize = "500Gi"
	} else if tc.Deployment.Resources.GPUs >= 1 {
		storageSize = "100Gi"
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
    - ReadWriteOnce
  resources:
    requests:
      storage: %s%s`, pvcName, dl.namespace, storageSize, dl.storageClassYAML()))
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile)

	_, err = dl.kubectl(ctx, "apply", "-f", tmpFile)
	return err
}

func (dl *Downloader) createDownloadJob(ctx context.Context, pvcName string, tc *config.TestCase) error {
	hfModel := strings.TrimPrefix(tc.Model.URI, "hf://")
	jobName := dl.jobName(pvcName)

	tmpFile, err := dl.writeTempYAML(fmt.Sprintf(`apiVersion: batch/v1
kind: Job
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: llm-d-conformance-test
    app.kubernetes.io/component: model-download
spec:
  backoffLimit: 2
  ttlSecondsAfterFinished: 86400
  template:
    metadata:
      labels:
        job-name: %s
    spec:
      restartPolicy: OnFailure
      containers:
        - name: download
          image: python:3.11-slim
          command:
            - /bin/bash
            - -c
            - |
              set -e
              echo "Installing huggingface_hub..."
              pip install -q huggingface_hub
              echo "Downloading model: %s"
              python3 -c "
              from huggingface_hub import snapshot_download
              import time
              start = time.time()
              snapshot_download(
                  repo_id='%s',
                  local_dir='/model-cache',
                  local_dir_use_symlinks=False,
              )
              elapsed = time.time() - start
              print(f'Download complete in {elapsed:.0f}s')
              "
              echo "Model download finished successfully"
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
            claimName: %s`, jobName, dl.namespace, jobName, hfModel, hfModel, pvcName))
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile)

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
	return string(output), err
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
	return "model-cache-" + strings.Trim(name, "-")
}

func (dl *Downloader) jobName(pvcName string) string {
	name := "dl-" + pvcName
	if len(name) > 63 {
		name = name[:63]
	}
	return strings.Trim(name, "-")
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
		f.Close()
		os.Remove(path)
		return "", fmt.Errorf("writing temp file: %w", err)
	}
	f.Close()
	return filepath.Clean(path), nil
}
