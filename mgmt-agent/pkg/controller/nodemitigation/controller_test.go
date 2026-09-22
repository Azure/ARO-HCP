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

package nodemitigation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/kuberesources"
	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
	recordfake "github.com/Azure/ARO-HCP/mgmt-agent/pkg/generated/clientset/versioned/fake"
)

type fakeAzure struct {
	now                                      *time.Time
	target                                   int32
	instances                                map[string]string
	instanceErr, poolErr, deleteErr, pollErr error
	operation                                MachineOperation
	deletes, polls                           int
}

func (a *fakeAzure) Pool(_ context.Context, cluster, pool string) (PoolObservation, error) {
	return PoolObservation{ID: strings.ToLower(cluster + "/agentPools/" + pool), Target: a.target, Stable: true, ObservedAt: *a.now}, a.poolErr
}
func (a *fakeAzure) Instance(_ context.Context, _, _, provider, _ string) (string, bool, error) {
	id, exists := a.instances[provider]
	return id, exists, a.instanceErr
}
func (a *fakeAzure) Machine(_ context.Context, _, _, provider string) (string, error) {
	return strings.TrimPrefix(provider, "azure://"), nil
}
func (a *fakeAzure) DeleteMachine(context.Context, string, string) (MachineOperation, error) {
	a.deletes++
	return MachineOperation{Token: "operation", Outcome: "Pending"}, a.deleteErr
}
func (a *fakeAzure) PollDeletion(context.Context, string, string) (MachineOperation, error) {
	a.polls++
	return a.operation, a.pollErr
}

type fixture struct {
	controller *Controller
	kube       *kubefake.Clientset
	records    *recordfake.Clientset
	azure      *fakeAzure
	now        time.Time
	cfg        Config
	informers  informers.SharedInformerFactory
}

func newFixture(t *testing.T, nodes, neverReady int) *fixture {
	t.Helper()
	f := &fixture{now: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC), cfg: testConfig()}
	f.azure = &fakeAzure{now: &f.now, target: 10, instances: map[string]string{}, operation: MachineOperation{Token: "operation", Outcome: "Pending"}}
	objects := []runtime.Object{}
	for i := 0; i < nodes; i++ {
		name, instance := fmt.Sprintf("node-%02d", i), fmt.Sprintf("instance-%02d", i)
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name), ResourceVersion: "1", CreationTimestamp: metav1.NewTime(f.now.Add(-time.Hour)),
			Labels: map[string]string{detectors.SwiftV2LabelKey: detectors.SwiftV2LabelValue, "kubernetes.azure.com/agentpool": "pool", corev1.LabelTopologyZone: "zone"}},
			Spec: corev1.NodeSpec{ProviderID: "azure://" + instance}, Status: corev1.NodeStatus{
				NodeInfo:    corev1.NodeSystemInfo{SystemUUID: instance},
				Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8"), corev1.ResourceMemory: resource.MustParse("32Gi"), corev1.ResourcePods: resource.MustParse("50"), kuberesources.SwiftNICResourceName: resource.MustParse("6")},
				Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(f.now.Add(-time.Hour))}},
			}}
		if i < neverReady {
			node.Status.Conditions[0].Status = corev1.ConditionFalse
		}
		objects = append(objects, node)
		f.azure.instances[node.Spec.ProviderID] = instance
	}
	f.kube = kubefake.NewClientset(objects...)
	f.records = recordfake.NewSimpleClientset()
	eventNumber := 0
	f.kube.PrependReactor("create", "events", func(action ktesting.Action) (bool, runtime.Object, error) {
		eventNumber++
		action.(ktesting.CreateAction).GetObject().(*corev1.Event).Name = fmt.Sprintf("event-%d", eventNumber)
		return false, nil, nil
	})
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{mtpncGVR: "MultitenantPodNetworkConfigList"})
	f.informers = informers.NewSharedInformerFactory(f.kube, 0)
	var err error
	f.controller, err = NewController(f.kube, f.records, dyn, f.azure, "mgmt-agent", f.informers.Core().V1().Nodes(), f.informers.Core().V1().Pods(), f.informers.Core().V1().Events(), func() time.Time { return f.now }, func(*corev1.Node) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	f.controller.AllowConfiguration(true)
	if err := f.controller.SetConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.controller.queue.ShutDown() })
	f.syncCaches(t)
	return f
}

