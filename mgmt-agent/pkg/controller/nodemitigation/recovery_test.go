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
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"

	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

func TestMultipleStalledPodsDoNotRequireAllReplicasReady(t *testing.T) {
	f := swiftFixture(t)
	pod, err := f.kube.CoreV1().Pods("test").Get(context.Background(), "router-stalled", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pod.Name, pod.UID, pod.Spec.NodeName = "router-stalled-2", "second", "node-02"
	if err := f.kube.Tracker().Add(pod); err != nil {
		t.Fatal(err)
	}
	event, err := f.kube.CoreV1().Events("test").Get(context.Background(), "sandbox", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	event.Name, event.Source.Host = "sandbox-2", pod.Spec.NodeName
	event.InvolvedObject.Name, event.InvolvedObject.UID = pod.Name, pod.UID
	if err := f.kube.Tracker().Add(event); err != nil {
		t.Fatal(err)
	}
	deployment, err := f.kube.AppsV1().Deployments("test").Get(context.Background(), "router", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	deployment.Spec.Replicas = ptr.To(int32(3))
	deployment.Status.Replicas, deployment.Status.UpdatedReplicas = 3, 3
	if err := f.kube.Tracker().Update(appsv1.SchemeGroupVersion.WithResource("deployments"), deployment, "test"); err != nil {
		t.Fatal(err)
	}
	f.kube.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		meta := action.(ktesting.CreateAction).GetObject().(metav1.Object)
		err := f.kube.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("pods"), "test", meta.GetName())
		return true, nil, err
	})
	f.tick(t)
	f.now = f.now.Add(f.cfg.EvictionCooldown.Duration)
	f.tick(t)
	if evictionCount(f.kube.Actions()) != 2 || len(f.ledger(t).Status.Evictions) != 2 {
		t.Fatal("rescue waited for a replacement checkpoint or all replicas")
	}
}

func TestLostLedgerWithOwnershipCannotResetHistory(t *testing.T) {
	for _, emptyLedger := range []bool{false, true} {
		for _, mode := range []Mode{Audit, Enforce} {
			t.Run(fmt.Sprintf("empty=%v/%s", emptyLedger, mode), func(t *testing.T) {
				f := swiftFixture(t)
				f.tick(t)
				budgets := f.records.MgmtagentV1alpha1().NodeMitigationBudgets("mgmt-agent")
				if err := budgets.Delete(context.Background(), budgetName, metav1.DeleteOptions{}); err != nil {
					t.Fatal(err)
				}
				if emptyLedger {
					_, err := budgets.Create(context.Background(), &api.NodeMitigationBudget{
						ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: "mgmt-agent"},
					}, metav1.CreateOptions{})
					if err != nil {
						t.Fatal(err)
					}
				}
				f.cfg.Mode = mode
				if err := f.controller.SetConfig(f.cfg); err != nil {
					t.Fatal(err)
				}
				f.kube.ClearActions()
				f.records.ClearActions()
				err := f.controller.reconcile(context.Background())
				if err == nil || !strings.Contains(err.Error(), "without initialized accounting") {
					t.Fatalf("lost accounting not reported: %v", err)
				}
				if len(mutations(f.kube.Actions())) != 0 || len(mutations(f.records.Actions())) != 0 {
					t.Fatal("lost accounting allowed new writes")
				}
			})
		}
	}
}

func TestMitigationEventsUseTargetNamespace(t *testing.T) {
	f := swiftFixture(t)
	f.tick(t)
	events, err := f.kube.CoreV1().Events("test").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events.Items {
		if event.Reason == "PodEvictionAccepted" && event.Namespace == event.InvolvedObject.Namespace {
			return
		}
	}
	t.Fatal("eviction Event missing from pod namespace")
}

func TestUnsupportedLedgerCannotResetHistory(t *testing.T) {
	f := swiftFixture(t)
	b := &api.NodeMitigationBudget{ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: "mgmt-agent"},
		Status: api.NodeMitigationBudgetStatus{Evictions: map[string]api.EvictionRecord{"existing": {NodeUID: "existing"}}}}
	if err := f.records.Tracker().Add(b); err != nil {
		t.Fatal(err)
	}

	f.records.ClearActions()
	if err := f.controller.reconcile(context.Background()); err == nil {
		t.Fatal("unsupported history silently migrated")
	}
	if len(mutations(f.records.Actions())) != 0 || len(mutations(f.kube.Actions())) != 0 {
		t.Fatal("unsupported ledger changed")
	}
}

