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

package e2e

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	kubetesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"
	configfake "github.com/openshift/client-go/config/clientset/versioned/fake"
)

const (
	ingressDNSTestNamespace = "certificate-test"
	ingressDNSTestHostname  = "acme-staging-v02.api.letsencrypt.org"
)

func TestWaitForIngressCertificateDNSWaitsForOperator(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name       string
		conditions []configv1.ClusterOperatorStatusCondition
	}{
		{name: "Available condition absent"},
		{name: "unavailable", conditions: []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse}}},
		{name: "availability unknown", conditions: []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorAvailable, Status: configv1.ConditionUnknown}}},
		{name: "available but degraded", conditions: []configv1.ClusterOperatorStatusCondition{
			{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue},
			{Type: configv1.OperatorDegraded, Status: configv1.ConditionTrue, Reason: "MissingDNSMetricsTLS"},
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client := kubefake.NewClientset(ingressDNSTestEndpointSlice())
				operator := ingressDNSTestOperator()
				operator.Status.Conditions = scenario.conditions
				configClient := configfake.NewSimpleClientset(operator)
				probe := newIngressDNSTestProbe(t, client, corev1.PodStatus{Phase: corev1.PodSucceeded})
				operatorReads := 0
				configClient.PrependReactor("get", "clusteroperators", func(action kubetesting.Action) (bool, runtime.Object, error) {
					require.Equal(t, "dns", action.(kubetesting.GetAction).GetName(), "must observe the guest DNS operator")
					operatorReads++
					if operatorReads == 2 {
						require.NoError(t, configClient.Tracker().Update(configv1.GroupVersion.WithResource("clusteroperators"), ingressDNSTestOperator(), ""))
					}
					return false, nil, nil
				})
				client.PrependReactor("create", "pods", func(kubetesting.Action) (bool, runtime.Object, error) {
					require.GreaterOrEqual(t, operatorReads, 2, "must not create a probe while DNS is unavailable or degraded")
					return false, nil, nil
				})

				err := waitForIngressCertificateDNS(t.Context(), client, configClient.ConfigV1(), ingressDNSTestNamespace, ingressDNSTestHostname, time.Minute, time.Second)

				require.NoError(t, err)
				probe.requireDeleted(t)
			})
		})
	}
}

func TestWaitForIngressCertificateDNSWaitsForReadyEndpoints(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name   string
		mutate func(*discoveryv1.EndpointSlice)
	}{
		{name: "no slices"},
		{name: "no endpoints", mutate: func(slice *discoveryv1.EndpointSlice) { slice.Endpoints = nil }},
		{name: "no addresses", mutate: func(slice *discoveryv1.EndpointSlice) { slice.Endpoints[0].Addresses = nil }},
		{name: "unready address", mutate: func(slice *discoveryv1.EndpointSlice) { slice.Endpoints[0].Conditions.Ready = ptr.To(false) }},
		{name: "unknown readiness", mutate: func(slice *discoveryv1.EndpointSlice) { slice.Endpoints[0].Conditions.Ready = nil }},
		{name: "terminating address", mutate: func(slice *discoveryv1.EndpointSlice) { slice.Endpoints[0].Conditions.Terminating = ptr.To(true) }},
		{name: "no ports", mutate: func(slice *discoveryv1.EndpointSlice) { slice.Ports = nil }},
		{name: "TCP only", mutate: func(slice *discoveryv1.EndpointSlice) { slice.Ports[0].Protocol = ptr.To(corev1.ProtocolTCP) }},
		{name: "not the DNS port", mutate: func(slice *discoveryv1.EndpointSlice) { slice.Ports[0].Name = ptr.To("metrics") }},
		{name: "zero port", mutate: func(slice *discoveryv1.EndpointSlice) { slice.Ports[0].Port = ptr.To(int32(0)) }},
		{name: "another service", mutate: func(slice *discoveryv1.EndpointSlice) { slice.Labels[discoveryv1.LabelServiceName] = "another-service" }},
		{name: "another namespace", mutate: func(slice *discoveryv1.EndpointSlice) { slice.Namespace = "another-namespace" }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client := kubefake.NewClientset()
				if scenario.mutate != nil {
					initial := ingressDNSTestEndpointSlice()
					scenario.mutate(initial)
					require.NoError(t, client.Tracker().Add(initial))
				}
				configClient := configfake.NewSimpleClientset(ingressDNSTestOperator())
				probe := newIngressDNSTestProbe(t, client, corev1.PodStatus{Phase: corev1.PodSucceeded})
				endpointLists := 0
				client.PrependReactor("list", "endpointslices", func(kubetesting.Action) (bool, runtime.Object, error) {
					endpointLists++
					if endpointLists == 2 {
						ready := ingressDNSTestEndpointSlice()
						ready.Name = "ready-dns"
						require.NoError(t, client.Tracker().Add(ready))
					}
					return false, nil, nil
				})
				client.PrependReactor("create", "pods", func(kubetesting.Action) (bool, runtime.Object, error) {
					require.GreaterOrEqual(t, endpointLists, 2, "must not probe before a ready nonterminating UDP DNS endpoint exists")
					return false, nil, nil
				})

				err := waitForIngressCertificateDNS(t.Context(), client, configClient.ConfigV1(), ingressDNSTestNamespace, ingressDNSTestHostname, time.Minute, time.Second)

				require.NoError(t, err)
				probe.requireDeleted(t)
			})
		})
	}
}

