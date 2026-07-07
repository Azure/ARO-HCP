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

package istio

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
)

type aksCallArgs struct {
	ResourceGroup string
	ClusterName   string
	Revision      string
}

type fakeAKSClient struct {
	clusterInfo     *ClusterInfo
	meshProfile     *MeshProfile
	upgradeInfo     *MeshUpgradeInfo
	calls           []string
	enableArgs      aksCallArgs
	canaryArgs      aksCallArgs
	completeArgs    aksCallArgs
	allCompleteArgs []aksCallArgs
	getStateErr     error
	getUpgradeErr   error
	enableErr       error
	canaryErr       error
	completeErr     error
}

func (f *fakeAKSClient) GetClusterState(_ context.Context, rg, cluster string) (*ClusterInfo, *MeshProfile, error) {
	f.calls = append(f.calls, "GetClusterState")
	if f.getStateErr != nil {
		return nil, nil, f.getStateErr
	}
	return f.clusterInfo, f.meshProfile, nil
}

func (f *fakeAKSClient) GetMeshUpgradeTargets(_ context.Context, rg, cluster string) (*MeshUpgradeInfo, error) {
	f.calls = append(f.calls, "GetMeshUpgradeTargets")
	if f.getUpgradeErr != nil {
		return nil, f.getUpgradeErr
	}
	return f.upgradeInfo, nil
}

func (f *fakeAKSClient) EnableMesh(_ context.Context, rg, cluster, revision string) error {
	f.calls = append(f.calls, "EnableMesh")
	f.enableArgs = aksCallArgs{ResourceGroup: rg, ClusterName: cluster, Revision: revision}
	return f.enableErr
}

func (f *fakeAKSClient) StartCanaryUpgrade(_ context.Context, rg, cluster, revision string) error {
	f.calls = append(f.calls, "StartCanaryUpgrade")
	f.canaryArgs = aksCallArgs{ResourceGroup: rg, ClusterName: cluster, Revision: revision}
	return f.canaryErr
}

func (f *fakeAKSClient) CompleteCanaryUpgrade(_ context.Context, rg, cluster, revision string) error {
	f.calls = append(f.calls, "CompleteCanaryUpgrade")
	args := aksCallArgs{ResourceGroup: rg, ClusterName: cluster, Revision: revision}
	f.completeArgs = args
	f.allCompleteArgs = append(f.allCompleteArgs, args)
	return f.completeErr
}

func testCtx(t *testing.T) context.Context {
	return logr.NewContext(context.Background(), testr.New(t))
}

func baseOpts() UpgradeOptions {
	opts := DefaultUpgradeOptions()
	opts.ResourceGroup = "rg-test"
	opts.ClusterName = "cluster-1"
	opts.Versions = "asm-1-29"
	return opts
}

func trackerAdd(t *testing.T, client *fake.Clientset, obj runtime.Object) {
	t.Helper()
	require.NoError(t, client.Tracker().Add(obj), "failed to add object to fake tracker")
}

func healthyKubeClient() *fake.Clientset {
	return fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingress"}},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingressgateway-external", Namespace: "aks-istio-ingress"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, Selector: map[string]string{"app": "gw"}},
			Status:     corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "10.0.0.1"}}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gw-pod", Namespace: "aks-istio-ingress",
				Labels:      map[string]string{"app": "gw"},
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
			},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		},
	)
}

func TestRunUpgrade_EmptyVersions(t *testing.T) {
	opts := baseOpts()
	opts.Versions = ""
	err := RunUpgrade(testCtx(t), opts, &fakeAKSClient{}, fake.NewSimpleClientset())
	assert.ErrorContains(t, err, "no versions specified")
}

func TestRunUpgrade_InvalidVersion(t *testing.T) {
	opts := baseOpts()
	opts.Versions = "asm 1 29!!"
	err := RunUpgrade(testCtx(t), opts, &fakeAKSClient{}, fake.NewSimpleClientset())
	assert.ErrorContains(t, err, "invalid target version")
}

func TestRunUpgrade_DryRun(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28"}},
		upgradeInfo: &MeshUpgradeInfo{AvailableUpgrades: []string{"asm-1-29"}},
	}
	opts := baseOpts()
	opts.DryRun = true

	err := RunUpgrade(testCtx(t), opts, aks, fake.NewSimpleClientset())
	require.NoError(t, err)
	assert.NotContains(t, aks.calls, "EnableMesh")
	assert.NotContains(t, aks.calls, "StartCanaryUpgrade")
	assert.NotContains(t, aks.calls, "CompleteCanaryUpgrade")
}

func TestRunUpgrade_AlreadyAtTarget(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-29"}},
		upgradeInfo: &MeshUpgradeInfo{},
	}
	opts := baseOpts()
	opts.Versions = "asm-1-29"
	opts.Tag = "prod-stable"

	revisionWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "istio-sidecar-injector-asm-1-29-aks-istio-system"},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "rev.namespace.sidecar-injector.istio.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("test-ca"),
					Service:  &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
				},
				AdmissionReviewVersions: []string{"v1"},
				SideEffects: func() *admissionregistrationv1.SideEffectClass {
					s := admissionregistrationv1.SideEffectClassNone
					return &s
				}(),
			},
		},
	}
	kubeClient := fake.NewSimpleClientset(revisionWebhook)
	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)
	require.NoError(t, err)
	assert.NotContains(t, aks.calls, "EnableMesh")
	assert.NotContains(t, aks.calls, "StartCanaryUpgrade")

	cm, err := kubeClient.CoreV1().ConfigMaps("aks-istio-system").Get(
		context.Background(), "istio-shared-configmap-asm-1-29", metav1.GetOptions{})
	require.NoError(t, err, "skip path should ensure ConfigMap exists when at target")
	assert.Equal(t, "asm-1-29", cm.Labels["istio.io/rev"])

	tagWH, err := kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(
		context.Background(), "istio-revision-tag-prod-stable-aks-istio-system", metav1.GetOptions{})
	require.NoError(t, err, "skip path should ensure tag webhook exists when at target")
	assert.Equal(t, "istiod-asm-1-29", tagWH.Webhooks[0].ClientConfig.Service.Name)
}

