// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package verifiers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
)

const (
	azureDiskCSIProvisioner = "disk.csi.azure.com"
	azureFileCSIProvisioner = "file.csi.azure.com"

	csiVolumeMountPath    = "/mnt/data"
	csiUIDRangeAnnotation = "openshift.io/sa.scc.uid-range"
	csiUIDRangeTimeout    = 5 * time.Minute
	csiPVCName            = "csi-e2e-pvc"
	csiPodName            = "csi-e2e-pod"
	csiServiceAccountName = "csi-e2e"
	csiDebugImage         = "mcr.microsoft.com/azurelinux/distroless/debug:3.0"
)

// podFailedError is a terminal error returned by checkReady when the pod
// transitions to PodFailed. It stops the poll loop immediately instead of
// retrying until timeout.
type podFailedError struct {
	msg string
}

func (e *podFailedError) Error() string { return e.msg }

// verifyCSIPersistentVolume is the HostedClusterVerifier implementation shared
// by both Disk and File CSI checks. It discovers a StorageClass by provisioner,
// creates a PVC and a pod that does a write-read round-trip on the volume, then
// asserts the output proves the data survived the round-trip.
type verifyCSIPersistentVolume struct {
	nsGenerateName string
	provisioner    string
	accessMode     corev1.PersistentVolumeAccessMode
	timeout        time.Duration
}

// csiPollState tracks mutable polling state for delta-only phase-transition
// logging and timeout diagnostics.
type csiPollState struct {
	kubeClient   kubernetes.Interface
	provisioner  string
	storageClass string
	namespace    string
	prevPodPhase corev1.PodPhase
	prevPVCPhase corev1.PersistentVolumeClaimPhase
}

func (v verifyCSIPersistentVolume) Name() string {
	return fmt.Sprintf("VerifyCSIPersistentVolume(%s)", v.provisioner)
}

// ---------------------------------------------------------------------------
// Verify orchestrates the full CSI volume check:
//  1. Discover a StorageClass matching the provisioner
//  2. Create an isolated namespace with SA and OpenShift UID range
//  3. Create a PVC
//  4. Create a pod that writes a random payload to the volume and prints a
//     separate success token only after a read-back compare succeeds
//  5. Poll until PVC is Bound and pod is Succeeded (abort on PodFailed)
//  6. Assert pod logs equal the success token
// ---------------------------------------------------------------------------
func (v verifyCSIPersistentVolume) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	sc, err := v.findStorageClass(ctx, kubeClient)
	if err != nil {
		return err
	}
	ginkgo.GinkgoWriter.Printf("[%s] using StorageClass %q (provisioner %s)\n", v.Name(), sc.Name, v.provisioner)

	nsName, fsGroup, err := v.setupTestNamespace(ctx, kubeClient)
	if err != nil {
		return err
	}
	defer func() {
		delCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = kubeClient.CoreV1().Namespaces().Delete(delCtx, nsName, metav1.DeleteOptions{})
	}()

	if _, err := kubeClient.CoreV1().PersistentVolumeClaims(nsName).Create(ctx, v.buildPVC(nsName, sc.Name), metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create PVC %s/%s: %w", nsName, csiPVCName, err)
	}

	payload := rand.String(16)
	successToken := rand.String(16)
	if _, err := kubeClient.CoreV1().Pods(nsName).Create(ctx, v.buildPod(nsName, fsGroup, payload, successToken), metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create pod %s/%s: %w", nsName, csiPodName, err)
	}

	state := &csiPollState{
		kubeClient:   kubeClient,
		provisioner:  v.provisioner,
		storageClass: sc.Name,
		namespace:    nsName,
	}
	if err := v.pollUntilPodSucceeded(ctx, state); err != nil {
		return err
	}

	return v.verifyPodOutput(ctx, kubeClient, nsName, successToken)
}

// ---------------------------------------------------------------------------
// Resource construction helpers
// ---------------------------------------------------------------------------