func TestWaitForIngressCertificateDNSRequiresSuccessfulProbe(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		unrelated := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: ingressDNSTestNamespace}}
		client := kubefake.NewClientset(ingressDNSTestEndpointSlice(), unrelated)
		configClient := configfake.NewSimpleClientset(ingressDNSTestOperator())
		probe := newIngressDNSTestProbe(t, client,
			corev1.PodStatus{Phase: corev1.PodPending},
			corev1.PodStatus{Phase: corev1.PodRunning},
			corev1.PodStatus{Phase: corev1.PodSucceeded},
		)

		err := waitForIngressCertificateDNS(t.Context(), client, configClient.ConfigV1(), ingressDNSTestNamespace, ingressDNSTestHostname, time.Minute, time.Second)

		require.NoError(t, err)
		require.Equal(t, 3, probe.reads, "Pending or Running must not satisfy the guest DNS lookup")
		probe.requireDeleted(t)
		pods, err := client.CoreV1().Pods(ingressDNSTestNamespace).List(t.Context(), metav1.ListOptions{})
		require.NoError(t, err)
		require.Len(t, pods.Items, 1, "cleanup must leave unrelated pods intact")
		require.Equal(t, unrelated.Name, pods.Items[0].Name)

		spec := probe.created.Spec
		require.Equal(t, corev1.DNSClusterFirst, spec.DNSPolicy, "lookup must use the normal workload DNS path")
		require.False(t, spec.HostNetwork, "host networking would bypass the workload network")
		require.False(t, spec.HostPID)
		require.False(t, spec.HostIPC)
		require.NotNil(t, spec.AutomountServiceAccountToken)
		require.False(t, *spec.AutomountServiceAccountToken, "DNS lookup must not receive API credentials")
		require.Empty(t, spec.Volumes, "probe must not mount tokens or host paths")
		require.Equal(t, corev1.RestartPolicyNever, spec.RestartPolicy)
		require.NotNil(t, spec.ActiveDeadlineSeconds)
		require.Positive(t, *spec.ActiveDeadlineSeconds)
		require.LessOrEqual(t, *spec.ActiveDeadlineSeconds, int64(61), "probe execution must be bounded by readiness timeout")
		require.NotNil(t, spec.SecurityContext)
		require.True(t, ptr.Deref(spec.SecurityContext.RunAsNonRoot, false))
		require.NotNil(t, spec.SecurityContext.SeccompProfile)
		require.Equal(t, corev1.SeccompProfileTypeRuntimeDefault, spec.SecurityContext.SeccompProfile.Type)
		require.Len(t, spec.Containers, 1)
		security := spec.Containers[0].SecurityContext
		require.NotNil(t, security)
		require.False(t, ptr.Deref(security.Privileged, false))
		require.NotNil(t, security.AllowPrivilegeEscalation)
		require.False(t, *security.AllowPrivilegeEscalation)
		require.NotNil(t, security.Capabilities)
		require.Contains(t, security.Capabilities.Drop, corev1.Capability("ALL"))
	})
}