func TestRunUpgrade_Install(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: nil},
		upgradeInfo: &MeshUpgradeInfo{},
	}

	kubeClient := healthyKubeClient()
	err := RunUpgrade(testCtx(t), baseOpts(), aks, kubeClient)
	require.NoError(t, err)
	assert.Contains(t, aks.calls, "EnableMesh")
	assert.NotContains(t, aks.calls, "StartCanaryUpgrade")
	assert.Equal(t, "rg-test", aks.enableArgs.ResourceGroup)
	assert.Equal(t, "cluster-1", aks.enableArgs.ClusterName)
	assert.Equal(t, "asm-1-29", aks.enableArgs.Revision)

	cm, err := kubeClient.CoreV1().ConfigMaps("aks-istio-system").Get(
		context.Background(), "istio-shared-configmap-asm-1-29", metav1.GetOptions{})
	require.NoError(t, err, "install path should create ConfigMap")
	assert.Equal(t, "asm-1-29", cm.Labels["istio.io/rev"])
}

func TestRunUpgrade_InstallWithTag(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: nil},
		upgradeInfo: &MeshUpgradeInfo{},
	}

	kubeClient := healthyKubeClient()
	trackerAdd(t, kubeClient, &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "istio-sidecar-injector-asm-1-29-aks-istio-system"},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "rev.namespace.sidecar-injector.istio.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("test-ca-bundle"),
					Service:  &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
				},
			},
		},
	})

	opts := baseOpts()
	opts.Tag = "prod-stable"

	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)
	require.NoError(t, err)
	assert.Contains(t, aks.calls, "EnableMesh")

	tagWH, err := kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(
		context.Background(), "istio-revision-tag-prod-stable-aks-istio-system", metav1.GetOptions{})
	require.NoError(t, err, "install with tag should create the tag webhook")
	assert.Equal(t, "istiod-asm-1-29", tagWH.Webhooks[0].ClientConfig.Service.Name)
	assert.Equal(t, []byte("test-ca-bundle"), tagWH.Webhooks[0].ClientConfig.CABundle)
}

func TestRunUpgrade_ResumeWithTag(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28", "asm-1-29"}},
		upgradeInfo: &MeshUpgradeInfo{UpgradeInProgress: true},
	}
	kubeClient := healthyKubeClient()
	trackerAdd(t, kubeClient, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "prod-stable"}},
	})
	// Tag webhook currently pointing at old revision
	trackerAdd(t, kubeClient, &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "istio-revision-tag-prod-stable-aks-istio-system"},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "rev.namespace.sidecar-injector.istio.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("ca-bundle-28"),
					Service:  &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-28", Namespace: "aks-istio-system"},
				},
			},
		},
	})
	trackerAdd(t, kubeClient, &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "istio-sidecar-injector-asm-1-29-aks-istio-system"},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "rev.namespace.sidecar-injector.istio.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("ca-bundle-29"),
					Service:  &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
				},
			},
		},
	})
	trackerAdd(t, kubeClient, &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "istio-sidecar-injector-asm-1-28-aks-istio-system"},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "rev.namespace.sidecar-injector.istio.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("ca-bundle-28"),
					Service:  &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-28", Namespace: "aks-istio-system"},
				},
			},
		},
	})

	opts := baseOpts()
	opts.Tag = "prod-stable"

	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)
	require.NoError(t, err)
	assert.NotContains(t, aks.calls, "StartCanaryUpgrade")
	assert.Contains(t, aks.calls, "CompleteCanaryUpgrade")

	// Tag webhook should now point at the target revision
	tagWH, err := kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(
		context.Background(), "istio-revision-tag-prod-stable-aks-istio-system", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "istiod-asm-1-29", tagWH.Webhooks[0].ClientConfig.Service.Name,
		"resume should flip tag webhook to target revision")
	assert.Equal(t, []byte("ca-bundle-29"), tagWH.Webhooks[0].ClientConfig.CABundle,
		"resume should update CA bundle to target revision")

	// Namespace labels should stay as the tag value
	ns, err := kubeClient.CoreV1().Namespaces().Get(context.Background(), "app-ns", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "prod-stable", ns.Labels["istio.io/rev"],
		"tag-based namespace labels should not be changed during resume")
}

func TestRunUpgrade_Upgrade(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28"}},
		upgradeInfo: &MeshUpgradeInfo{AvailableUpgrades: []string{"asm-1-29"}},
	}
	kubeClient := healthyKubeClient()
	trackerAdd(t, kubeClient, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "asm-1-28"}},
	})
	trackerAdd(t, kubeClient, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app-ns"},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
		Status:     appsv1.DeploymentStatus{UpdatedReplicas: 1, ReadyReplicas: 1},
	})

	err := RunUpgrade(testCtx(t), baseOpts(), aks, kubeClient)
	require.NoError(t, err)
	assert.Contains(t, aks.calls, "StartCanaryUpgrade")
	assert.Contains(t, aks.calls, "CompleteCanaryUpgrade")
	assert.Equal(t, "asm-1-29", aks.canaryArgs.Revision)
	assert.Equal(t, "asm-1-29", aks.completeArgs.Revision)
}