func (f *fixture) syncCaches(t *testing.T) {
	t.Helper()
	for _, source := range []struct {
		resource, kind string
		store          cache.Store
	}{
		{"nodes", "Node", f.informers.Core().V1().Nodes().Informer().GetStore()},
		{"pods", "Pod", f.informers.Core().V1().Pods().Informer().GetStore()},
		{"events", "Event", f.informers.Core().V1().Events().Informer().GetStore()},
	} {
		list, err := f.kube.Tracker().List(corev1.SchemeGroupVersion.WithResource(source.resource), corev1.SchemeGroupVersion.WithKind(source.kind), "")
		if err != nil {
			t.Fatal(err)
		}
		items, err := meta.ExtractList(list)
		if err != nil {
			t.Fatal(err)
		}
		objects := make([]any, len(items))
		for i, item := range items {
			objects[i] = item
		}
		if err := source.store.Replace(objects, ""); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *fixture) tick(t *testing.T) {
	t.Helper()
	f.syncCaches(t)
	f.now = f.now.Add(2 * time.Second)
	if err := f.controller.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) ledger(t *testing.T) *api.NodeMitigationBudget {
	t.Helper()
	budget, err := f.records.MgmtagentV1alpha1().NodeMitigationBudgets("mgmt-agent").Get(context.Background(), budgetName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return budget
}
func mutations(actions []ktesting.Action) []ktesting.Action {
	var result []ktesting.Action
	for _, action := range actions {
		if action.GetVerb() != "get" && action.GetVerb() != "list" && action.GetVerb() != "watch" {
			result = append(result, action)
		}
	}
	return result
}

func TestAdmissionPreservesObservationFreshness(t *testing.T) {
	for _, offset := range []time.Duration{0, -time.Minute, -time.Minute - time.Nanosecond, time.Nanosecond} {
		t.Run(offset.String(), func(t *testing.T) {
			f := newFixture(t, 11, 1)
			at := f.now.Add(offset)
			f.azure.now = &at
			cfg, revision := f.controller.configuration()
			cfg.Mode = Audit
			snapshot, err := f.controller.snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			budget := &api.NodeMitigationBudget{Status: api.NodeMitigationBudgetStatus{Pools: map[string]api.PoolBaseline{}}}
			observation, _, err := f.controller.admission(context.Background(), cfg, revision, snapshot.Nodes[0], budget, snapshot, "")
			wantErr := offset > 0 || offset < -time.Minute
			if (err != nil) != wantErr || !observation.ObservedAt.Equal(at) {
				t.Fatalf("observation=%+v error=%v", observation, err)
			}
		})
	}
}

func TestIdleDiscoveryAndReconcileRate(t *testing.T) {
	f := newFixture(t, 11, 0)
	f.tick(t)
	f.kube.ClearActions()
	for i := 0; i < 10; i++ {
		f.tick(t)
	}
	for _, action := range f.kube.Actions() {
		if action.GetVerb() == "list" {
			t.Fatal("idle discovery performed live LIST")
		}
	}
	if delay := f.controller.reconcileDelay(); delay != 0 {
		t.Fatal(delay)
	}
	if delay := f.controller.reconcileDelay(); delay != f.cfg.RetryInterval.Duration {
		t.Fatal(delay)
	}
}

func TestAuditIndependentCandidatesNoWrites(t *testing.T) {
	f := newFixture(t, 12, 2)
	f.cfg.Mode = Audit
	if err := f.controller.SetConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	if len(mutations(f.kube.Actions())) != 0 || len(mutations(f.records.Actions())) != 0 || f.azure.deletes != 0 {
		t.Fatal("audit mutated mitigation state")
	}
}

func TestNeverReadyOperationAndCapacity(t *testing.T) {
	f := newFixture(t, 11, 1)
	f.tick(t)
	if f.azure.deletes != 0 {
		t.Fatal("cordon and submission occurred in one reconciliation")
	}
	f.tick(t)
	key := recordName("node-00")
	r := f.ledger(t).Status.Reservations[key]
	if f.azure.deletes != 1 || r.OperationToken == "" || r.Outcome != "Pending" {
		t.Fatalf("reservation=%+v deletes=%d", r, f.azure.deletes)
	}
	for _, a := range f.kube.Actions() {
		if a.GetVerb() == "delete" {
			t.Fatal("Kubernetes DELETE used")
		}
	}
	node, err := f.kube.CoreV1().Nodes().Get(context.Background(), "node-00", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !node.Spec.Unschedulable || !ownsCordon(node, r) {
		t.Fatal("incorrect node ownership or cordon")
	}
	f.azure.operation = MachineOperation{Outcome: "Succeeded"}
	f.azure.target = 11
	f.tick(t)
	if f.ledger(t).Status.Reservations[key].ReleasedAt != nil {
		t.Fatal("operation success released missing capacity")
	}
	f.azure.target = 10
	f.tick(t)
	if f.ledger(t).Status.Reservations[key].ReleasedAt == nil || f.azure.deletes != 1 {
		t.Fatal("completion lost reservation history or repeated deletion")
	}
	f.now = f.now.Add(2 * time.Hour)
	f.cfg.Mode = Audit
	if err := f.controller.SetConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	f.kube.ClearActions()
	f.records.ClearActions()
	f.tick(t)
	if len(mutations(f.records.Actions())) != 0 {
		t.Fatal("audit pruned history")
	}
}

func TestUnknownDeletionIsNeverReplayed(t *testing.T) {
	f := newFixture(t, 11, 1)
	f.tick(t)
	f.azure.deleteErr = errors.New("lost response")
	if err := f.controller.reconcile(context.Background()); err == nil {
		t.Fatal("lost response hidden")
	}
	for i := 0; i < 3; i++ {
		f.now = f.now.Add(2 * time.Hour)
		if err := f.controller.reconcile(context.Background()); err == nil {
			t.Fatal("unknown operation was not reported")
		}
	}
	r := f.ledger(t).Status.Reservations[recordName("node-00")]
	if f.azure.deletes != 1 || r.ReleasedAt != nil || r.Outcome != "Unknown" {
		t.Fatalf("unsafe unknown result: %+v", r)
	}
}

func TestPendingObservationSurvivesCapacityFailureAndPause(t *testing.T) {
	f := newFixture(t, 11, 1)
	f.tick(t)
	f.tick(t)
	f.controller.dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "*", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("NIC unavailable") })
	f.cfg.Mode = Audit
	if err := f.controller.SetConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	f.kube.ClearActions()
	f.records.ClearActions()
	if err := f.controller.reconcile(context.Background()); err == nil {
		t.Fatal("capacity failure hidden")
	}
	if f.azure.polls != 1 || f.azure.deletes != 1 || len(mutations(f.records.Actions())) != 0 || len(mutations(f.kube.Actions())) != 0 {
		t.Fatal("pause lost operation observation or made writes")
	}
}

func TestNeverReadyBlocksEveryNonterminalPod(t *testing.T) {
	for _, phase := range []corev1.PodPhase{corev1.PodPending, corev1.PodRunning, corev1.PodUnknown} {
		t.Run(string(phase), func(t *testing.T) {
			f := newFixture(t, 11, 1)
			pod := placementPod("daemon", "node-00")
			pod.Status.Phase = phase
			pod.OwnerReferences = []metav1.OwnerReference{{Kind: "DaemonSet", Name: "network", UID: "ds"}}
			if err := f.kube.Tracker().Add(pod); err != nil {
				t.Fatal(err)
			}
			if err := f.controller.reconcile(context.Background()); err == nil {
				t.Fatal("nonterminal pod not reported")
			}
			if f.azure.deletes != 0 {
				t.Fatal("machine containing a pod deleted")
			}
		})
	}
}

func TestModeAndAccountingFence(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint(conflict), func(t *testing.T) {
			f := newFixture(t, 11, 1)
			if !conflict {
				_, revision := f.controller.configuration()
				if err := f.controller.SetConfig(Default()); err != nil {
					t.Fatal(err)
				}
				if err := f.controller.write(revision, func() error {
					t.Fatal("paused write executed")
					return nil
				}); !errors.Is(err, ErrPaused) {
					t.Fatalf("expected a fenced write, got %v", err)
				}
				return
			}
			f.records.PrependReactor("update", "nodemitigationbudgets", func(action ktesting.Action) (bool, runtime.Object, error) {
				b := action.(ktesting.UpdateAction).GetObject().(*api.NodeMitigationBudget)
				for _, r := range b.Status.Reservations {
					if r.DeleteStartedAt == nil {
						continue
					}
					return true, nil, apierrors.NewConflict(api.Resource("nodemitigationbudgets"), budgetName, errors.New("concurrent reservation"))
				}
				return false, nil, nil
			})
			f.tick(t)
			if err := f.controller.reconcile(context.Background()); err == nil {
				t.Fatal("fenced action succeeded")
			}
			if f.azure.deletes != 0 {
				t.Fatal("write escaped configuration or accounting fence")
			}
		})
	}
}