func TestNonemptyLedgerRequiresPositiveEvictionWindow(t *testing.T) {
	for _, window := range []time.Duration{0, -time.Second} {
		for _, mode := range []Mode{Disabled, Audit, Enforce} {
			t.Run(fmt.Sprintf("%s/%s", window, mode), func(t *testing.T) {
				f := swiftFixture(t)
				f.cfg.Mode = mode
				if err := f.controller.SetConfig(f.cfg); err != nil {
					t.Fatal(err)
				}
				budget := &api.NodeMitigationBudget{
					ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: "mgmt-agent"},
					Status: api.NodeMitigationBudgetStatus{
						Version: 1, EvictionWindow: metav1.Duration{Duration: window},
						Evictions: map[string]api.EvictionRecord{"existing": {
							WorkloadUID: "deployment", NodeUID: "node-00", PodUID: "earlier-pod",
							AttemptedAt: metav1.NewTime(f.now.Add(-2 * time.Second)),
						}},
					},
				}
				if err := f.records.Tracker().Add(budget); err != nil {
					t.Fatal(err)
				}
				f.kube.ClearActions()
				f.records.ClearActions()
				err := f.controller.reconcile(context.Background())
				if err == nil || !strings.Contains(err.Error(), "positive eviction window") {
					t.Fatalf("invalid accounting not reported: %v", err)
				}
				if len(mutations(f.kube.Actions())) != 0 || len(mutations(f.records.Actions())) != 0 {
					t.Fatal("invalid accounting permitted writes")
				}
				if !reflect.DeepEqual(f.ledger(t).Status, budget.Status) {
					t.Fatal("invalid accounting history was changed")
				}
			})
		}
	}
}

func TestEmptyVersionOneLedgerCanInitializeEvictionWindow(t *testing.T) {
	f := swiftFixture(t)
	if err := f.records.Tracker().Add(&api.NodeMitigationBudget{
		ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: "mgmt-agent"},
		Status:     api.NodeMitigationBudgetStatus{Version: 1},
	}); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	budget := f.ledger(t)
	if evictionCount(f.kube.Actions()) != 1 || len(budget.Status.Evictions) != 1 ||
		budget.Status.EvictionWindow != f.cfg.EvictionWindow {
		t.Fatal("empty version-1 accounting could not initialize")
	}
}

func TestValidLedgerRetention(t *testing.T) {
	for _, mode := range []Mode{Disabled, Audit, Enforce} {
		for _, age := range []time.Duration{0, 5 * time.Minute, 5*time.Minute + time.Second} {
			t.Run(fmt.Sprintf("%s/age=%s", mode, age), func(t *testing.T) {
				f := swiftFixture(t)
				f.cfg.Mode = mode
				f.cfg.EvictionWindow.Duration = 5 * time.Minute
				if err := f.controller.SetConfig(f.cfg); err != nil {
					t.Fatal(err)
				}
				if err := f.informers.Core().V1().Events().Informer().GetStore().Replace(nil, ""); err != nil {
					t.Fatal(err)
				}
				budget := &api.NodeMitigationBudget{
					ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: "mgmt-agent"},
					Status: api.NodeMitigationBudgetStatus{
						Version: 1, EvictionWindow: f.cfg.EvictionWindow,
						Evictions: map[string]api.EvictionRecord{"existing": {
							WorkloadUID: "deployment", NodeUID: "node-00", PodUID: "earlier-pod",
							AttemptedAt: metav1.NewTime(f.now.Add(-age)),
						}},
					},
				}
				if err := f.records.Tracker().Add(budget); err != nil {
					t.Fatal(err)
				}
				f.kube.ClearActions()
				f.records.ClearActions()
				if err := f.controller.reconcile(context.Background()); err != nil {
					t.Fatal(err)
				}
				wantRecords := 1
				if mode == Enforce && age > f.cfg.EvictionWindow.Duration {
					wantRecords = 0
				}
				if got := len(f.ledger(t).Status.Evictions); got != wantRecords {
					t.Fatalf("retained records = %d, want %d", got, wantRecords)
				}
				if len(mutations(f.kube.Actions())) != 0 {
					t.Fatal("pruning mutated Kubernetes resources")
				}
				if mode != Enforce && len(mutations(f.records.Actions())) != 0 {
					t.Fatal("non-enforcing mode mutated accounting")
				}
			})
		}
	}
}

