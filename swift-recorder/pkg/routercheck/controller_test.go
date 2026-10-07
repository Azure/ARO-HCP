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

package routercheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/swift-recorder/pkg/probe"
)

type runtimeFunc func(context.Context, *corev1.Pod) (Sandbox, error)

func (f runtimeFunc) Sandbox(ctx context.Context, pod *corev1.Pod) (Sandbox, error) {
	return f(ctx, pod)
}

func cachedController(t *testing.T, pod *corev1.Pod, runtime Runtime, execute ExecuteFunc) *Controller {
	t.Helper()
	client := fake.NewClientset()
	factory := informers.NewSharedInformerFactory(client, 0)
	ctrl, err := New(Config{NodeName: "node", Executable: "helper", MaxRecordBytes: 65536}, factory.Core().V1().Pods(), client, discoveryClient(), runtime, execute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ctrl.queue.ShutDown)
	input := discoveryInput(t)
	input.Pod, input.Peers = pod, nil
	ctrl.discovery = cachedDiscoveryInput(t, input)
	if err := ctrl.pods.Informer().GetIndexer().Add(pod); err != nil {
		t.Fatal(err)
	}
	return ctrl
}

func controllerClients(pod *corev1.Pod) (*fake.Clientset, *dynamicfake.FakeDynamicClient) {
	objects := append(trustFixtures(), pod)
	for i, name := range []string{"ignition-server", "ignition-server-proxy"} {
		objects = append(objects,
			&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: pod.Namespace}, Spec: corev1.ServiceSpec{ClusterIP: fmt.Sprintf("172.16.0.%d", i+1), Selector: map[string]string{"app": name}, Ports: []corev1.ServicePort{{Name: "https", Port: 443, TargetPort: intstr.FromInt32(9090)}}}},
			&discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: pod.Namespace, Labels: map[string]string{discoveryv1.LabelServiceName: name}}, AddressType: discoveryv1.AddressTypeIPv4,
				Ports: []discoveryv1.EndpointPort{{Name: ptr.To("https"), Port: ptr.To(int32(9090))}}, Endpoints: []discoveryv1.Endpoint{{Addresses: []string{fmt.Sprintf("10.3.0.%d", i+1)}}}})
	}
	kasService, kasEndpoints, kasRoute := kasFixtures()
	objects = append(objects, kasService, kasEndpoints)
	objects = append(objects, controllerServicePods()...)
	machine, azure := workerObjects("worker", "infra-id", "10.2.0.1")
	return fake.NewClientset(objects...), discoveryClient(machine, azure, kasRoute, mtpnc(pod, "10.1.0.1"),
		&unstructured.Unstructured{Object: map[string]any{"apiVersion": "route.openshift.io/v1", "kind": "Route", "metadata": map[string]any{"name": "ignition-server", "namespace": pod.Namespace, "labels": map[string]any{"hypershift.openshift.io/hosted-control-plane": pod.Namespace}}, "spec": map[string]any{"host": "ignition.example.test"}}})
}