func TestRunUpgrade_DirectRevisionUpdatesNamespaceBeforeRestart(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28"}},
		upgradeInfo: &MeshUpgradeInfo{AvailableUpgrades: []string{"asm-1-29"}},
	}
	kubeClient := healthyKubeClient()
	trackerAdd(t, kubeClient, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "asm-1-28"}},
	})
	trackerAdd(t, kubeClient, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app-ns"},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
		Status:     appsv1.DeploymentStatus{UpdatedReplicas: 1, ReadyReplicas: 1},
	})
	trackerAdd(t, kubeClient, &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-abc", Namespace: "app-ns",
			OwnerReferences: []metav1.OwnerReference{{Name: "web", Kind: "Deployment", APIVersion: "apps/v1", Controller: ptr.To(true)}},
		},
	})
	trackerAdd(t, kubeClient, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-abc-123", Namespace: "app-ns",
			Annotations:     map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-28"}`},
			OwnerReferences: []metav1.OwnerReference{{Name: "web-abc", Kind: "ReplicaSet", APIVersion: "apps/v1", Controller: ptr.To(true)}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	})

	observedRestart := false
	kubeClient.PrependReactor("patch", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		observedRestart = true
		ns, err := kubeClient.Tracker().Get(corev1.SchemeGroupVersion.WithResource("namespaces"), "", "app-ns")
		if err != nil {
			return true, nil, err
		}
		if got := ns.(*corev1.Namespace).Labels["istio.io/rev"]; got != "asm-1-29" {
			return true, nil, fmt.Errorf("namespace label was %s during restart, expected asm-1-29", got)
		}
		pod, err := kubeClient.Tracker().Get(corev1.SchemeGroupVersion.WithResource("pods"), "app-ns", "web-abc-123")
		if err != nil {
			return true, nil, err
		}
		updated := pod.(*corev1.Pod).DeepCopy()
		updated.Annotations["sidecar.istio.io/status"] = `{"revision":"asm-1-29"}`
		if err := kubeClient.Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), updated, "app-ns"); err != nil {
			return true, nil, err
		}
		return false, nil, nil
	})

	err := RunUpgrade(testCtx(t), baseOpts(), aks, kubeClient)
	require.NoError(t, err)
	assert.True(t, observedRestart, "stale workload should have been restarted during the upgrade")
}

func TestRunUpgrade_EnableMeshError(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: nil},
		upgradeInfo: &MeshUpgradeInfo{},
		enableErr:   fmt.Errorf("ARM 500"),
	}

	err := RunUpgrade(testCtx(t), baseOpts(), aks, healthyKubeClient())
	assert.ErrorContains(t, err, "ARM 500")
}

func TestRunUpgrade_GetClusterStateError(t *testing.T) {
	aks := &fakeAKSClient{
		getStateErr: fmt.Errorf("ARM throttled"),
	}
	err := RunUpgrade(testCtx(t), baseOpts(), aks, fake.NewSimpleClientset())
	assert.ErrorContains(t, err, "failed to get cluster state")
	assert.ErrorContains(t, err, "ARM throttled")
}

func TestRunUpgrade_GetUpgradeTargetsError(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo:   &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile:   &MeshProfile{Revisions: []string{"asm-1-28"}},
		getUpgradeErr: fmt.Errorf("network timeout"),
	}
	err := RunUpgrade(testCtx(t), baseOpts(), aks, fake.NewSimpleClientset())
	assert.ErrorContains(t, err, "failed to get upgrade targets")
	assert.ErrorContains(t, err, "network timeout")
}

func TestRunUpgrade_Resume(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28", "asm-1-29"}},
		upgradeInfo: &MeshUpgradeInfo{UpgradeInProgress: true},
	}
	kubeClient := healthyKubeClient()
	trackerAdd(t, kubeClient, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "asm-1-28"}},
	})

	err := RunUpgrade(testCtx(t), baseOpts(), aks, kubeClient)
	require.NoError(t, err)
	assert.NotContains(t, aks.calls, "StartCanaryUpgrade")
	assert.Contains(t, aks.calls, "CompleteCanaryUpgrade")

	cm, err := kubeClient.CoreV1().ConfigMaps("aks-istio-system").Get(
		context.Background(), "istio-shared-configmap-asm-1-29", metav1.GetOptions{})
	require.NoError(t, err, "resume path should ensure ConfigMap exists")
	assert.Equal(t, "asm-1-29", cm.Labels["istio.io/rev"])
}

func TestRunUpgrade_TagBasedNamespacesWithoutTagConfig(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28"}},
		upgradeInfo: &MeshUpgradeInfo{AvailableUpgrades: []string{"asm-1-29"}},
	}
	kubeClient := healthyKubeClient()
	trackerAdd(t, kubeClient, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "prod-stable"}},
	})

	opts := baseOpts()
	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)
	assert.ErrorContains(t, err, "tag-based injection labels but no tag is configured")
}

func TestRunUpgrade_OrphanRetrySucceeds(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28", "asm-1-29"}},
		upgradeInfo: &MeshUpgradeInfo{},
	}
	kubeClient := healthyKubeClient()
	trackerAdd(t, kubeClient, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "asm-1-28"}},
	})
	trackerAdd(t, kubeClient, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app-ns"},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
		Status:     appsv1.DeploymentStatus{UpdatedReplicas: 1, ReadyReplicas: 1},
	})
	trackerAdd(t, kubeClient, &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-abc", Namespace: "app-ns",
			OwnerReferences: []metav1.OwnerReference{{Name: "web", Kind: "Deployment", APIVersion: "apps/v1", Controller: ptr.To(true)}},
		},
	})
	trackerAdd(t, kubeClient, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-abc-123", Namespace: "app-ns",
			Annotations:     map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-28"}`},
			OwnerReferences: []metav1.OwnerReference{{Name: "web-abc", Kind: "ReplicaSet", APIVersion: "apps/v1", Controller: ptr.To(true)}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	})

	// Simulate pod getting new sidecar only after the orphan retry restart (2nd patch),
	// not the initial restart (1st patch), so the orphan check finds stale pods on its
	// first pass and triggers a real retry iteration.
	patchCount := 0
	kubeClient.PrependReactor("patch", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patchCount++
		if patchCount >= 2 {
			pod, _ := kubeClient.Tracker().Get(
				corev1.SchemeGroupVersion.WithResource("pods"),
				"app-ns", "web-abc-123",
			)
			if pod != nil {
				updated := pod.(*corev1.Pod).DeepCopy()
				updated.Annotations["sidecar.istio.io/status"] = `{"revision":"asm-1-29"}`
				_ = kubeClient.Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), updated, "app-ns")
			}
		}
		return false, nil, nil
	})

	opts := baseOpts()
	opts.MaxOrphanRetries = 3

	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)
	require.NoError(t, err)
	assert.Contains(t, aks.calls, "CompleteCanaryUpgrade")
	assert.GreaterOrEqual(t, patchCount, 2, "orphan retry loop should have triggered a second restart")
}