// setupTestNamespace creates an ephemeral namespace with a service account and
// waits for the OpenShift cluster-policy-controller to annotate the namespace
// with a UID range (required for pods running under the restricted SCC).
func (v verifyCSIPersistentVolume) setupTestNamespace(ctx context.Context, kubeClient kubernetes.Interface) (string, *int64, error) {
	ns, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: v.nsGenerateName},
	}, metav1.CreateOptions{})
	if err != nil {
		return "", nil, fmt.Errorf("failed to create namespace: %w", err)
	}

	if _, err := kubeClient.CoreV1().ServiceAccounts(ns.Name).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: csiServiceAccountName},
	}, metav1.CreateOptions{}); err != nil {
		return "", nil, fmt.Errorf("failed to create service account %s/%s: %w", ns.Name, csiServiceAccountName, err)
	}

	fsGroup, err := waitForUIDRange(ctx, kubeClient, ns.Name)
	if err != nil {
		return "", nil, err
	}
	return ns.Name, fsGroup, nil
}

func (v verifyCSIPersistentVolume) buildPVC(namespace, storageClassName string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      csiPVCName,
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{v.accessMode},
			StorageClassName: to.Ptr(storageClassName),
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
		},
	}
}

// buildPod returns a one-shot pod that writes payload to the CSI volume and
// prints successToken only after verifying the data survived a read-back. The
// payload and successToken are injected via environment variables so they never
// appear in the command string itself — preventing a false-positive log match.
func (v verifyCSIPersistentVolume) buildPod(namespace string, fsGroup *int64, payload, successToken string) *corev1.Pod {
	automountSAToken := false
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      csiPodName,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			ServiceAccountName:           csiServiceAccountName,
			AutomountServiceAccountToken: &automountSAToken,
			SecurityContext: &corev1.PodSecurityContext{
				FSGroup: fsGroup,
			},
			Volumes: []corev1.Volume{{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: csiPVCName,
					},
				},
			}},
			Containers: []corev1.Container{{
				Name:  "volume-check",
				Image: csiDebugImage,
				Command: []string{"busybox", "sh", "-c", strings.Join([]string{
					`printf '%s\n' "$CSI_PAYLOAD" > "$CSI_MOUNT/marker" || exit 1`,
					`got=$(busybox cat "$CSI_MOUNT/marker") || exit 1`,
					`[ "$got" = "$CSI_PAYLOAD" ] || exit 1`,
					`printf '%s\n' "$CSI_SUCCESS"`,
				}, "; ")},
				Env: []corev1.EnvVar{
					{Name: "HOME", Value: "/tmp"},
					{Name: "CSI_MOUNT", Value: csiVolumeMountPath},
					{Name: "CSI_PAYLOAD", Value: payload},
					{Name: "CSI_SUCCESS", Value: successToken},
				},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "data",
					MountPath: csiVolumeMountPath,
				}},
				SecurityContext: &corev1.SecurityContext{
					RunAsNonRoot:             to.Ptr(true),
					AllowPrivilegeEscalation: to.Ptr(false),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
					SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				},
			}},
		},
	}
}

// ---------------------------------------------------------------------------
// StorageClass discovery
// ---------------------------------------------------------------------------

// findStorageClass lists cluster StorageClasses and returns the first one whose
// Provisioner matches v.provisioner, preferring the default class if multiple
// match. Returns an error that enumerates observed classes on miss.
func (v verifyCSIPersistentVolume) findStorageClass(ctx context.Context, kubeClient kubernetes.Interface) (*storagev1.StorageClass, error) {
	classes, err := kubeClient.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list StorageClasses: %w", err)
	}

	var matches []storagev1.StorageClass
	observed := make([]string, 0, len(classes.Items))
	for i := range classes.Items {
		observed = append(observed, fmt.Sprintf("%s (%s)", classes.Items[i].Name, classes.Items[i].Provisioner))
		if classes.Items[i].Provisioner == v.provisioner {
			matches = append(matches, classes.Items[i])
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no StorageClass with provisioner %q; observed: [%s]", v.provisioner, strings.Join(observed, ", "))
	}
	for i := range matches {
		if matches[i].Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
			return &matches[i], nil
		}
	}
	return &matches[0], nil
}

// ---------------------------------------------------------------------------
// Poll loop — like pollUntilReady but with terminal-error support
// ---------------------------------------------------------------------------