func TestEvictionWindowIncreaseRequiresMigration(t *testing.T) {
	for _, mode := range []Mode{Disabled, Audit, Enforce} {
		for _, candidates := range []bool{false, true} {
			for _, age := range []time.Duration{0, 30 * time.Second, 2 * time.Minute, 11 * time.Minute} {
				t.Run(fmt.Sprintf("%s/candidates=%v/age=%s", mode, candidates, age), func(t *testing.T) {
					f := swiftFixture(t)
					f.cfg.Mode = mode
					f.cfg.EvictionWindow.Duration = 10 * time.Minute
					if err := f.controller.SetConfig(f.cfg); err != nil {
						t.Fatal(err)
					}
					if !candidates {
						if err := f.informers.Core().V1().Events().Informer().GetStore().Replace(nil, ""); err != nil {
							t.Fatal(err)
						}
					}
					budget := &api.NodeMitigationBudget{
						ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: "mgmt-agent"},
						Status: api.NodeMitigationBudgetStatus{
							Version: 1, EvictionWindow: metav1.Duration{Duration: time.Minute},
							Evictions: map[string]api.EvictionRecord{},
						},
					}
					if age > 0 {
						budget.Status.Evictions["existing"] = api.EvictionRecord{
							WorkloadUID: "deployment", NodeUID: "node-00", PodUID: "earlier-pod",
							AttemptedAt: metav1.NewTime(f.now.Add(-age)),
						}
					}
					if err := f.records.Tracker().Add(budget); err != nil {
						t.Fatal(err)
					}
					f.kube.ClearActions()
					f.records.ClearActions()
					for range 2 {
						err := f.controller.reconcile(context.Background())
						if mode == Disabled {
							if err != nil {
								t.Fatal(err)
							}
						} else if err == nil || !strings.Contains(err.Error(), "increase requires explicit accounting migration") {
							t.Fatalf("window increase not reported: %v", err)
						}
					}
					if len(mutations(f.kube.Actions())) != 0 || len(mutations(f.records.Actions())) != 0 {
						t.Fatal("window increase permitted ownership, accounting or eviction writes")
					}
					if !reflect.DeepEqual(f.ledger(t).Status, budget.Status) {
						t.Fatal("window increase changed retained accounting")
					}
					if err := evictionAllowance(budget.Status, f.cfg, "deployment", "node-00", f.now); err == nil {
						t.Fatal("direct admission allowed a window increase")
					}
				})
			}
		}
	}
}

func TestEvictionWindowDecreasePreservesStoredRetention(t *testing.T) {
	for _, mode := range []Mode{Audit, Enforce} {
		for _, age := range []time.Duration{0, 2 * time.Minute, 10 * time.Minute, 10*time.Minute + time.Second} {
			t.Run(fmt.Sprintf("%s/age=%s", mode, age), func(t *testing.T) {
				f := swiftFixture(t)
				f.cfg.Mode = mode
				f.cfg.EvictionWindow.Duration = time.Minute
				if err := f.controller.SetConfig(f.cfg); err != nil {
					t.Fatal(err)
				}
				budget := &api.NodeMitigationBudget{
					ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: "mgmt-agent"},
					Status: api.NodeMitigationBudgetStatus{
						Version: 1, EvictionWindow: metav1.Duration{Duration: 10 * time.Minute},
						Evictions: map[string]api.EvictionRecord{},
					},
				}
				if age > 0 {
					budget.Status.Evictions["existing"] = api.EvictionRecord{
						WorkloadUID: "deployment", NodeUID: "node-00", PodUID: "earlier-pod",
						AttemptedAt: metav1.NewTime(f.now.Add(-age)),
					}
				}
				if err := f.records.Tracker().Add(budget); err != nil {
					t.Fatal(err)
				}
				f.kube.ClearActions()
				f.records.ClearActions()
				err := f.controller.reconcile(context.Background())
				eligible := age == 0 || age > budget.Status.EvictionWindow.Duration
				if eligible {
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil || !strings.Contains(err.Error(), "decrease requires expired accounting") {
					t.Fatalf("unexpired longer-window accounting did not block admission: %v", err)
				}
				if mode == Audit || !eligible {
					if len(mutations(f.kube.Actions())) != 0 || len(mutations(f.records.Actions())) != 0 {
						t.Fatal("audit or retained longer-window history permitted writes")
					}
					if !reflect.DeepEqual(f.ledger(t).Status, budget.Status) {
						t.Fatal("retained accounting was changed")
					}
				} else if evictionCount(f.kube.Actions()) != 1 ||
					len(f.ledger(t).Status.Evictions) != 1 || f.ledger(t).Status.EvictionWindow != f.cfg.EvictionWindow {
					t.Fatal("expired accounting did not admit and record the shorter-window attempt")
				}
			})
		}
	}
}