func TestValidateStopAfter(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    StopAfter
		wantErr bool
	}{
		{name: "canary-start", input: "canary-start", want: StopAfterCanaryStart},
		{name: "orphan-check", input: "orphan-check", want: StopAfterOrphanCheck},
		{name: "invalid value", input: "bogus", wantErr: true},
		{name: "empty is invalid", input: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateStopAfter(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestRunUpgrade_SkipWithMismatchWarning(t *testing.T) {
	// ARM installed asm-1-29 (default) but config targets asm-1-28 — downgrade skip
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-29"}},
		upgradeInfo: &MeshUpgradeInfo{},
	}
	opts := baseOpts()
	opts.Versions = "asm-1-28"

	err := RunUpgrade(testCtx(t), opts, aks, fake.NewSimpleClientset())
	require.NoError(t, err)
	assert.NotContains(t, aks.calls, "EnableMesh")
	assert.NotContains(t, aks.calls, "StartCanaryUpgrade")
	assert.NotContains(t, aks.calls, "CompleteCanaryUpgrade")
}

func TestRunUpgrade_FreshClusterUpgradesToConfigTarget(t *testing.T) {
	// ARM installed n-1 (asm-1-28) as default, config targets n (asm-1-29)
	// Go code should trigger canary upgrade to reach config target
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28"}},
		upgradeInfo: &MeshUpgradeInfo{AvailableUpgrades: []string{"asm-1-29"}},
	}
	kubeClient := healthyKubeClient()
	trackerAdd(t, kubeClient, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "asm-1-28"}},
	})
	trackerAdd(t, kubeClient, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app-ns"},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
		Status:     appsv1.DeploymentStatus{UpdatedReplicas: 1, ReadyReplicas: 1},
	})

	opts := baseOpts()
	opts.Versions = "asm-1-29"

	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)
	require.NoError(t, err)
	assert.Contains(t, aks.calls, "StartCanaryUpgrade")
	assert.Contains(t, aks.calls, "CompleteCanaryUpgrade")
	assert.Equal(t, "asm-1-29", aks.canaryArgs.Revision)
}

func TestRunUpgrade_StopAfterCanaryStart(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28"}},
		upgradeInfo: &MeshUpgradeInfo{AvailableUpgrades: []string{"asm-1-29"}},
	}
	kubeClient := healthyKubeClient()

	opts := baseOpts()
	opts.StopAfter = StopAfterCanaryStart

	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)
	require.NoError(t, err)
	assert.Contains(t, aks.calls, "StartCanaryUpgrade", "should start canary before stopping")
	assert.NotContains(t, aks.calls, "CompleteCanaryUpgrade", "should not complete canary when stopping after canary-start")
}

func TestRunUpgrade_StopAfterOrphanCheck(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28"}},
		upgradeInfo: &MeshUpgradeInfo{AvailableUpgrades: []string{"asm-1-29"}},
	}
	kubeClient := healthyKubeClient()
	trackerAdd(t, kubeClient, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "asm-1-28"}},
	})
	trackerAdd(t, kubeClient, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app-ns"},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
		Status:     appsv1.DeploymentStatus{UpdatedReplicas: 1, ReadyReplicas: 1},
	})

	opts := baseOpts()
	opts.StopAfter = StopAfterOrphanCheck

	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)
	require.NoError(t, err)
	assert.Contains(t, aks.calls, "StartCanaryUpgrade", "should start canary")
	assert.NotContains(t, aks.calls, "CompleteCanaryUpgrade", "should not complete canary when stopping after orphan-check")
}

func TestRunUpgrade_UpgradeWithTag(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28"}},
		upgradeInfo: &MeshUpgradeInfo{AvailableUpgrades: []string{"asm-1-29"}},
	}
	kubeClient := healthyKubeClient()
	trackerAdd(t, kubeClient, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "prod-stable"}},
	})
	trackerAdd(t, kubeClient, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app-ns"},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
		Status:     appsv1.DeploymentStatus{UpdatedReplicas: 1, ReadyReplicas: 1},
	})
	trackerAdd(t, kubeClient, &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "istio-sidecar-injector-asm-1-29-aks-istio-system"},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "rev.namespace.sidecar-injector.istio.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("test-ca-bundle"),
				},
			},
		},
	})

	opts := baseOpts()
	opts.Tag = "prod-stable"

	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)
	require.NoError(t, err)
	assert.Contains(t, aks.calls, "StartCanaryUpgrade")
	assert.Contains(t, aks.calls, "CompleteCanaryUpgrade")

	ns, err := kubeClient.CoreV1().Namespaces().Get(context.Background(), "app-ns", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "prod-stable", ns.Labels["istio.io/rev"], "tag-based label should be preserved, not changed to direct revision")
}