// pollUntilPodSucceeded polls checkReady until both the PVC is Bound and the
// pod reaches Succeeded. PodFailed is treated as a terminal error that stops
// polling immediately (the pod has RestartPolicy: Never so it cannot recover).
func (v verifyCSIPersistentVolume) pollUntilPodSucceeded(ctx context.Context, state *csiPollState) error {
	if v.timeout <= 0 {
		return fmt.Errorf("%s: timeout must be > 0, got %s", v.Name(), v.timeout)
	}

	name := v.Name()
	logger := ginkgo.GinkgoLogr
	var lastErrMsg string
	var lastErr error
	start := time.Now()

	logger.Info("Verifier polling", "name", name, "timeout", v.timeout.String(), "interval", DefaultPollInterval.String())
	ginkgo.GinkgoWriter.Printf("[%s] polling (timeout=%s, interval=%s)\n", name, v.timeout, DefaultPollInterval)

	pollErr := wait.PollUntilContextTimeout(ctx, DefaultPollInterval, v.timeout, true, func(ctx context.Context) (bool, error) {
		err := state.checkReady(ctx)
		if err == nil {
			return true, nil
		}
		if msg := err.Error(); msg != lastErrMsg {
			logger.Info("Verifier check", "name", name, "status", "failed", "error", msg)
			lastErrMsg = msg
		}
		var pfe *podFailedError
		if errors.As(err, &pfe) {
			return false, err // terminal — stop polling
		}
		lastErr = err
		return false, nil // retriable
	})

	elapsed := time.Since(start)
	switch {
	case pollErr == nil:
		logVerifierTiming(name, "succeeded", elapsed)
		return nil

	case isPodFailed(pollErr):
		logVerifierTiming(name, "failed", elapsed)
		return fmt.Errorf("%s: %w", name, pollErr)

	case ctx.Err() != nil:
		logVerifierTiming(name, "cancelled", elapsed)
		err := lastErr
		if err == nil {
			err = ctx.Err()
		}
		return fmt.Errorf("%s cancelled after %s: %w", name, elapsed.Round(time.Millisecond), err)

	default: // timeout
		logVerifierTiming(name, "timed out", elapsed)
		diagCtx, cancel := context.WithTimeout(context.Background(), DefaultDiagnoseTimeout)
		defer cancel()
		details := state.diagnose(diagCtx)
		if details != "" {
			logger.Info("Failure diagnostics", "name", name, "details", details)
		}
		err := lastErr
		if err == nil {
			err = pollErr
		}
		if details != "" {
			return fmt.Errorf("%s timed out after %s: %w\n%s", name, elapsed.Round(time.Millisecond), err, details)
		}
		return fmt.Errorf("%s timed out after %s: %w", name, elapsed.Round(time.Millisecond), err)
	}
}

func isPodFailed(err error) bool {
	var pfe *podFailedError
	return errors.As(err, &pfe)
}

// ---------------------------------------------------------------------------
// Single-poll check and diagnostics (methods on csiPollState for mutable
// phase-tracking)
// ---------------------------------------------------------------------------

func (s *csiPollState) checkReady(ctx context.Context) error {
	pvc, err := s.kubeClient.CoreV1().PersistentVolumeClaims(s.namespace).Get(ctx, csiPVCName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get PVC %s/%s: %w", s.namespace, csiPVCName, err)
	}
	pod, err := s.kubeClient.CoreV1().Pods(s.namespace).Get(ctx, csiPodName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get pod %s/%s: %w", s.namespace, csiPodName, err)
	}

	s.logTransitions(pvc.Status.Phase, pod.Status.Phase)

	if pod.Status.Phase == corev1.PodFailed {
		return &podFailedError{msg: podStatusSummary(ctx, s.kubeClient, pod)}
	}
	if pvc.Status.Phase != corev1.ClaimBound {
		return fmt.Errorf("PVC %s/%s phase is %q, expected Bound; pod phase is %q",
			s.namespace, csiPVCName, pvc.Status.Phase, pod.Status.Phase)
	}
	if pod.Status.Phase != corev1.PodSucceeded {
		return fmt.Errorf("pod %s/%s phase is %q, expected Succeeded; PVC is Bound",
			s.namespace, csiPodName, pod.Status.Phase)
	}
	return nil
}