func TestWaitForIngressCertificateDNSDeadlineRetainsLastObservation(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"operator", "endpoints", "probe"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client := kubefake.NewClientset(ingressDNSTestEndpointSlice())
				operator := ingressDNSTestOperator()
				wantObservation := ""
				switch scenario {
				case "operator":
					operator.Status.Conditions = []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse, Reason: "MissingDNSMetricsTLS"}}
					wantObservation = "MissingDNSMetricsTLS"
				case "endpoints":
					require.NoError(t, client.Tracker().Delete(discoveryv1.SchemeGroupVersion.WithResource("endpointslices"), "openshift-dns", "dns-default"))
					wantObservation = "no ready UDP endpoints"
				case "probe":
					wantObservation = "ContainerCreating"
				}
				configClient := configfake.NewSimpleClientset(operator)
				probe := newIngressDNSTestProbe(t, client, corev1.PodStatus{
					Phase: corev1.PodPending,
					ContainerStatuses: []corev1.ContainerStatus{{Name: "dns-lookup", State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating", Message: "waiting for pod network"},
					}}},
				})
				started := time.Now()

				err := waitForIngressCertificateDNS(t.Context(), client, configClient.ConfigV1(), ingressDNSTestNamespace, ingressDNSTestHostname, 5*time.Second, time.Second)

				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.ErrorContains(t, err, wantObservation)
				require.ErrorContains(t, err, ingressDNSTestHostname)
				require.Equal(t, 5*time.Second, time.Since(started), "permanent DNS unavailability must exhaust the bounded timeout")
				if scenario == "probe" {
					probe.requireDeleted(t)
				} else {
					require.Nil(t, probe.created, "DNS gate must not launch a probe when prerequisites never become ready")
					require.Empty(t, probe.deleted)
				}
			})
		})
	}
}

func TestWaitForIngressCertificateDNSUsesOneTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		client := kubefake.NewClientset(ingressDNSTestEndpointSlice())
		configClient := configfake.NewSimpleClientset(ingressDNSTestOperator())
		probe := newIngressDNSTestProbe(t, client, corev1.PodStatus{Phase: corev1.PodRunning, Reason: "LookupStillPending"})
		operatorReads := 0
		configClient.PrependReactor("get", "clusteroperators", func(kubetesting.Action) (bool, runtime.Object, error) {
			operatorReads++
			if operatorReads < 3 {
				operator := ingressDNSTestOperator()
				operator.Status.Conditions[0].Status = configv1.ConditionFalse
				return true, operator, nil
			}
			return false, nil, nil
		})
		started := time.Now()

		err := waitForIngressCertificateDNS(t.Context(), client, configClient.ConfigV1(), ingressDNSTestNamespace, ingressDNSTestHostname, 5*time.Second, time.Second)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.ErrorContains(t, err, "LookupStillPending")
		require.Equal(t, 5*time.Second, time.Since(started), "operator and pod waits must share one deadline")
		probe.requireDeleted(t)
	})
}

func TestWaitForIngressCertificateDNSCancellation(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"before start", "operator wait", "probe wait"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				client := kubefake.NewClientset(ingressDNSTestEndpointSlice())
				configClient := configfake.NewSimpleClientset(ingressDNSTestOperator())
				probe := newIngressDNSTestProbe(t, client, corev1.PodStatus{Phase: corev1.PodRunning, Reason: "LookupPendingAtCancellation"})
				switch scenario {
				case "before start":
					cancel()
				case "operator wait":
					configClient.PrependReactor("get", "clusteroperators", func(kubetesting.Action) (bool, runtime.Object, error) {
						cancel()
						operator := ingressDNSTestOperator()
						operator.Status.Conditions[0].Status = configv1.ConditionFalse
						operator.Status.Conditions[0].Reason = "DNSUnavailableAtCancellation"
						return true, operator, nil
					})
				case "probe wait":
					client.PrependReactor("get", "pods", func(kubetesting.Action) (bool, runtime.Object, error) {
						cancel()
						return false, nil, nil
					})
				}
				cleanupInspected := false
				contextClient := &ingressDNSCleanupClient{Interface: client, inspectDelete: func(cleanupCtx context.Context) {
					cleanupInspected = true
					require.NoError(t, cleanupCtx.Err(), "owned probe deletion must not reuse the canceled readiness context")
					deadline, ok := cleanupCtx.Deadline()
					require.True(t, ok, "cleanup must have an independent bounded deadline")
					require.Positive(t, time.Until(deadline))
					require.LessOrEqual(t, time.Until(deadline), time.Minute)
				}}

				err := waitForIngressCertificateDNS(ctx, contextClient, configClient.ConfigV1(), ingressDNSTestNamespace, ingressDNSTestHostname, time.Minute, time.Second)

				require.ErrorIs(t, err, context.Canceled)
				if scenario == "probe wait" {
					require.ErrorContains(t, err, "LookupPendingAtCancellation")
					require.True(t, cleanupInspected)
					probe.requireDeleted(t)
				} else {
					require.Nil(t, probe.created, "cancellation before DNS readiness must not create a probe")
					require.False(t, cleanupInspected, "must not delete a resource that was never created")
					if scenario == "before start" {
						require.Empty(t, client.Actions(), "pre-canceled calls must not access Kubernetes")
						require.Empty(t, configClient.Actions(), "pre-canceled calls must not access the operator")
					} else {
						require.ErrorContains(t, err, "DNSUnavailableAtCancellation")
					}
				}
			})
		})
	}
}

