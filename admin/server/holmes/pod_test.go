package holmes

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestBuildPodSpec(t *testing.T) {
	config := &HolmesConfig{
		Image:                 "quay.io/haoran/holmesgpt:latest",
		AzureOpenAIAPIBase:    "https://test.openai.azure.com",
		AzureOpenAIAPIVersion: "2025-04-01-preview",
		Model:                 "azure/gpt-5.2",
		DefaultTimeout:        600,
	}

	pod := buildPodSpec("test-pod", "aro-holmesgpt", "test-secret", "test-cm", config, "what is wrong?")

	t.Run("metadata", func(t *testing.T) {
		if pod.Name != "test-pod" {
			t.Errorf("name = %q, want test-pod", pod.Name)
		}
		if pod.Namespace != "aro-holmesgpt" {
			t.Errorf("namespace = %q, want aro-holmesgpt", pod.Namespace)
		}
		if pod.Labels["azure.workload.identity/use"] != "true" {
			t.Error("missing WI label")
		}
	})

	t.Run("service account", func(t *testing.T) {
		if pod.Spec.ServiceAccountName != HolmesServiceAccount {
			t.Errorf("SA = %q, want %q", pod.Spec.ServiceAccountName, HolmesServiceAccount)
		}
		if pod.Spec.AutomountServiceAccountToken == nil || !*pod.Spec.AutomountServiceAccountToken {
			t.Error("AutomountServiceAccountToken should be true for WI")
		}
	})

	t.Run("restart policy", func(t *testing.T) {
		if pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
			t.Errorf("restart policy = %v, want Never", pod.Spec.RestartPolicy)
		}
	})

	t.Run("active deadline", func(t *testing.T) {
		if pod.Spec.ActiveDeadlineSeconds == nil || *pod.Spec.ActiveDeadlineSeconds != 600 {
			t.Errorf("ActiveDeadlineSeconds = %v, want 600", pod.Spec.ActiveDeadlineSeconds)
		}
	})

	t.Run("security context", func(t *testing.T) {
		sc := pod.Spec.SecurityContext
		if sc == nil {
			t.Fatal("pod security context is nil")
		}
		if sc.RunAsUser == nil || *sc.RunAsUser != 1000 {
			t.Error("RunAsUser should be 1000")
		}

		csc := pod.Spec.Containers[0].SecurityContext
		if csc == nil {
			t.Fatal("container security context is nil")
		}
		if csc.RunAsNonRoot == nil || !*csc.RunAsNonRoot {
			t.Error("RunAsNonRoot should be true")
		}
		if csc.AllowPrivilegeEscalation == nil || *csc.AllowPrivilegeEscalation {
			t.Error("AllowPrivilegeEscalation should be false")
		}
		if len(csc.Capabilities.Drop) != 1 || csc.Capabilities.Drop[0] != "ALL" {
			t.Error("should drop ALL capabilities")
		}
	})

	t.Run("container", func(t *testing.T) {
		if len(pod.Spec.Containers) != 1 {
			t.Fatalf("container count = %d, want 1", len(pod.Spec.Containers))
		}
		c := pod.Spec.Containers[0]

		if c.Image != "quay.io/haoran/holmesgpt:latest" {
			t.Errorf("image = %q", c.Image)
		}

		if len(c.Command) != 2 || c.Command[0] != "python" || c.Command[1] != "holmes_cli.py" {
			t.Errorf("command = %v", c.Command)
		}

		if len(c.Args) < 2 || c.Args[0] != "ask" || c.Args[1] != "what is wrong?" {
			t.Errorf("args = %v", c.Args)
		}
	})

	t.Run("env vars", func(t *testing.T) {
		c := pod.Spec.Containers[0]
		envMap := make(map[string]string)
		for _, e := range c.Env {
			envMap[e.Name] = e.Value
		}

		if envMap["AZURE_AD_TOKEN_AUTH"] != "true" {
			t.Error("AZURE_AD_TOKEN_AUTH should be true")
		}
		if envMap["AZURE_API_BASE"] != "https://test.openai.azure.com" {
			t.Errorf("AZURE_API_BASE = %q", envMap["AZURE_API_BASE"])
		}
		if envMap["KUBECONFIG"] != "/etc/kubeconfig/config" {
			t.Errorf("KUBECONFIG = %q", envMap["KUBECONFIG"])
		}
	})

	t.Run("volumes", func(t *testing.T) {
		volMap := make(map[string]corev1.Volume)
		for _, v := range pod.Spec.Volumes {
			volMap[v.Name] = v
		}

		if v, ok := volMap["kubeconfig"]; !ok {
			t.Error("missing kubeconfig volume")
		} else if v.Secret == nil || v.Secret.SecretName != "test-secret" {
			t.Errorf("kubeconfig secret = %v", v.Secret)
		}

		if v, ok := volMap["holmes-config"]; !ok {
			t.Error("missing holmes-config volume")
		} else if v.ConfigMap == nil || v.ConfigMap.Name != "test-cm" {
			t.Errorf("holmes-config configmap = %v", v.ConfigMap)
		}

		if _, ok := volMap["tmp"]; !ok {
			t.Error("missing tmp volume")
		}

		if _, ok := volMap["holmes-cache"]; !ok {
			t.Error("missing holmes-cache volume")
		}
	})

	t.Run("volume mounts", func(t *testing.T) {
		c := pod.Spec.Containers[0]
		mountMap := make(map[string]corev1.VolumeMount)
		for _, m := range c.VolumeMounts {
			mountMap[m.Name] = m
		}

		if m, ok := mountMap["kubeconfig"]; !ok {
			t.Error("missing kubeconfig mount")
		} else if !m.ReadOnly {
			t.Error("kubeconfig should be read-only")
		}

		if m, ok := mountMap["holmes-config"]; !ok {
			t.Error("missing holmes-config mount")
		} else if !m.ReadOnly {
			t.Error("holmes-config should be read-only")
		}
	})

	t.Run("resource limits", func(t *testing.T) {
		c := pod.Spec.Containers[0]
		cpuLimit := c.Resources.Limits[corev1.ResourceCPU]
		memLimit := c.Resources.Limits[corev1.ResourceMemory]
		if cpuLimit.String() != "1" {
			t.Errorf("CPU limit = %s, want 1", cpuLimit.String())
		}
		if memLimit.String() != "2Gi" {
			t.Errorf("memory limit = %s, want 2Gi", memLimit.String())
		}
	})
}