func TestRunUpgrade_OrphanRetryExhausted(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28", "asm-1-29"}},
		upgradeInfo: &MeshUpgradeInfo{},
	}
	kubeClient := healthyKubeClient()
	trackerAdd(t, kubeClient, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "asm-1-28"}},
	})
	trackerAdd(t, kubeClient, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "stuck-pod", Namespace: "app-ns",
			Annotations:     map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-28"}`},
			OwnerReferences: []metav1.OwnerReference{{Name: "stuck-rs", Kind: "ReplicaSet", APIVersion: "apps/v1", Controller: ptr.To(true)}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	})
	trackerAdd(t, kubeClient, &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "stuck-rs", Namespace: "app-ns",
			OwnerReferences: []metav1.OwnerReference{{Name: "stuck-deploy", Kind: "Deployment", APIVersion: "apps/v1", Controller: ptr.To(true)}},
		},
	})
	trackerAdd(t, kubeClient, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "stuck-deploy", Namespace: "app-ns"},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
		Status:     appsv1.DeploymentStatus{UpdatedReplicas: 1, ReadyReplicas: 1},
	})

	opts := baseOpts()
	opts.MaxOrphanRetries = 1

	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)
	assert.ErrorIs(t, err, ErrRetireRevisionWouldOrphanWorkloads)
	assert.NotContains(t, aks.calls, "CompleteCanaryUpgrade")
}

func TestRunUpgrade_HealthCheckFailsRollsBackWorkloads(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28", "asm-1-29"}},
		upgradeInfo: &MeshUpgradeInfo{},
	}
	// Missing istiod-asm-1-29 deployment triggers health check failure
	kubeClient := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingress"}},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-28", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 0},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingressgateway-external", Namespace: "aks-istio-ingress"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, Selector: map[string]string{"app": "gw"}},
			Status:     corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "10.0.0.1"}}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gw-pod", Namespace: "aks-istio-ingress",
				Labels:      map[string]string{"app": "gw"},
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
			},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		},
	)

	opts := baseOpts()
	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)
	assert.ErrorIs(t, err, ErrControlPlaneUnhealthy, "should return health check error")
	assert.NotContains(t, aks.calls, "CompleteCanaryUpgrade", "should not complete canary on health failure")
}

func TestRunUpgrade_CleanupAndUpgrade(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28", "asm-1-29"}},
		upgradeInfo: &MeshUpgradeInfo{AvailableUpgrades: []string{"asm-1-30"}},
	}
	kubeClient := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingress"}},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-28", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-30", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingressgateway-external", Namespace: "aks-istio-ingress"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, Selector: map[string]string{"app": "gw"}},
			Status:     corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "10.0.0.1"}}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gw-pod", Namespace: "aks-istio-ingress",
				Labels:      map[string]string{"app": "gw"},
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-28"}`},
			},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		},
	)

	opts := baseOpts()
	opts.Versions = "asm-1-30"

	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)
	require.NoError(t, err)

	require.GreaterOrEqual(t, len(aks.allCompleteArgs), 2, "should have two CompleteCanaryUpgrade calls")
	assert.Equal(t, "asm-1-28", aks.allCompleteArgs[0].Revision, "first complete should keep old stable revision")
	assert.Equal(t, "asm-1-30", aks.allCompleteArgs[1].Revision, "second complete should finalize fresh canary")
	assert.Contains(t, aks.calls, "StartCanaryUpgrade")
	assert.Equal(t, "asm-1-30", aks.canaryArgs.Revision, "fresh canary should target new version")
}

func TestRunUpgrade_CleanupAndUpgradeWithTag(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28", "asm-1-29"}},
		upgradeInfo: &MeshUpgradeInfo{AvailableUpgrades: []string{"asm-1-30"}},
	}
	kubeClient := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingress"}},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-28", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-30", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingressgateway-external", Namespace: "aks-istio-ingress"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, Selector: map[string]string{"app": "gw"}},
			Status:     corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "10.0.0.1"}}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gw-pod", Namespace: "aks-istio-ingress",
				Labels:      map[string]string{"app": "gw"},
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-28"}`},
			},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		},
		// Webhook for asm-1-28 (the stable revision we're rolling back to)
		&admissionregistrationv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: "istio-sidecar-injector-asm-1-28-aks-istio-system"},
			Webhooks: []admissionregistrationv1.MutatingWebhook{
				{
					Name: "rev.namespace.sidecar-injector.istio.io",
					ClientConfig: admissionregistrationv1.WebhookClientConfig{
						CABundle: []byte("test-ca-bundle"),
					},
				},
			},
		},
		// Webhook for asm-1-30 (the new target after cleanup)
		&admissionregistrationv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: "istio-sidecar-injector-asm-1-30-aks-istio-system"},
			Webhooks: []admissionregistrationv1.MutatingWebhook{
				{
					Name: "rev.namespace.sidecar-injector.istio.io",
					ClientConfig: admissionregistrationv1.WebhookClientConfig{
						CABundle: []byte("test-ca-bundle"),
					},
				},
			},
		},
	)

	opts := baseOpts()
	opts.Versions = "asm-1-30"
	opts.Tag = "prod-stable"

	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)
	require.NoError(t, err)
	assert.Contains(t, aks.calls, "CompleteCanaryUpgrade")
	assert.Contains(t, aks.calls, "StartCanaryUpgrade")
	require.GreaterOrEqual(t, len(aks.allCompleteArgs), 2)
	assert.Equal(t, "asm-1-28", aks.allCompleteArgs[0].Revision, "cleanup should keep old stable revision")
}