func TestEmptyLedgerRejectsNodeOwner(t *testing.T) {
	for _, version := range []int{0, 1} {
		for _, mode := range []Mode{Disabled, Audit, Enforce} {
			t.Run(fmt.Sprintf("version=%d/%s", version, mode), func(t *testing.T) {
				f := swiftFixture(t)
				f.cfg.Mode = mode
				if err := f.controller.SetConfig(f.cfg); err != nil {
					t.Fatal(err)
				}
				if err := f.records.Tracker().Add(&api.NodeMitigationBudget{
					ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: "mgmt-agent",
						OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Node", Name: "node-00", UID: "node-00"}}},
					Status: api.NodeMitigationBudgetStatus{Version: version},
				}); err != nil {
					t.Fatal(err)
				}
				f.kube.ClearActions()
				f.records.ClearActions()
				if err := f.controller.reconcile(context.Background()); err == nil || !strings.Contains(err.Error(), "Node owner reference") {
					t.Fatalf("unsafe ledger ownership not reported: %v", err)
				}
				if len(mutations(f.kube.Actions())) != 0 || len(mutations(f.records.Actions())) != 0 {
					t.Fatal("unsafe ledger ownership permitted writes")
				}
			})
		}
	}
}

func TestMalformedLedgerCannotResetHistory(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*api.NodeMitigationBudget, time.Time)
	}{
		{"empty record ID", func(b *api.NodeMitigationBudget, _ time.Time) {
			b.Status.Evictions[""] = b.Status.Evictions["existing"]
			delete(b.Status.Evictions, "existing")
		}},
		{"missing workload UID", func(b *api.NodeMitigationBudget, _ time.Time) {
			record := b.Status.Evictions["existing"]
			record.WorkloadUID = ""
			b.Status.Evictions["existing"] = record
		}},
		{"missing node UID", func(b *api.NodeMitigationBudget, _ time.Time) {
			record := b.Status.Evictions["existing"]
			record.NodeUID = ""
			b.Status.Evictions["existing"] = record
		}},
		{"missing pod UID", func(b *api.NodeMitigationBudget, _ time.Time) {
			record := b.Status.Evictions["existing"]
			record.PodUID = ""
			b.Status.Evictions["existing"] = record
		}},
		{"zero timestamp", func(b *api.NodeMitigationBudget, _ time.Time) {
			record := b.Status.Evictions["existing"]
			record.AttemptedAt = metav1.Time{}
			b.Status.Evictions["existing"] = record
		}},
		{"future timestamp", func(b *api.NodeMitigationBudget, now time.Time) {
			record := b.Status.Evictions["existing"]
			record.AttemptedAt = metav1.NewTime(now.Add(time.Second))
			b.Status.Evictions["existing"] = record
		}},
		{"node owner", func(b *api.NodeMitigationBudget, _ time.Time) {
			b.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "Node", Name: "node-00", UID: "node-00"}}
		}},
		{"node controller owner", func(b *api.NodeMitigationBudget, _ time.Time) {
			b.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "Node", Name: "node-00", UID: "node-00", Controller: ptr.To(true)}}
		}},
	} {
		for _, mode := range []Mode{Disabled, Audit, Enforce} {
			for _, candidates := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/candidates=%v", test.name, mode, candidates), func(t *testing.T) {
					f := swiftFixture(t)
					f.cfg.Mode = mode
					if !candidates {
						if err := f.informers.Core().V1().Events().Informer().GetStore().Replace(nil, ""); err != nil {
							t.Fatal(err)
						}
					}
					if err := f.controller.SetConfig(f.cfg); err != nil {
						t.Fatal(err)
					}
					budget := &api.NodeMitigationBudget{
						ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: "mgmt-agent"},
						Status: api.NodeMitigationBudgetStatus{
							Version: 1, EvictionWindow: f.cfg.EvictionWindow,
							Evictions: map[string]api.EvictionRecord{"existing": {
								WorkloadUID: "deployment", NodeUID: "node-00", PodUID: "earlier-pod",
								AttemptedAt: metav1.NewTime(f.now.Add(-2 * f.cfg.EvictionWindow.Duration)),
							}},
						},
					}
					test.change(budget, f.now)
					if err := f.records.Tracker().Add(budget); err != nil {
						t.Fatal(err)
					}
					f.kube.ClearActions()
					f.records.ClearActions()
					err := f.controller.reconcile(context.Background())
					if err == nil {
						t.Fatal("malformed accounting accepted")
					}
					if !strings.Contains(err.Error(), "accounting") {
						t.Fatalf("unrelated error masked accounting validation: %v", err)
					}
					if len(mutations(f.kube.Actions())) != 0 || len(mutations(f.records.Actions())) != 0 {
						t.Fatal("malformed accounting permitted writes")
					}
					persisted := f.ledger(t)
					if !reflect.DeepEqual(persisted.Status, budget.Status) || !reflect.DeepEqual(persisted.OwnerReferences, budget.OwnerReferences) {
						t.Fatal("malformed accounting was changed")
					}
				})
			}
		}
	}
}