func (s *csiPollState) logTransitions(pvcPhase corev1.PersistentVolumeClaimPhase, podPhase corev1.PodPhase) {
	prefix := fmt.Sprintf("[VerifyCSIPersistentVolume(%s)]", s.provisioner)
	if s.prevPVCPhase != pvcPhase {
		ginkgo.GinkgoWriter.Printf("%s PVC %s/%s transitioned from %q to %q\n",
			prefix, s.namespace, csiPVCName, s.prevPVCPhase, pvcPhase)
		s.prevPVCPhase = pvcPhase
	}
	if s.prevPodPhase != podPhase {
		ginkgo.GinkgoWriter.Printf("%s pod %s/%s transitioned from %q to %q\n",
			prefix, s.namespace, csiPodName, s.prevPodPhase, podPhase)
		s.prevPodPhase = podPhase
	}
}

func (s *csiPollState) diagnose(ctx context.Context) string {
	var sections []string
	sections = append(sections, fmt.Sprintf("[storageclass] name=%s provisioner=%s", s.storageClass, s.provisioner))

	if pvc, err := s.kubeClient.CoreV1().PersistentVolumeClaims(s.namespace).Get(ctx, csiPVCName, metav1.GetOptions{}); err != nil {
		sections = append(sections, fmt.Sprintf("[pvc] failed to get %s/%s: %v", s.namespace, csiPVCName, err))
	} else {
		scName := ""
		if pvc.Spec.StorageClassName != nil {
			scName = *pvc.Spec.StorageClassName
		}
		sections = append(sections, fmt.Sprintf("[pvc] %s/%s phase=%s volumeName=%s storageClass=%s",
			s.namespace, csiPVCName, pvc.Status.Phase, pvc.Spec.VolumeName, scName))
	}

	if pod, err := s.kubeClient.CoreV1().Pods(s.namespace).Get(ctx, csiPodName, metav1.GetOptions{}); err != nil {
		sections = append(sections, fmt.Sprintf("[pod] failed to get %s/%s: %v", s.namespace, csiPodName, err))
	} else {
		sections = append(sections, podDiagnosticLines(pod)...)
	}

	sections = append(sections, diagnoseEvents(ctx, s.kubeClient, s.namespace)...)
	return strings.Join(sections, "\n")
}

// ---------------------------------------------------------------------------
// Pod log verification
// ---------------------------------------------------------------------------

func (v verifyCSIPersistentVolume) verifyPodOutput(ctx context.Context, kubeClient kubernetes.Interface, namespace, successToken string) error {
	logs, err := getPodLogsWithRetry(ctx, kubeClient, namespace, csiPodName)
	if err != nil {
		return fmt.Errorf("failed to get logs for pod %s/%s after it succeeded: %w", namespace, csiPodName, err)
	}
	if strings.TrimSpace(logs) != successToken {
		return fmt.Errorf("pod %s/%s succeeded but logs %q did not equal the volume round-trip token %q "+
			"(payload was only written to the mount, not echoed)", namespace, csiPodName, logs, successToken)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// podStatusSummary builds a one-line status string for a failed pod including
// container exit codes and available logs.
func podStatusSummary(ctx context.Context, kubeClient kubernetes.Interface, pod *corev1.Pod) string {
	var b strings.Builder
	fmt.Fprintf(&b, "pod %s/%s failed", pod.Namespace, pod.Name)
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Terminated != nil {
			fmt.Fprintf(&b, "; container %s terminated with exit code %d, reason: %s, message: %s",
				cs.Name, cs.State.Terminated.ExitCode, cs.State.Terminated.Reason, cs.State.Terminated.Message)
		}
		if cs.State.Waiting != nil {
			fmt.Fprintf(&b, "; container %s waiting reason: %s, message: %s",
				cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message)
		}
	}
	if logs, err := kubeClient.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{}).Do(ctx).Raw(); err == nil && len(logs) > 0 {
		fmt.Fprintf(&b, "; pod logs: %s", string(logs))
	}
	return b.String()
}