func TestOldRevisionFrom(t *testing.T) {
	tests := []struct {
		name      string
		revisions []string
		target    string
		want      string
	}{
		{
			name:      "single old revision",
			revisions: []string{"asm-1-28", "asm-1-29"},
			target:    "asm-1-29",
			want:      "asm-1-28",
		},
		{
			name:      "multiple old revisions picks highest",
			revisions: []string{"asm-1-27", "asm-1-28", "asm-1-29"},
			target:    "asm-1-29",
			want:      "asm-1-28",
		},
		{
			name:      "only target installed",
			revisions: []string{"asm-1-29"},
			target:    "asm-1-29",
			want:      "",
		},
		{
			name:      "empty revisions",
			revisions: nil,
			target:    "asm-1-29",
			want:      "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := oldRevisionFrom(tt.revisions, tt.target)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRunUpgrade_RollbackDoubleFailure(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28", "asm-1-29"}},
		upgradeInfo: &MeshUpgradeInfo{},
	}
	kubeClient := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingress"}},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-28", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 0},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingressgateway-external", Namespace: "aks-istio-ingress"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, Selector: map[string]string{"app": "gw"}},
			Status:     corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "10.0.0.1"}}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gw-pod", Namespace: "aks-istio-ingress",
				Labels:      map[string]string{"app": "gw"},
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
			},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		},
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "asm-1-28"}},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app-ns"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
			Status:     appsv1.DeploymentStatus{UpdatedReplicas: 1, ReadyReplicas: 1},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "web-rs", Namespace: "app-ns",
				OwnerReferences: []metav1.OwnerReference{{Name: "web", Kind: "Deployment", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "web-rs-pod", Namespace: "app-ns",
				Annotations:     map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
				OwnerReferences: []metav1.OwnerReference{{Name: "web-rs", Kind: "ReplicaSet", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)
	// Make deployment patches fail to simulate rollback failure
	kubeClient.PrependReactor("patch", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated rollback patch failure")
	})

	err := RunUpgrade(testCtx(t), baseOpts(), aks, kubeClient)
	assert.ErrorIs(t, err, ErrControlPlaneUnhealthy, "should contain original health check error")
	assert.ErrorContains(t, err, "rollback also failed", "should contain rollback failure")
}

func TestRunUpgrade_StartCanaryError(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28"}},
		upgradeInfo: &MeshUpgradeInfo{AvailableUpgrades: []string{"asm-1-29"}},
		canaryErr:   fmt.Errorf("ARM 409 conflict"),
	}
	kubeClient := healthyKubeClient()

	err := RunUpgrade(testCtx(t), baseOpts(), aks, kubeClient)
	assert.ErrorContains(t, err, "ARM 409 conflict")
	assert.ErrorContains(t, err, "failed to start canary")
	assert.NotContains(t, aks.calls, "CompleteCanaryUpgrade")
}

func TestRunUpgrade_CompleteCanaryFailureMidCleanup(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28", "asm-1-29"}},
		upgradeInfo: &MeshUpgradeInfo{AvailableUpgrades: []string{"asm-1-30"}},
		completeErr: fmt.Errorf("ARM timeout on complete"),
	}
	kubeClient := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingress"}},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-28", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingressgateway-external", Namespace: "aks-istio-ingress"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, Selector: map[string]string{"app": "gw"}},
			Status:     corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "10.0.0.1"}}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gw-pod", Namespace: "aks-istio-ingress",
				Labels:      map[string]string{"app": "gw"},
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-28"}`},
			},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		},
	)

	opts := baseOpts()
	opts.Versions = "asm-1-30"

	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)
	assert.ErrorContains(t, err, "cleanup ARM completion failed")
	assert.ErrorContains(t, err, "ARM timeout on complete")
}

func TestRunUpgrade_PostCompleteVerificationFailure(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28", "asm-1-29"}},
		upgradeInfo: &MeshUpgradeInfo{},
	}
	kubeClient := healthyKubeClient()

	// After CompleteCanaryUpgrade, the target ConfigMap is deleted by
	// external interference. VerifyUpgrade catches the missing ConfigMap.
	configMapDeleted := false
	origComplete := aks.CompleteCanaryUpgrade
	_ = origComplete
	// We can't easily hook the fake AKS client's CompleteCanaryUpgrade,
	// so instead we use a reactor that deletes the ConfigMap on the
	// DeleteRevisionConfigMap call for the old revision — which runs
	// right after complete. The reactor also deletes the target ConfigMap.
	kubeClient.PrependReactor("delete", "configmaps", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if !configMapDeleted {
			configMapDeleted = true
			// Also delete the target ConfigMap to trigger verification failure
			_ = kubeClient.Tracker().Delete(
				corev1.SchemeGroupVersion.WithResource("configmaps"),
				"aks-istio-system", "istio-shared-configmap-asm-1-29")
		}
		return false, nil, nil
	})

	opts := baseOpts()
	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)

	assert.Contains(t, aks.calls, "CompleteCanaryUpgrade",
		"canary should have been completed — this state is non-recoverable")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "post-upgrade verification failed")
	assert.NotContains(t, aks.calls, "EnableMesh",
		"should not attempt re-install after failed post-complete verification")
}

func TestRunUpgrade_OverallTimeout(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28"}},
		upgradeInfo: &MeshUpgradeInfo{AvailableUpgrades: []string{"asm-1-29"}},
		canaryErr:   context.DeadlineExceeded,
	}

	opts := baseOpts()
	opts.OverallTimeout = 1 * time.Nanosecond

	err := RunUpgrade(testCtx(t), opts, aks, healthyKubeClient())
	assert.Error(t, err, "should fail with deadline or canary error")
}