func TestRecoveredNodeAndReusedIdentityBlockSubmission(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		t.Run(fmt.Sprint(reuse), func(t *testing.T) {
			f := newFixture(t, 11, 1)
			f.kube.PrependReactor("patch", "nodes", func(action ktesting.Action) (bool, runtime.Object, error) {
				obj, err := f.kube.Tracker().Get(corev1.SchemeGroupVersion.WithResource("nodes"), "", "node-00")
				if err != nil {
					t.Fatal(err)
				}
				node := obj.(*corev1.Node)
				if reuse {
					node.UID = "replacement"
				} else {
					node.Status.Conditions[0].Status = corev1.ConditionTrue
				}
				if err := f.kube.Tracker().Update(corev1.SchemeGroupVersion.WithResource("nodes"), node, ""); err != nil {
					t.Fatal(err)
				}
				return true, node, nil
			})
			f.tick(t)
			f.tick(t)
			if f.azure.deletes != 0 {
				t.Fatal("recovered/reused node deleted")
			}
		})
	}
}

func TestUnattemptedReservationResumesWithCurrentConfiguration(t *testing.T) {
	f := newFixture(t, 11, 1)
	cfg, revision := f.controller.configuration()
	b, err := f.controller.budget(context.Background(), revision, cfg.Mode)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.controller.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	node := snapshot.Nodes[0]
	observation, instance, err := f.controller.admission(context.Background(), cfg, revision, node, b, snapshot, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.controller.reserveDeletion(context.Background(), revision, api.MitigationReservation{NodeName: node.Name, NodeUID: node.UID, ProviderID: node.Spec.ProviderID, InstanceID: instance, PoolID: observation.ID, MachineName: instance, Zone: "zone"}, b); err != nil {
		t.Fatal(err)
	}
	f.cfg.Mitigators = []string{"swift"}
	if err := f.controller.SetConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	if f.azure.deletes != 0 || f.ledger(t).Status.Reservations[recordName(string(node.UID))].Outcome != "Cancelled" {
		t.Fatal("saved reservation bypassed current policy")
	}
}
