package holmes

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

const (
	dataplaneConfigMapName = "holmesgpt-dataplane-config"
)

type PodManager struct {
	config *HolmesConfig
}

func NewPodManager(config *HolmesConfig) *PodManager {
	return &PodManager{config: config}
}

func (pm *PodManager) RunInvestigation(
	ctx context.Context,
	kubeClient kubernetes.Interface,
	kubeconfigYAML []byte,
	question string,
	investigationID string,
	w http.ResponseWriter,
) error {
	namespace := HolmesNamespace
	secretName := "holmes-investigate-" + investigationID
	podName := "holmes-investigate-" + investigationID

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"config": kubeconfigYAML,
		},
	}

	pod := buildPodSpec(podName, namespace, secretName, pm.config, question)

	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = kubeClient.CoreV1().Pods(namespace).Delete(cleanupCtx, podName, metav1.DeleteOptions{})
		_ = kubeClient.CoreV1().Secrets(namespace).Delete(cleanupCtx, secretName, metav1.DeleteOptions{})
	}()

	if _, err := kubeClient.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create investigation secret: %w", err)
	}

	if _, err := kubeClient.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create investigation pod: %w", err)
	}

	if err := waitForPodReady(ctx, kubeClient, namespace, podName); err != nil {
		return fmt.Errorf("pod did not become ready: %w", err)
	}

	return streamPodLogs(ctx, kubeClient, namespace, podName, w)
}

func buildPodSpec(name, namespace, secretName string, config *HolmesConfig, question string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"azure.workload.identity/use": "true",
			},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName:           HolmesServiceAccount,
			AutomountServiceAccountToken: ptr.To(true),
			ActiveDeadlineSeconds:        ptr.To(int64(config.DefaultTimeout)),
			RestartPolicy:                corev1.RestartPolicyNever,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsUser:  ptr.To(int64(1000)),
				RunAsGroup: ptr.To(int64(1000)),
				FSGroup:    ptr.To(int64(1000)),
			},
			Containers: []corev1.Container{{
				Name:            "holmes",
				Image:           config.Image,
				ImagePullPolicy: corev1.PullAlways,
				Command:         []string{"python", "holmes_cli.py"},
				Args:            []string{"ask", question, "-n", "--model=" + config.Model, "--config=/etc/holmes/main-config.yaml"},
				Env: []corev1.EnvVar{
					{Name: "AZURE_AD_TOKEN_AUTH", Value: "true"},
					{Name: "AZURE_API_BASE", Value: config.AzureOpenAIAPIBase},
					{Name: "AZURE_API_VERSION", Value: config.AzureOpenAIAPIVersion},
					{Name: "KUBECONFIG", Value: "/etc/kubeconfig/config"},
					{Name: "HOLMES_CONFIG_PATH", Value: "/etc/holmes/main-config.yaml"},
				},
				VolumeMounts: []corev1.VolumeMount{
					{Name: "kubeconfig", MountPath: "/etc/kubeconfig", ReadOnly: true},
					{Name: "dataplane-config", MountPath: "/etc/holmes/config.yaml", SubPath: "toolsets.yaml", ReadOnly: true},
					{Name: "dataplane-config", MountPath: "/etc/holmes/main-config.yaml", SubPath: "main-config.yaml", ReadOnly: true},
					{Name: "dataplane-config", MountPath: "/etc/holmes/skills/hcp-creation-dataplane/SKILL.md", SubPath: "skill-dataplane.md", ReadOnly: true},
					{Name: "tmp", MountPath: "/tmp"},
					{Name: "holmes-cache", MountPath: "/.holmes"},
				},
				SecurityContext: &corev1.SecurityContext{
					RunAsNonRoot:             ptr.To(true),
					AllowPrivilegeEscalation: ptr.To(false),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
			}},
			Volumes: []corev1.Volume{
				{Name: "kubeconfig", VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: secretName,
						Items:      []corev1.KeyToPath{{Key: "config", Path: "config"}},
					},
				}},
				{Name: "dataplane-config", VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: dataplaneConfigMapName},
					},
				}},
				{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
				{Name: "holmes-cache", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
			},
		},
	}
}

func waitForPodReady(ctx context.Context, kubeClient kubernetes.Interface, namespace, name string) error {
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		pod, err := kubeClient.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		switch pod.Status.Phase {
		case corev1.PodRunning, corev1.PodSucceeded:
			return true, nil
		case corev1.PodFailed:
			return false, fmt.Errorf("pod failed: %s", pod.Status.Message)
		default:
			return false, nil
		}
	})
}

func streamPodLogs(ctx context.Context, kubeClient kubernetes.Interface, namespace, podName string, w http.ResponseWriter) error {
	req := kubeClient.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{Follow: true})
	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("failed to stream pod logs: %w", err)
	}
	defer stream.Close()

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Cache-Control", "no-cache")

	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, readErr := stream.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return fmt.Errorf("failed to write response: %w", writeErr)
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("failed to read pod logs: %w", readErr)
		}
	}

	return nil
}