func TestRunUpgrade_HealthCheckFailsVerifiesRollbackRestarted(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28", "asm-1-29"}},
		upgradeInfo: &MeshUpgradeInfo{},
	}
	kubeClient := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingress"}},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-28", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 0},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingressgateway-external", Namespace: "aks-istio-ingress"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, Selector: map[string]string{"app": "gw"}},
			Status:     corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "10.0.0.1"}}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gw-pod", Namespace: "aks-istio-ingress",
				Labels:      map[string]string{"app": "gw"},
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
			},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		},
		// app-ns has a pod on asm-1-29 that needs rollback to asm-1-28
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "asm-1-28"}},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app-ns"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
			Status:     appsv1.DeploymentStatus{UpdatedReplicas: 1, ReadyReplicas: 1},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "web-rs", Namespace: "app-ns",
				OwnerReferences: []metav1.OwnerReference{{Name: "web", Kind: "Deployment", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "web-rs-pod", Namespace: "app-ns",
				Annotations:     map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
				OwnerReferences: []metav1.OwnerReference{{Name: "web-rs", Kind: "ReplicaSet", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)

	rollbackPatchCount := 0
	kubeClient.PrependReactor("patch", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		rollbackPatchCount++
		return false, nil, nil
	})

	err := RunUpgrade(testCtx(t), baseOpts(), aks, kubeClient)
	assert.ErrorIs(t, err, ErrControlPlaneUnhealthy)
	assert.Greater(t, rollbackPatchCount, 0, "rollback should have triggered deployment patches to restore old sidecar")
}

func TestRunUpgrade_HealthCheckFailsRollsBackTagWebhook(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28", "asm-1-29"}},
		upgradeInfo: &MeshUpgradeInfo{},
	}
	kubeClient := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingress"}},
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "prod-stable"}},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-28", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 0},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingressgateway-external", Namespace: "aks-istio-ingress"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, Selector: map[string]string{"app": "gw"}},
			Status:     corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "10.0.0.1"}}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gw-pod", Namespace: "aks-istio-ingress",
				Labels:      map[string]string{"app": "gw"},
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
			},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		},
		// Tag webhook initially pointing at asm-1-29 (the failed revision)
		&admissionregistrationv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: "istio-revision-tag-prod-stable-aks-istio-system"},
			Webhooks: []admissionregistrationv1.MutatingWebhook{
				{
					Name: "rev.namespace.sidecar-injector.istio.io",
					ClientConfig: admissionregistrationv1.WebhookClientConfig{
						CABundle: []byte("ca-bundle-29"),
						Service:  &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
					},
				},
			},
		},
		// Revision webhooks for rollback CA bundle lookup
		&admissionregistrationv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: "istio-sidecar-injector-asm-1-28-aks-istio-system"},
			Webhooks: []admissionregistrationv1.MutatingWebhook{
				{
					Name: "rev.namespace.sidecar-injector.istio.io",
					ClientConfig: admissionregistrationv1.WebhookClientConfig{
						CABundle: []byte("ca-bundle-28"),
						Service:  &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-28", Namespace: "aks-istio-system"},
					},
				},
			},
		},
		&admissionregistrationv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: "istio-sidecar-injector-asm-1-29-aks-istio-system"},
			Webhooks: []admissionregistrationv1.MutatingWebhook{
				{
					Name: "rev.namespace.sidecar-injector.istio.io",
					ClientConfig: admissionregistrationv1.WebhookClientConfig{
						CABundle: []byte("ca-bundle-29"),
						Service:  &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
					},
				},
			},
		},
	)

	opts := baseOpts()
	opts.Tag = "prod-stable"

	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)
	assert.ErrorIs(t, err, ErrControlPlaneUnhealthy, "should return health check error")
	assert.NotContains(t, aks.calls, "CompleteCanaryUpgrade", "should not complete canary on health failure")

	tagWH, err := kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(
		context.Background(), "istio-revision-tag-prod-stable-aks-istio-system", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "istiod-asm-1-28", tagWH.Webhooks[0].ClientConfig.Service.Name,
		"rollback should flip tag webhook back to old revision")
	assert.Equal(t, []byte("ca-bundle-28"), tagWH.Webhooks[0].ClientConfig.CABundle,
		"rollback should update CA bundle to old revision")
}

func TestRunUpgrade_OrphanRetryExhaustedRollsBackTagWebhook(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28", "asm-1-29"}},
		upgradeInfo: &MeshUpgradeInfo{},
	}
	kubeClient := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingress"}},
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "prod-stable"}},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-28", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingressgateway-external", Namespace: "aks-istio-ingress"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, Selector: map[string]string{"app": "gw"}},
			Status:     corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "10.0.0.1"}}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gw-pod", Namespace: "aks-istio-ingress",
				Labels:      map[string]string{"app": "gw"},
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
			},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		},
		// Stuck pod that won't migrate off asm-1-28
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "stuck-pod", Namespace: "app-ns",
				Annotations:     map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-28"}`},
				OwnerReferences: []metav1.OwnerReference{{Name: "stuck-rs", Kind: "ReplicaSet", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "stuck-rs", Namespace: "app-ns",
				OwnerReferences: []metav1.OwnerReference{{Name: "stuck-deploy", Kind: "Deployment", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "stuck-deploy", Namespace: "app-ns"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
			Status:     appsv1.DeploymentStatus{UpdatedReplicas: 1, ReadyReplicas: 1},
		},
		// Tag webhook initially pointing at asm-1-29
		&admissionregistrationv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: "istio-revision-tag-prod-stable-aks-istio-system"},
			Webhooks: []admissionregistrationv1.MutatingWebhook{
				{
					Name: "rev.namespace.sidecar-injector.istio.io",
					ClientConfig: admissionregistrationv1.WebhookClientConfig{
						CABundle: []byte("ca-bundle-29"),
						Service:  &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
					},
				},
			},
		},
		&admissionregistrationv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: "istio-sidecar-injector-asm-1-28-aks-istio-system"},
			Webhooks: []admissionregistrationv1.MutatingWebhook{
				{
					Name: "rev.namespace.sidecar-injector.istio.io",
					ClientConfig: admissionregistrationv1.WebhookClientConfig{
						CABundle: []byte("ca-bundle-28"),
						Service:  &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-28", Namespace: "aks-istio-system"},
					},
				},
			},
		},
		&admissionregistrationv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: "istio-sidecar-injector-asm-1-29-aks-istio-system"},
			Webhooks: []admissionregistrationv1.MutatingWebhook{
				{
					Name: "rev.namespace.sidecar-injector.istio.io",
					ClientConfig: admissionregistrationv1.WebhookClientConfig{
						CABundle: []byte("ca-bundle-29"),
						Service:  &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
					},
				},
			},
		},
	)

	opts := baseOpts()
	opts.Tag = "prod-stable"
	opts.MaxOrphanRetries = 1

	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)
	assert.ErrorIs(t, err, ErrRetireRevisionWouldOrphanWorkloads)
	assert.NotContains(t, aks.calls, "CompleteCanaryUpgrade")

	tagWH, err := kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(
		context.Background(), "istio-revision-tag-prod-stable-aks-istio-system", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "istiod-asm-1-28", tagWH.Webhooks[0].ClientConfig.Service.Name,
		"rollback should flip tag webhook back to old revision")
	assert.Equal(t, []byte("ca-bundle-28"), tagWH.Webhooks[0].ClientConfig.CABundle,
		"rollback should update CA bundle to old revision")
}