func controllerServicePods() []runtime.Object {
	var pods []runtime.Object
	for _, name := range []string{"ignition-server", "ignition-server-proxy", "kube-apiserver"} {
		pods = append(pods, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", Labels: map[string]string{"app": name}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "server", Ports: []corev1.ContainerPort{{Name: "https", ContainerPort: 9090}, {Name: "client", ContainerPort: 7443}}}}}})
	}
	return pods
}

func controllerSandbox() Sandbox {
	return Sandbox{ID: "sandbox", Path: "/run/netns/cni-sandbox", Device: 1, Inode: 2,
		DNS: probe.DNSConfig{Servers: []string{"10.0.0.10"}, Searches: []string{"ns.svc.cluster.local"}}}
}

func successfulResult(request probe.Request) probe.Result {
	now := time.Now()
	result := probe.Result{NamespaceDevice: request.NamespaceDevice, NamespaceInode: request.NamespaceInode, StartedAt: now, FinishedAt: now, DNSConfig: request.DNS}
	for _, target := range request.Targets {
		observation := probe.Observation{Target: target, StartedAt: now, Stage: "complete"}
		if target.TCPOnly {
			observation.TCPOutcome = "connected"
		} else {
			observation.HTTPStatus, observation.ExpectedStatus, observation.Expected200 = 200, 200, true
			observation.TLS, observation.TLSVerify, observation.TLSVerified = !target.PlainHTTP, !target.PlainHTTP, !target.PlainHTTP
		}
		result.Targets = append(result.Targets, observation)
	}
	for _, server := range request.DNS.Servers[:min(4, len(request.DNS.Servers))] {
		for _, name := range request.DNSNames[:min(8, len(request.DNSNames))] {
			for _, protocol := range []string{"udp", "tcp"} {
				for _, kind := range []string{"TypeA", "TypeAAAA"} {
					observation := probe.DNSObservation{Server: server, Name: name, Protocol: protocol, Type: kind, StartedAt: now, Stage: "complete"}
					if kind == "TypeA" {
						observation.Answers = []string{"10.0.0.1"}
					}
					result.DNS = append(result.DNS, observation)
				}
			}
		}
	}
	return result
}

func TestControllerCachedPodsAndCancellation(t *testing.T) {
	for _, scenario := range []string{"runtime replacement", "shutdown"} {
		t.Run(scenario, func(t *testing.T) {
			pod := routerPod("router", "uid", "node", "10.0.0.1")
			pod.CreationTimestamp = metav1.NewTime(time.Now().Add(-24 * time.Hour))
			client, dynamicClient := controllerClients(pod)
			factory := informers.NewSharedInformerFactory(client, 0)
			pods := factory.Core().V1().Pods()
			ctx, cancel := context.WithCancel(utils.ContextWithLogger(t.Context(), logr.Discard()))
			defer cancel()
			_ = pods.Informer()
			factory.Start(ctx.Done())
			defer factory.Shutdown()
			defer cancel()
			if !cache.WaitForCacheSync(ctx.Done(), pods.Informer().HasSynced) {
				t.Fatal("cache never synced")
			}
			var runtimeReplaced atomic.Bool
			runtimeClient := runtimeFunc(func(context.Context, *corev1.Pod) (Sandbox, error) {
				id := "sandbox"
				if runtimeReplaced.Load() {
					id = "replacement"
				}
				sandbox := controllerSandbox()
				sandbox.ID = id
				return sandbox, nil
			})
			started, stopped := make(chan struct{}, 8), make(chan struct{}, 8)
			var active, overlap atomic.Int32
			ctrl, err := New(Config{NodeName: "node", Executable: "helper", MaxRecordBytes: 64 * 1024}, pods, client, dynamicClient, runtimeClient,
				func(ctx context.Context, _ string, request probe.Request) (json.RawMessage, error) {
					if active.Add(1) != 1 {
						overlap.Add(1)
					}
					defer active.Add(-1)
					if _, ok := ctx.Deadline(); !ok {
						t.Error("helper missing deadline")
					}
					if request.NamespaceInode != 2 || len(request.Targets) != 14 {
						t.Errorf("incomplete request: %+v", request)
					}
					started <- struct{}{}
					<-ctx.Done()
					stopped <- struct{}{}
					return nil, ctx.Err()
				})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- ctrl.Run(ctx) }()
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("controller failed to stop")
				}
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("existing healthy cached pod was not checked")
			}
			if !ctrl.Ready() {
				t.Fatal("probe execution incorrectly prevents readiness")
			}
			// Queue deduplication must prevent another pass while this one runs.
			for range 10 {
				ctrl.queue.Add(keyFor(pod))
			}
			select {
			case <-started:
				t.Fatal("overlapping pass for same pod")
			case <-time.After(50 * time.Millisecond):
			}
			switch scenario {
			case "runtime replacement":
				runtimeReplaced.Store(true)
			case "shutdown":
				cancel()
			}
			select {
			case <-stopped:
			case <-time.After(5 * time.Second):
				t.Fatal("runtime replacement/shutdown did not cancel helper")
			}
			cancel()
			if overlap.Load() != 0 {
				t.Fatal("helper executions overlapped")
			}
		})
	}
}