func TestWaitForIngressCertificateDNSReportsProbeFailure(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		client := kubefake.NewClientset(ingressDNSTestEndpointSlice())
		configClient := configfake.NewSimpleClientset(ingressDNSTestOperator())
		probe := newIngressDNSTestProbe(t, client, corev1.PodStatus{
			Phase: corev1.PodFailed, Reason: "DNSLookupFailed", Message: "lookup did not produce an answer",
			ContainerStatuses: []corev1.ContainerStatus{{Name: "dns-lookup", State: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "LookupRefused", Message: "resolver refused the lookup"},
			}}},
		})
		started := time.Now()

		err := waitForIngressCertificateDNS(t.Context(), client, configClient.ConfigV1(), ingressDNSTestNamespace, ingressDNSTestHostname, time.Minute, time.Second)

		require.ErrorContains(t, err, "DNSLookupFailed")
		require.ErrorContains(t, err, "LookupRefused")
		require.ErrorContains(t, err, "resolver refused the lookup")
		require.False(t, errors.Is(err, context.DeadlineExceeded), "terminal pod failure must not be hidden behind a timeout")
		require.Equal(t, time.Duration(0), time.Since(started), "failed probes must fail immediately")
		probe.requireDeleted(t)
	})
}

func TestWaitForIngressCertificateDNSSurfacesAPIErrors(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name      string
		verb      string
		resource  string
		created   bool
		wantDelay bool
	}{
		{name: "operator get", verb: "get", resource: "clusteroperators", wantDelay: true},
		{name: "endpoints list", verb: "list", resource: "endpointslices", wantDelay: true},
		{name: "probe create", verb: "create", resource: "pods"},
		{name: "probe get", verb: "get", resource: "pods", created: true, wantDelay: true},
		{name: "probe delete", verb: "delete", resource: "pods", created: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client := kubefake.NewClientset(ingressDNSTestEndpointSlice())
				configClient := configfake.NewSimpleClientset(ingressDNSTestOperator())
				probe := newIngressDNSTestProbe(t, client, corev1.PodStatus{Phase: corev1.PodSucceeded})
				failure := errors.New("test API access denied")
				reactor := func(kubetesting.Action) (bool, runtime.Object, error) { return true, nil, failure }
				if scenario.resource == "clusteroperators" {
					configClient.PrependReactor(scenario.verb, scenario.resource, reactor)
				} else {
					client.PrependReactor(scenario.verb, scenario.resource, reactor)
				}

				err := waitForIngressCertificateDNS(t.Context(), client, configClient.ConfigV1(), ingressDNSTestNamespace, ingressDNSTestHostname, 5*time.Second, time.Second)

				require.ErrorIs(t, err, failure, "API errors must remain discoverable, not be mistaken for readiness")
				if scenario.wantDelay {
					require.ErrorIs(t, err, context.DeadlineExceeded)
				}
				if !scenario.created {
					require.Nil(t, probe.created)
					require.Empty(t, probe.deleted, "unsuccessful create must not delete an unowned pod")
				} else if scenario.verb != "delete" {
					probe.requireDeleted(t)
				} else {
					require.NotNil(t, probe.created, "cleanup error must be reported even after successful resolution")
				}
			})
		})
	}
}

func ingressDNSTestOperator() *configv1.ClusterOperator {
	return &configv1.ClusterOperator{
		ObjectMeta: metav1.ObjectMeta{Name: "dns"},
		Status: configv1.ClusterOperatorStatus{Conditions: []configv1.ClusterOperatorStatusCondition{
			{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue},
			{Type: configv1.OperatorDegraded, Status: configv1.ConditionFalse},
		}},
	}
}