// podDiagnosticLines returns structured diagnostic lines for a pod's phase,
// conditions, and container states — used in timeout diagnostics.
func podDiagnosticLines(pod *corev1.Pod) []string {
	lines := []string{fmt.Sprintf("[pod] %s/%s phase=%s reason=%s message=%s",
		pod.Namespace, pod.Name, pod.Status.Phase, pod.Status.Reason, pod.Status.Message)}
	for _, cond := range pod.Status.Conditions {
		lines = append(lines, fmt.Sprintf("[pod] condition type=%s status=%s reason=%s message=%s",
			cond.Type, cond.Status, cond.Reason, cond.Message))
	}
	for _, cs := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
		lines = append(lines, fmt.Sprintf("[pod] container %s ready=%t restartCount=%d", cs.Name, cs.Ready, cs.RestartCount))
		if cs.State.Waiting != nil {
			lines = append(lines, fmt.Sprintf("[pod]   waiting reason=%s message=%s", cs.State.Waiting.Reason, cs.State.Waiting.Message))
		}
		if cs.State.Terminated != nil {
			lines = append(lines, fmt.Sprintf("[pod]   terminated reason=%s exitCode=%d message=%s",
				cs.State.Terminated.Reason, cs.State.Terminated.ExitCode, cs.State.Terminated.Message))
		}
	}
	return lines
}

// waitForUIDRange polls until the namespace gets the openshift.io/sa.scc.uid-range
// annotation and returns the start value as an fsGroup suitable for PodSecurityContext.
func waitForUIDRange(ctx context.Context, kubeClient kubernetes.Interface, namespace string) (*int64, error) {
	var fsGroup *int64
	err := wait.PollUntilContextTimeout(ctx, DefaultPollInterval, csiUIDRangeTimeout, true, func(ctx context.Context) (bool, error) {
		ns, err := kubeClient.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		uidRange := ns.Annotations[csiUIDRangeAnnotation]
		if uidRange == "" {
			return false, nil
		}
		start, _, ok := strings.Cut(uidRange, "/")
		if !ok {
			return false, fmt.Errorf("invalid %s annotation %q", csiUIDRangeAnnotation, uidRange)
		}
		n, err := strconv.ParseInt(start, 10, 64)
		if err != nil {
			return false, fmt.Errorf("invalid %s start %q: %w", csiUIDRangeAnnotation, start, err)
		}
		fsGroup = &n
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("namespace %s was never annotated with %s: %w", namespace, csiUIDRangeAnnotation, err)
	}
	return fsGroup, nil
}

func getPodLogsWithRetry(ctx context.Context, kubeClient kubernetes.Interface, namespace, podName string) (string, error) {
	logBackoff := wait.Backoff{
		Duration: 2 * time.Second,
		Factor:   2.0,
		Steps:    5,
		Cap:      30 * time.Second,
	}
	var logs []byte
	err := retry.OnError(logBackoff, func(error) bool { return ctx.Err() == nil }, func() error {
		var err error
		logs, err = kubeClient.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{}).Do(ctx).Raw()
		return err
	})
	if err != nil {
		return "", err
	}
	return string(logs), nil
}

// ---------------------------------------------------------------------------
// Factory functions
// ---------------------------------------------------------------------------

// VerifyAzureDiskCSIVolume returns a verifier that provisions a 1Gi RWO PVC
// using the Azure Disk CSI provisioner (disk.csi.azure.com) and runs a pod
// that writes and reads a marker file on it.
func VerifyAzureDiskCSIVolume(timeout time.Duration) HostedClusterVerifier {
	return verifyCSIPersistentVolume{
		nsGenerateName: "e2e-csi-disk-",
		provisioner:    azureDiskCSIProvisioner,
		accessMode:     corev1.ReadWriteOnce,
		timeout:        timeout,
	}
}

// VerifyAzureFileCSIVolume returns a verifier that provisions a 1Gi RWO PVC
// using the Azure File CSI provisioner (file.csi.azure.com) and runs a pod
// that writes and reads a marker file on it.
func VerifyAzureFileCSIVolume(timeout time.Duration) HostedClusterVerifier {
	return verifyCSIPersistentVolume{
		nsGenerateName: "e2e-csi-file-",
		provisioner:    azureFileCSIProvisioner,
		accessMode:     corev1.ReadWriteOnce,
		timeout:        timeout,
	}
}