func TestRuntimeFailureDoesNotStopController(t *testing.T) {
	pod := routerPod("router", "uid", "node", "10.0.0.1")
	client := fake.NewClientset(pod)
	factory := informers.NewSharedInformerFactory(client, 0)
	called := make(chan struct{}, 8)
	ctrl, err := New(Config{NodeName: "node", Executable: "helper", MaxRecordBytes: 64 * 1024}, factory.Core().V1().Pods(), client, discoveryClient(),
		runtimeFunc(func(context.Context, *corev1.Pod) (Sandbox, error) {
			called <- struct{}{}
			return Sandbox{}, errors.New("unavailable")
		}),
		func(context.Context, string, probe.Request) (json.RawMessage, error) {
			t.Error("helper called without runtime identity")
			return nil, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(utils.ContextWithLogger(t.Context(), logr.Discard()))
	factory.Start(ctx.Done())
	done := make(chan error, 1)
	go func() { done <- ctrl.Run(ctx) }()
	defer func() { cancel(); <-done; factory.Shutdown() }()
	for i := range 2 {
		select {
		case <-called:
		case <-time.After(5 * time.Second):
			t.Fatal("runtime failure stopped controller")
		}
		if !ctrl.Ready() {
			t.Fatal("runtime error made controller unready")
		}
		changed := pod.DeepCopy()
		changed.UID = types.UID(fmt.Sprintf("replacement-%d", i))
		if _, err := client.CoreV1().Pods(pod.Namespace).Update(ctx, changed, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestControllerWaitsForAllSources(t *testing.T) {
	for _, source := range []string{"secrets", "azuremachines", "multitenantpodnetworkconfigs"} {
		t.Run(source, func(t *testing.T) {
			pod := routerPod("router", "uid", "node", "10.0.0.1")
			client, dynamicClient := controllerClients(pod)
			ctx, cancel := context.WithCancel(utils.ContextWithLogger(t.Context(), logr.Discard()))
			defer cancel()
			listed := make(chan struct{}, 1)
			reactor := func(action clienttesting.Action) (bool, runtime.Object, error) {
				select {
				case listed <- struct{}{}:
				default:
				}
				return true, nil, apierrors.NewForbidden(action.GetResource().GroupResource(), "", errors.New("denied"))
			}
			if source == "secrets" {
				client.PrependReactor("list", source, reactor)
			} else {
				dynamicClient.PrependReactor("list", source, reactor)
			}
			factory := informers.NewSharedInformerFactory(client, 0)
			var calls atomic.Int32
			ctrl, err := New(Config{NodeName: "node", Executable: "helper", MaxRecordBytes: 1024}, factory.Core().V1().Pods(), client, dynamicClient,
				runtimeFunc(func(context.Context, *corev1.Pod) (Sandbox, error) {
					calls.Add(1)
					return Sandbox{}, errors.New("unexpected runtime call")
				}), func(context.Context, string, probe.Request) (json.RawMessage, error) {
					t.Error("worker ran before source synchronization")
					return nil, nil
				})
			if err != nil {
				t.Fatal(err)
			}
			factory.Start(ctx.Done())
			done := make(chan error, 1)
			go func() { done <- ctrl.Run(ctx) }()
			defer func() { cancel(); factory.Shutdown() }()
			select {
			case <-listed:
			case <-time.After(5 * time.Second):
				t.Fatal("source informer never started")
			}
			if !cache.WaitForCacheSync(ctx.Done(), ctrl.pods.Informer().HasSynced, ctrl.handler.HasSynced, ctrl.discovery.pods.Informer().HasSynced) {
				t.Fatal("pod caches did not synchronize")
			}
			if ctrl.Ready() || calls.Load() != 0 {
				t.Fatal("workers started with an unsynchronized source")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cache sync shutdown returned %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cache sync did not stop on cancellation")
			}
		})
	}
}

func TestReadinessFlapsPreserveCadence(t *testing.T) {
	pod := routerPod("router", "uid", "node", "10.0.0.1")
	pod.CreationTimestamp = metav1.Now()
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "router", ContainerID: "containerd://original", Ready: true}}
	started, release := make(chan struct{}, 4), make(chan struct{})
	ctrl := cachedController(t, pod,
		runtimeFunc(func(context.Context, *corev1.Pod) (Sandbox, error) { return controllerSandbox(), nil }),
		func(ctx context.Context, _ string, request probe.Request) (json.RawMessage, error) {
			started <- struct{}{}
			select {
			case <-release:
				return json.Marshal(successfulResult(request))
			case <-ctx.Done():
				t.Error("readiness flap canceled helper")
				return nil, ctx.Err()
			}
		})
	store := ctrl.pods.Informer().GetIndexer()
	clock := clocktesting.NewFakeClock(time.Now())
	ctrl.clock = clock
	ctrl.cooldown.SetClock(clock)
	ctx := utils.ContextWithLogger(t.Context(), logr.Discard())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if delay, err := ctrl.sync(ctx, keyFor(pod)); err != nil || delay < 30*time.Second || delay > 36*time.Second {
			t.Errorf("initial pass: delay=%v err=%v", delay, err)
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("initial pass did not start")
	}
	for i := range 10 {
		updated := pod.DeepCopy()
		updated.Annotations = map[string]string{"unrelated": "changed"}
		updated.Status.ContainerStatuses[0].Ready = i%2 == 0
		updated.Status.ContainerStatuses[0].Started = ptr.To(i%2 == 0)
		updated.Status.Conditions[0].Status = corev1.ConditionFalse
		if err := store.Update(updated); err != nil {
			t.Fatal(err)
		}
		ctrl.enqueue(updated)
		pod = updated
	}
	if ctrl.queue.Len() != 1 {
		t.Fatal("readiness/metadata updates were not enqueued")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pass did not finish")
	}
	delay := ctrl.cooldown.TimeUntilReady(keyFor(pod))
	next := clock.Now().Add(delay)
	if delay < 30*time.Second || delay > 36*time.Second {
		t.Fatalf("cadence %v outside 30s+jitter", delay)
	}
	// Even a stale delayed queue entry must not cause a back-to-back pass.
	if got, err := ctrl.sync(ctx, keyFor(pod)); err != nil || got != delay {
		t.Fatalf("cooldown pass: delay=%v want=%v err=%v", got, delay, err)
	}
	select {
	case <-started:
		t.Fatal("back-to-back pass bypassed cadence")
	default:
	}
	clock.SetTime(next.Add(-time.Nanosecond))
	if delay, err := ctrl.sync(ctx, keyFor(pod)); err != nil || delay != time.Nanosecond {
		t.Fatalf("pass before deadline: delay=%v err=%v", delay, err)
	}
	select {
	case <-started:
		t.Fatal("pass started before jittered deadline")
	default:
	}
	clock.SetTime(next)
	if delay, err := ctrl.sync(ctx, keyFor(pod)); err != nil || delay < 30*time.Second || delay > 36*time.Second {
		t.Fatalf("scheduled pass: delay=%v err=%v", delay, err)
	}
	select {
	case <-started:
	default:
		t.Fatal("scheduled pass did not start")
	}
	updated := pod.DeepCopy()
	updated.Status.ContainerStatuses[0].ContainerID = "containerd://replacement"
	if err := store.Update(updated); err != nil {
		t.Fatal(err)
	}
	ctrl.enqueue(updated)
	wantDelay := ctrl.cooldown.TimeUntilReady(keyFor(updated))
	if delay, err := ctrl.sync(ctx, keyFor(updated)); err != nil || delay != wantDelay {
		t.Fatalf("container update pass: delay=%v want=%v err=%v", delay, wantDelay, err)
	}
	select {
	case <-started:
		t.Fatal("container update bypassed cadence")
	default:
	}
}

func TestControllerBoundsLargeWorkerPlan(t *testing.T) {
	input := discoveryInput(t)
	input.Workers = nil
	for i := range 1500 {
		input.Workers = append(input.Workers, workerInput(fmt.Sprintf("worker-%04d", i), "infra-id", fmt.Sprintf("10.2.%d.%d", i/250, i%250+1)))
	}
	pod, dns := input.Pod, input.DNS
	d, err := BuildDiscovery(input)
	if err != nil {
		t.Fatal(err)
	}
	roles := []string{"haproxy-ready", "router-loopback", "kas-router-loopback", "router-management", "kas-router-management", "router-swift", "kas-router-swift", "peer-management", "kas-peer-management", "peer-swift", "kas-peer-swift", "ignition-server-service", "ignition-server-endpoint", "ignition-server-proxy-service", "ignition-server-proxy-endpoint", "kas-service", "kas-endpoint"}
	if len(d.Targets) != len(roles)+1500 {
		t.Fatalf("discovery lost targets: %d", len(d.Targets))
	}
	unbounded := probe.Request{}
	for i, target := range d.Targets {
		role := "worker-outbound"
		if i < len(roles) {
			role = roles[i]
		}
		if target.Target.Role != role {
			t.Fatalf("discovery order at %d: got %s, want %s", i, target.Target.Role, role)
		}
		unbounded.Targets = append(unbounded.Targets, target.Target)
	}
	data, err := json.Marshal(unbounded)
	if err != nil || len(data) <= probe.MaxRequestBytes {
		t.Fatalf("fixture must exceed Execute request limit: bytes=%d err=%v", len(data), err)
	}
	called := false
	ctrl := cachedController(t, pod,
		runtimeFunc(func(context.Context, *corev1.Pod) (Sandbox, error) { return Sandbox{ID: "sandbox", DNS: dns}, nil }),
		func(_ context.Context, _ string, request probe.Request) (json.RawMessage, error) {
			called = true
			data, err := json.Marshal(request)
			if err != nil || len(data) > probe.MaxRequestBytes || len(request.Targets) != probe.MaxTargets {
				t.Fatalf("unbounded Execute request: targets=%d bytes=%d err=%v", len(request.Targets), len(data), err)
			}
			if !reflect.DeepEqual(request.Targets, unbounded.Targets[:probe.MaxTargets]) || !reflect.DeepEqual(request.DNS, dns) || !reflect.DeepEqual(request.DNSNames, d.DNSNames) {
				t.Fatal("baseline order or DNS checks lost before Execute")
			}
			result := successfulResult(request)
			// A real failure retains the complete bounded diagnostic families.
			result.Targets[0].HTTPStatus, result.Targets[0].Expected200 = 503, false
			return json.Marshal(result)
		})
	ctrl.discovery = cachedDiscoveryInput(t, input)
	var output bytes.Buffer
	if delay, err := ctrl.sync(utils.ContextWithLogger(t.Context(), logr.FromSlogHandler(slog.NewJSONHandler(&output, nil))), keyFor(pod)); !errors.Is(err, errProbeFailed) || delay != 0 {
		t.Fatalf("failed large worker pass: delay=%v err=%v", delay, err)
	}
	if !called {
		t.Fatal("large worker discovery suppressed Execute")
	}
	var identities []TargetInfo
	statuses := map[string]bool{}
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte{'\n'})
	if len(lines) >= 500 {
		t.Fatalf("overflow identities were not batched: %d records", len(lines))
	}
	for _, line := range lines {
		var entry struct {
			Field  string          `json:"field"`
			Record json.RawMessage `json:"record"`
		}
		if err := json.Unmarshal(line, &entry); err != nil || len(line) > ctrl.cfg.MaxRecordBytes {
			t.Fatalf("invalid/oversized record: bytes=%d err=%v", len(line), err)
		}
		switch entry.Field {
		case "summary":
			var summary passSummary
			if err := json.Unmarshal(entry.Record, &summary); err != nil || !summary.Partial || summary.Truncated != 1 || summary.Discovered != len(d.Targets) || summary.Submitted != probe.MaxTargets || summary.Reported != probe.MaxTargets || summary.Omitted != len(d.Targets)-probe.MaxTargets {
				t.Fatalf("capped coverage counts: %+v err=%v", summary, err)
			}
		case "target":
			var target TargetInfo
			if err := json.Unmarshal(entry.Record, &target); err != nil {
				t.Fatal(err)
			}
			identities = append(identities, target)
		case "omitted_targets":
			var targets []TargetInfo
			if err := json.Unmarshal(entry.Record, &targets); err != nil {
				t.Fatal(err)
			}
			identities = append(identities, targets...)
		case "plan", "coverage":
			var status struct {
				Discovered int `json:"discovered_targets"`
				Submitted  int `json:"submitted_targets"`
				Reported   int `json:"reported_targets"`
				Omitted    int `json:"omitted_targets"`
			}
			if err := json.Unmarshal(entry.Record, &status); err != nil {
				t.Fatal(err)
			}
			if status.Discovered != len(d.Targets) || status.Submitted != probe.MaxTargets || status.Omitted != len(d.Targets)-probe.MaxTargets || (entry.Field == "coverage" && status.Reported != probe.MaxTargets) {
				t.Fatalf("overflow status not explicit in %s: %+v", entry.Field, status)
			}
			statuses[entry.Field] = true
		}
	}
	if !statuses["plan"] || !statuses["coverage"] || !reflect.DeepEqual(identities, d.Targets) {
		t.Fatalf("missing status or discovery identity correlation: statuses=%v identities=%d want=%d", statuses, len(identities), len(d.Targets))
	}
}

func TestProcessNextBackoffAndRecovery(t *testing.T) {
	for _, failure := range []string{"runtime", "discovery", "retiring worker missing azure", "retiring peer missing mtpnc", "helper", "invalid result", "network"} {
		t.Run(failure, func(t *testing.T) {
			pod := routerPod("router", "uid", "node", "10.0.0.1")
			cause := errors.New("temporary failure")
			fail := true
			calls := 0
			ctrl := cachedController(t, pod,
				runtimeFunc(func(context.Context, *corev1.Pod) (Sandbox, error) {
					if fail && failure == "runtime" {
						return Sandbox{}, &RuntimeError{Reason: SandboxStatusUnavailable, Err: cause}
					}
					return controllerSandbox(), nil
				}), func(_ context.Context, _ string, request probe.Request) (json.RawMessage, error) {
					calls++
					if fail && failure == "helper" {
						return nil, cause
					}
					if fail && failure == "invalid result" {
						return json.RawMessage(`{}`), nil
					}
					result := successfulResult(request)
					if fail && failure == "network" {
						result.Targets[0].HTTPStatus = 503
					}
					return json.Marshal(result)
				})
			ctrl.queue.ShutDown()
			clock := clocktesting.NewFakeClock(time.Now())
			ctrl.clock = clock
			ctrl.cooldown.SetClock(clock)
			ctrl.queue = workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.NewTypedItemExponentialFailureRateLimiter[Key](time.Second, time.Minute), workqueue.TypedRateLimitingQueueConfig[Key]{Clock: clock})
			defer ctrl.queue.ShutDown()
			input := discoveryInput(t)
			input.Workers[0].Machine.DeletionTimestamp = ptr.To(metav1.Now())
			ctrl.discovery = cachedDiscoveryInput(t, input)
			indexer, objectKey := ctrl.discovery.secrets.Informer().GetIndexer(), "ns/"+ignitionCAName
			switch failure {
			case "retiring worker missing azure":
				indexer, objectKey = ctrl.discovery.objects[azureMachineGVR].Informer().GetIndexer(), "ns/worker-azure"
			case "retiring peer missing mtpnc":
				indexer, objectKey = ctrl.discovery.objects[mtpncGVR].Informer().GetIndexer(), "ns/peer"
			}
			object, exists, err := indexer.GetByKey(objectKey)
			if err != nil || !exists {
				t.Fatalf("fixture missing %s: %v", objectKey, err)
			}
			discoveryFailure := failure == "discovery" || strings.HasPrefix(failure, "retiring")
			if discoveryFailure {
				if err := indexer.Delete(object); err != nil {
					t.Fatal(err)
				}
			}
			ctx := utils.ContextWithLogger(t.Context(), logr.Discard())
			key := keyFor(pod)
			for retry := 1; retry <= 2; retry++ {
				ctrl.queue.Add(key)
				if !ctrl.processNext(ctx) || ctrl.queue.NumRequeues(key) != retry || ctrl.cooldown.TimeUntilReady(key) != 0 {
					t.Fatalf("failure did not back off: retries=%d cooldown=%v", ctrl.queue.NumRequeues(key), ctrl.cooldown.TimeUntilReady(key))
				}
			}
			if (failure == "runtime" || discoveryFailure) && calls != 0 {
				t.Fatal("missing inputs did not stop execution")
			}
			fail = false
			if discoveryFailure {
				if err := indexer.Add(object); err != nil {
					t.Fatal(err)
				}
			}
			ctrl.queue.Add(key)
			ctrl.processNext(ctx)
			if delay := ctrl.cooldown.TimeUntilReady(key); ctrl.queue.NumRequeues(key) != 0 || delay < 5*time.Minute || delay > 6*time.Minute {
				t.Fatalf("success did not reset retries and set cadence: retries=%d delay=%v", ctrl.queue.NumRequeues(key), delay)
			}
			completed := calls
			ctrl.queue.Add(key)
			ctrl.processNext(ctx)
			if calls != completed {
				t.Fatal("stale retry bypassed success cooldown")
			}
		})
	}
}

func TestCurrentPodEligibility(t *testing.T) {
	pod := routerPod("router", "uid", "node", "10.0.0.1")
	c := &Controller{cfg: Config{NodeName: "node"}}
	if !c.eligible(pod) {
		t.Fatal("healthy running pod excluded")
	}
	for _, mutate := range []func(*corev1.Pod){
		func(p *corev1.Pod) { p.Spec.NodeName = "other" }, func(p *corev1.Pod) { p.DeletionTimestamp = &metav1.Time{} },
		func(p *corev1.Pod) { p.Labels[pniLabel] = "" }, func(p *corev1.Pod) { p.Spec.Containers = nil },
		func(p *corev1.Pod) { p.Spec.HostNetwork = true }, func(p *corev1.Pod) { p.Labels["app"] = "other" },
	} {
		changed := pod.DeepCopy()
		mutate(changed)
		if c.eligible(changed) {
			t.Errorf("ineligible pod accepted: %+v", changed)
		}
	}
	// Empty queues terminate cleanly even if cancellation precedes cache sync.
	client := fake.NewClientset()
	factory := informers.NewSharedInformerFactory(client, 0)
	ctrl, err := New(Config{NodeName: "node", Executable: "helper", MaxRecordBytes: 1024}, factory.Core().V1().Pods(), client, discoveryClient(), runtimeFunc(func(context.Context, *corev1.Pod) (Sandbox, error) { return Sandbox{}, nil }), func(context.Context, string, probe.Request) (json.RawMessage, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(utils.ContextWithLogger(t.Context(), logr.Discard()))
	cancel()
	if err := ctrl.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Run: %v", err)
	}
}

func TestLogSplitting(t *testing.T) {
	var output bytes.Buffer
	logger := logr.FromSlogHandler(slog.NewJSONHandler(&output, nil)).WithValues("pod_name", "router", "controller_name", RouterCheckControllerName)
	ctx := utils.ContextWithLogger(t.Context(), logger)
	c := &Controller{cfg: Config{MaxRecordBytes: 64 * 1024}}
	var records []any
	for range 100 {
		records = append(records, map[string]any{"target": "peer", "data": strings.Repeat("x", 2000)})
	}
	c.record(ctx, "result", map[string]any{"targets": records})
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte{'\n'})
	if len(lines) < 2 {
		t.Fatal("large result was not split")
	}
	for _, line := range lines {
		if len(line) > 64*1024 || !json.Valid(line) {
			t.Fatalf("invalid/oversized log record: %d bytes", len(line))
		}
	}
}

func TestEnqueueDefersEligibilityToReconcile(t *testing.T) {
	pod := routerPod("router", "uid", "node", "10.0.0.1")
	client := fake.NewClientset()
	factory := informers.NewSharedInformerFactory(client, 0)
	ctrl, err := New(Config{NodeName: "node", Executable: "helper", MaxRecordBytes: 1024}, factory.Core().V1().Pods(), client, discoveryClient(), runtimeFunc(func(context.Context, *corev1.Pod) (Sandbox, error) {
		t.Error("runtime called for disappeared pod")
		return Sandbox{}, nil
	}), func(context.Context, string, probe.Request) (json.RawMessage, error) {
		t.Error("disappeared pod probed")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.queue.ShutDown()
	for _, scenario := range []string{"deleted", "unrelated", "other node", "replaced UID"} {
		t.Run(scenario, func(t *testing.T) {
			current := pod.DeepCopy()
			switch scenario {
			case "unrelated":
				current.Labels["app"] = "other"
			case "other node":
				current.Spec.NodeName = "other"
			case "replaced UID":
				current.UID = "replacement"
			}
			if scenario != "deleted" {
				if err := ctrl.pods.Informer().GetIndexer().Add(current); err != nil {
					t.Fatal(err)
				}
			}
			event := any(current)
			switch scenario {
			case "deleted":
				event = cache.DeletedFinalStateUnknown{Key: "ns/router", Obj: pod}
			case "replaced UID":
				event = pod
			}
			ctrl.enqueue(event)
			if ctrl.queue.Len() != 1 {
				t.Fatal("pod event filtered before reconciliation")
			}
			ctrl.processNext(utils.ContextWithLogger(t.Context(), logr.Discard()))
			if ctrl.cooldown.TimeUntilReady(keyFor(pod)) != 0 || ctrl.queue.Len() != 0 || ctrl.queue.NumRequeues(keyFor(pod)) != 0 {
				t.Fatal("ineligible pod scheduled another pass")
			}
		})
	}
}