func ingressDNSTestEndpointSlice() *discoveryv1.EndpointSlice {
	return &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Name: "dns-default", Namespace: "openshift-dns", Labels: map[string]string{discoveryv1.LabelServiceName: "dns-default"}},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{"10.128.0.10"}, Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(true)}}},
		Ports:       []discoveryv1.EndpointPort{{Name: ptr.To("dns"), Protocol: ptr.To(corev1.ProtocolUDP), Port: ptr.To(int32(5353))}},
	}
}

type ingressDNSTestProbe struct {
	client  *kubefake.Clientset
	created *corev1.Pod
	reads   int
	deleted []string
}

func newIngressDNSTestProbe(t *testing.T, client *kubefake.Clientset, statuses ...corev1.PodStatus) *ingressDNSTestProbe {
	t.Helper()
	require.NotEmpty(t, statuses)
	probe := &ingressDNSTestProbe{client: client}
	client.PrependReactor("create", "pods", func(action kubetesting.Action) (bool, runtime.Object, error) {
		require.Nil(t, probe.created, "readiness must own only one lookup pod")
		pod := action.(kubetesting.CreateAction).GetObject().(*corev1.Pod).DeepCopy()
		// Supply API-server identity generation; all later reads/deletes use the real tracker.
		if pod.Name == "" {
			pod.Name = pod.GenerateName + "test"
		}
		pod.Namespace = action.GetNamespace()
		pod.UID = types.UID("dns-probe-test")
		require.Equal(t, ingressDNSTestNamespace, pod.Namespace, "probe must run in the caller's workload namespace")
		require.NoError(t, client.Tracker().Add(pod))
		probe.created = pod.DeepCopy()
		return true, pod, nil
	})
	client.PrependReactor("get", "pods", func(action kubetesting.Action) (bool, runtime.Object, error) {
		require.NotNil(t, probe.created, "probe status must only be read after a successful create")
		get := action.(kubetesting.GetAction)
		object, err := client.Tracker().Get(corev1.SchemeGroupVersion.WithResource("pods"), action.GetNamespace(), get.GetName())
		if err != nil {
			return true, nil, err
		}
		pod := object.(*corev1.Pod).DeepCopy()
		pod.Status = statuses[min(probe.reads, len(statuses)-1)]
		probe.reads++
		return true, pod, nil
	})
	client.PrependReactor("delete", "pods", func(action kubetesting.Action) (bool, runtime.Object, error) {
		require.NotNil(t, probe.created, "must not delete a lookup pod that was never created")
		name := action.(kubetesting.DeleteAction).GetName()
		require.Equal(t, probe.created.Name, name, "cleanup must target only the owned probe")
		require.Equal(t, probe.created.Namespace, action.GetNamespace())
		probe.deleted = append(probe.deleted, name)
		return false, nil, nil
	})
	return probe
}

func (probe *ingressDNSTestProbe) requireDeleted(t *testing.T) {
	t.Helper()
	require.NotNil(t, probe.created, "expected readiness to create a lookup pod")
	require.Equal(t, []string{probe.created.Name}, probe.deleted, "must delete the owned probe once")
	_, err := probe.client.Tracker().Get(corev1.SchemeGroupVersion.WithResource("pods"), probe.created.Namespace, probe.created.Name)
	require.True(t, apierrors.IsNotFound(err), "probe must actually be removed from the API tracker, got %v", err)
}

// The generated fake drops request contexts, so inspect cleanup at the typed boundary.
type ingressDNSCleanupClient struct {
	kubernetes.Interface
	inspectDelete func(context.Context)
}

func (client *ingressDNSCleanupClient) CoreV1() corev1client.CoreV1Interface {
	return &ingressDNSCleanupCore{CoreV1Interface: client.Interface.CoreV1(), inspectDelete: client.inspectDelete}
}

type ingressDNSCleanupCore struct {
	corev1client.CoreV1Interface
	inspectDelete func(context.Context)
}

func (client *ingressDNSCleanupCore) Pods(namespace string) corev1client.PodInterface {
	return &ingressDNSCleanupPods{PodInterface: client.CoreV1Interface.Pods(namespace), inspectDelete: client.inspectDelete}
}

type ingressDNSCleanupPods struct {
	corev1client.PodInterface
	inspectDelete func(context.Context)
}

func (client *ingressDNSCleanupPods) Delete(ctx context.Context, name string, options metav1.DeleteOptions) error {
	client.inspectDelete(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	return client.PodInterface.Delete(ctx, name, options)
}