func TestRunUpgrade_CleanupUpdatesNamespaceBeforeRestart(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28", "asm-1-29"}},
		upgradeInfo: &MeshUpgradeInfo{AvailableUpgrades: []string{"asm-1-30"}},
	}
	kubeClient := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingress"}},
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "asm-1-29"}},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-28", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-30", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingressgateway-external", Namespace: "aks-istio-ingress"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, Selector: map[string]string{"app": "gw"}},
			Status:     corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "10.0.0.1"}}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gw-pod", Namespace: "aks-istio-ingress",
				Labels:      map[string]string{"app": "gw"},
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-28"}`},
			},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app-ns"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
			Status:     appsv1.DeploymentStatus{UpdatedReplicas: 1, ReadyReplicas: 1},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "web-abc", Namespace: "app-ns",
				OwnerReferences: []metav1.OwnerReference{{Name: "web", Kind: "Deployment", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "web-abc-123", Namespace: "app-ns",
				Annotations:     map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
				OwnerReferences: []metav1.OwnerReference{{Name: "web-abc", Kind: "ReplicaSet", APIVersion: "apps/v1", Controller: ptr.To(true)}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)

	cleanupRestartVerified := false
	kubeClient.PrependReactor("patch", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patchAction := action.(k8stesting.PatchAction)
		if patchAction.GetNamespace() != "app-ns" {
			return false, nil, nil
		}

		ns, err := kubeClient.Tracker().Get(corev1.SchemeGroupVersion.WithResource("namespaces"), "", "app-ns")
		if err != nil {
			return true, nil, err
		}
		nsLabel := ns.(*corev1.Namespace).Labels["istio.io/rev"]

		pod, err := kubeClient.Tracker().Get(corev1.SchemeGroupVersion.WithResource("pods"), "app-ns", "web-abc-123")
		if err != nil {
			return true, nil, err
		}

		if !cleanupRestartVerified {
			if nsLabel != "asm-1-28" {
				return true, nil, fmt.Errorf("namespace label was %s during cleanup restart, expected asm-1-28", nsLabel)
			}
			cleanupRestartVerified = true
		}

		updated := pod.(*corev1.Pod).DeepCopy()
		updated.Annotations["sidecar.istio.io/status"] = fmt.Sprintf(`{"revision":"%s"}`, nsLabel)
		if err := kubeClient.Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), updated, "app-ns"); err != nil {
			return true, nil, err
		}
		return false, nil, nil
	})

	opts := baseOpts()
	opts.Versions = "asm-1-30"

	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)
	require.NoError(t, err)
	assert.True(t, cleanupRestartVerified, "cleanup phase should have restarted workloads with labels already updated")
}

func TestRunUpgrade_DirectRevisionRollbackUpdatesLabels(t *testing.T) {
	aks := &fakeAKSClient{
		clusterInfo: &ClusterInfo{ProvisioningState: "Succeeded"},
		meshProfile: &MeshProfile{Revisions: []string{"asm-1-28", "asm-1-29"}},
		upgradeInfo: &MeshUpgradeInfo{},
	}
	kubeClient := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingress"}},
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "app-ns", Labels: map[string]string{"istio.io/rev": "asm-1-28"}},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-28", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 0},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "aks-istio-ingressgateway-external", Namespace: "aks-istio-ingress"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, Selector: map[string]string{"app": "gw"}},
			Status:     corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "10.0.0.1"}}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gw-pod", Namespace: "aks-istio-ingress",
				Labels:      map[string]string{"app": "gw"},
				Annotations: map[string]string{"sidecar.istio.io/status": `{"revision":"asm-1-29"}`},
			},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		},
	)

	opts := baseOpts()
	opts.Versions = "asm-1-29"

	err := RunUpgrade(testCtx(t), opts, aks, kubeClient)
	assert.ErrorIs(t, err, ErrControlPlaneUnhealthy, "should fail due to unhealthy CP")

	ns, err := kubeClient.CoreV1().Namespaces().Get(context.Background(), "app-ns", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "asm-1-28", ns.Labels["istio.io/rev"],
		"rollback should revert namespace label to old revision")
}

func TestEnsureIngress_PartialConfigErrors(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctx := logr.NewContext(context.Background(), testr.New(t))

	tests := []struct {
		name          string
		ingressIPName string
		regionRG      string
		wantErr       bool
	}{
		{
			name:          "both empty is no-op",
			ingressIPName: "",
			regionRG:      "",
			wantErr:       false,
		},
		{
			name:          "only IngressIPName set errors",
			ingressIPName: "my-ip",
			regionRG:      "",
			wantErr:       true,
		},
		{
			name:          "only RegionRG set errors",
			ingressIPName: "",
			regionRG:      "my-rg",
			wantErr:       true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := UpgradeOptions{
				IngressIPName: tc.ingressIPName,
				RegionRG:      tc.regionRG,
			}
			err := ensureIngress(ctx, client, opts)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "incomplete")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
