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
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"

	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

func TestMachineBirthRequiredBeforeCordonAndSubmission(t *testing.T) {
	for _, afterCordon := range []bool{false, true} {
		for _, cause := range []string{"missing", "future", "after Node", "before observer"} {
			t.Run(fmt.Sprintf("afterCordon=%v/%s", afterCordon, cause), func(t *testing.T) {
				f := newFixture(t, 11, 1)
				boundary := f.now.Add(-3 * time.Hour)
				f.controller.readiness = func(_ *corev1.Node, createdAt time.Time) error {
					if createdAt != f.azure.createdAt {
						t.Fatal("readiness observer did not receive the verified VM creation time")
					}
					if !createdAt.After(boundary) {
						return errors.New("VM predates complete observation")
					}
					return nil
				}
				if afterCordon {
					f.tick(t)
				}
				switch cause {
				case "missing":
					f.azure.createdAt = time.Time{}
				case "future":
					f.azure.createdAt = f.now.Add(time.Hour)
				case "after Node":
					f.azure.createdAt = f.now.Add(-time.Minute)
				case "before observer":
					f.azure.createdAt = boundary.Add(-time.Minute)
				}
				f.syncCaches(t)
				if err := f.controller.reconcile(context.Background()); (err == nil) != afterCordon {
					t.Fatalf("afterCordon=%v, reconcile error=%v", afterCordon, err)
				}
				if f.azure.deletes != 0 {
					t.Fatal("invalid machine birth authorized AKS deletion")
				}
				node, err := f.kube.CoreV1().Nodes().Get(context.Background(), "node-00", metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if node.Spec.Unschedulable || node.Annotations[cordonAnnotation] != "" {
					t.Fatal("invalid machine lifetime left an unsubmitted owned cordon")
				}
			})
		}
	}
}

func (f *fixture) node(t *testing.T) *corev1.Node {
	t.Helper()
	node, err := f.kube.CoreV1().Nodes().Get(context.Background(), "node-00", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func (f *fixture) changeNode(t *testing.T, change func(*corev1.Node)) {
	t.Helper()
	node := f.node(t)
	change(node)
	if _, err := f.kube.CoreV1().Nodes().Update(context.Background(), node, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestCordonCancellationContract(t *testing.T) {
	for _, cause := range []string{"Ready", "history lost on restart", "mitigator disabled", "blocking pod", "terminal finalizer", "terminal PVC", "Node replaced", "claim never applied"} {
		t.Run(cause, func(t *testing.T) {
			f := newFixture(t, 11, 1)
			f.tick(t)
			key := recordName("node-00")
			if !ownsCordon(f.node(t), f.ledger(t).Status.Reservations[key]) || f.azure.deletes != 0 {
				t.Fatal("claim was not separate from submission")
			}
			switch cause {
			case "Ready":
				f.changeNode(t, func(n *corev1.Node) { n.Status.Conditions[0].Status = corev1.ConditionTrue })
			case "history lost on restart":
				f.controller.readiness = func(*corev1.Node, time.Time) error { return errors.New("history unknown") }
			case "mitigator disabled":
				f.cfg.Mitigators = []string{"swift"}
				if err := f.controller.SetConfig(f.cfg); err != nil {
					t.Fatal(err)
				}
			case "blocking pod", "terminal finalizer", "terminal PVC":
				pod := placementPod("late", "node-00")
				if cause == "terminal finalizer" {
					pod.Status.Phase = corev1.PodSucceeded
					pod.Finalizers = []string{"protect"}
				}
				if cause == "terminal PVC" {
					pod.Status.Phase = corev1.PodFailed
					pod.Spec.Volumes = []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"}}}}
				}
				if err := f.kube.Tracker().Add(pod); err != nil {
					t.Fatal(err)
				}
			case "Node replaced":
				f.changeNode(t, func(n *corev1.Node) {
					n.UID = "replacement"
					n.Spec.Unschedulable = false
					delete(n.Labels, ownershipLabel)
					delete(n.Annotations, cordonAnnotation)
				})
			case "claim never applied":
				f.changeNode(t, func(n *corev1.Node) {
					n.Spec.Unschedulable = false
					delete(n.Labels, ownershipLabel)
					delete(n.Annotations, cordonAnnotation)
				})
			}
			f.tick(t)
			r, node := f.ledger(t).Status.Reservations[key], f.node(t)
			if r.Outcome != "Cancelled" || r.ReleasedAt == nil || node.Spec.Unschedulable ||
				node.Annotations[cordonAnnotation] != "" || f.azure.deletes != 0 {
				t.Fatalf("cancellation did not release safely: %+v node=%+v", r, node)
			}
		})
	}
}

func TestCordonTakeoverAndIdentityHold(t *testing.T) {
	for _, takeover := range []string{"annotation removed", "annotation changed", "label changed", "provider changed", "instance changed"} {
		t.Run(takeover, func(t *testing.T) {
			f := newFixture(t, 11, 1)
			f.tick(t)
			f.changeNode(t, func(n *corev1.Node) {
				n.Status.Conditions[0].Status = corev1.ConditionTrue
				switch takeover {
				case "annotation removed":
					delete(n.Annotations, cordonAnnotation)
				case "annotation changed":
					n.Annotations[cordonAnnotation] = "operator"
				case "label changed":
					n.Labels[ownershipLabel] = "operator"
				case "provider changed":
					n.Spec.ProviderID = "azure://replacement"
				case "instance changed":
					n.Status.NodeInfo.SystemUUID = "replacement"
				}
			})
			f.kube.ClearActions()
			if err := f.controller.reconcile(context.Background()); err == nil {
				t.Fatal("ownership hold was not reported")
			}
			if !f.node(t).Spec.Unschedulable || len(mutations(f.kube.Actions())) != 0 ||
				f.ledger(t).Status.Reservations[recordName("node-00")].ReleasedAt != nil || f.azure.deletes != 0 {
				t.Fatal("takeover or identity change allowed uncordon/deletion")
			}
		})
	}
}

func TestCordonReleaseConflictAndCrashRecovery(t *testing.T) {
	for _, conflict := range []bool{true, false} {
		t.Run(fmt.Sprint(conflict), func(t *testing.T) {
			f := newFixture(t, 11, 1)
			f.tick(t)
			f.changeNode(t, func(n *corev1.Node) { n.Status.Conditions[0].Status = corev1.ConditionTrue })
			if conflict {
				f.kube.PrependReactor("patch", "nodes", func(ktesting.Action) (bool, runtime.Object, error) {
					resource := corev1.SchemeGroupVersion.WithResource("nodes")
					obj, err := f.kube.Tracker().Get(resource, "", "node-00")
					if err != nil {
						return true, nil, err
					}
					node := obj.(*corev1.Node)
					node.ResourceVersion = "concurrent-maintenance"
					if err := f.kube.Tracker().Update(resource, node, ""); err != nil {
						return true, nil, err
					}
					return false, nil, nil
				})
			} else {
				f.records.PrependReactor("update", "nodemitigationbudgets", func(action ktesting.Action) (bool, runtime.Object, error) {
					for _, r := range action.(ktesting.UpdateAction).GetObject().(*api.NodeMitigationBudget).Status.Reservations {
						if r.Outcome == "Cancelled" {
							return true, nil, errors.New("lost cancellation status")
						}
					}
					return false, nil, nil
				})
			}
			if err := f.controller.reconcile(context.Background()); err == nil {
				t.Fatal("failed release or accounting write was hidden")
			}
			if f.node(t).Spec.Unschedulable != conflict || f.azure.deletes != 0 ||
				f.ledger(t).Status.Reservations[recordName("node-00")].ReleasedAt != nil {
				t.Fatal("failure lost ownership or reservation safety")
			}
			if !conflict {
				f.records.ReactionChain = f.records.ReactionChain[1:]
				f.tick(t)
				if f.ledger(t).Status.Reservations[recordName("node-00")].Outcome != "Cancelled" {
					t.Fatal("restart after release did not finish cancellation")
				}
			}
		})
	}
}

func TestAttemptedDeletionNeverUncordons(t *testing.T) {
	for _, outcome := range []string{"Unknown", "Pending", "Succeeded", "Failed"} {
		t.Run(outcome, func(t *testing.T) {
			f := newFixture(t, 11, 1)
			f.tick(t)
			f.tick(t)
			key := recordName("node-00")
			b := f.ledger(t)
			r := b.Status.Reservations[key]
			r.Outcome = outcome
			b.Status.Reservations[key] = r
			f.changeNode(t, func(n *corev1.Node) { n.Status.Conditions[0].Status = corev1.ConditionTrue })
			f.kube.ClearActions()
			_, revision := f.controller.configuration()
			if err := f.controller.cancelReservation(context.Background(), revision, key, b, errors.New("recovered")); err == nil {
				t.Fatal("attempted deletion was cancelled")
			}
			if !f.node(t).Spec.Unschedulable || len(mutations(f.kube.Actions())) != 0 {
				t.Fatal("attempted deletion uncordoned a recovering Node")
			}
		})
	}
}

func TestMissingReadinessAndExternalCordonNeverClaim(t *testing.T) {
	for _, missing := range []string{"observer", "history", "external cordon", "foreign annotation", "everReady"} {
		t.Run(missing, func(t *testing.T) {
			f := newFixture(t, 11, 1)
			switch missing {
			case "observer":
				f.controller.readiness = nil
			case "history", "everReady":
				f.controller.readiness = func(*corev1.Node, time.Time) error { return errors.New(missing) }
			case "external cordon":
				f.changeNode(t, func(n *corev1.Node) { n.Spec.Unschedulable = true })
			case "foreign annotation":
				f.changeNode(t, func(n *corev1.Node) { n.Annotations = map[string]string{cordonAnnotation: "operator"} })
			}
			f.syncCaches(t)
			f.kube.ClearActions()
			err := f.controller.reconcile(context.Background())
			if missing != "external cordon" && err == nil {
				t.Fatal("missing prerequisite was not reported")
			}
			if len(mutations(f.kube.Actions())) != 0 || f.azure.deletes != 0 || len(f.ledger(t).Status.Reservations) != 0 {
				t.Fatal("missing prerequisite allowed a claim")
			}
		})
	}
}

func TestPausedCordonMakesNoWrites(t *testing.T) {
	for _, mode := range []Mode{Audit, Disabled} {
		t.Run(string(mode), func(t *testing.T) {
			f := newFixture(t, 11, 1)
			f.tick(t)
			f.cfg.Mode = mode
			if err := f.controller.SetConfig(f.cfg); err != nil {
				t.Fatal(err)
			}
			f.kube.ClearActions()
			f.records.ClearActions()
			if err := f.controller.reconcile(context.Background()); err == nil {
				t.Fatal("paused cordon hold was not reported")
			}
			if len(mutations(f.kube.Actions())) != 0 || len(mutations(f.records.Actions())) != 0 || f.azure.deletes != 0 {
				t.Fatal("paused mode mutated an owned cordon")
			}
		})
	}
}

func TestUncertainAttemptWithoutTimestampIsNotSubmitted(t *testing.T) {
	for _, outcome := range []string{"Unknown", "Pending", "token-only"} {
		t.Run(outcome, func(t *testing.T) {
			f := newFixture(t, 11, 1)
			f.tick(t)
			b := f.ledger(t)
			key := recordName("node-00")
			r := b.Status.Reservations[key]
			if outcome == "token-only" {
				r.OperationToken = "uncertain"
			} else {
				r.Outcome = outcome
			}
			b.Status.Reservations[key] = r
			if _, err := f.records.MgmtagentV1alpha1().NodeMitigationBudgets("mgmt-agent").UpdateStatus(context.Background(), b, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			f.kube.ClearActions()
			if err := f.controller.reconcile(context.Background()); err == nil {
				t.Fatal("uncertain operation was not reported")
			}
			if f.azure.deletes != 0 || len(mutations(f.kube.Actions())) != 0 || !f.node(t).Spec.Unschedulable {
				t.Fatal("uncertain accounting allowed deletion or release")
			}
		})
	}
}

func TestHeldCordonDoesNotStopSwift(t *testing.T) {
	f := swiftFixture(t)
	cfg, revision := f.controller.configuration()
	b, err := f.controller.budget(context.Background(), revision, cfg.Mode)
	if err != nil {
		t.Fatal(err)
	}
	node, err := f.kube.CoreV1().Nodes().Get(context.Background(), "node-02", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	node.Spec.Unschedulable = true
	if _, err := f.kube.CoreV1().Nodes().Update(context.Background(), node, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	b.Status.Reservations["held"] = api.MitigationReservation{NodeName: node.Name, NodeUID: node.UID,
		ProviderID: node.Spec.ProviderID, InstanceID: node.Status.NodeInfo.SystemUUID, Zone: "zone",
		PoolID: cfg.ClusterResourceID + "/agentPools/pool"}
	if err := f.controller.saveBudget(context.Background(), revision, b); err != nil {
		t.Fatal(err)
	}
	if err := f.controller.reconcile(context.Background()); err == nil {
		t.Fatal("operator-owned cordon hold was not reported")
	}
	if evictionCount(f.kube.Actions()) != 1 || f.azure.deletes != 0 {
		t.Fatal("unresolved never-ready ownership blocked independent SWIFT rescue")
	}
}
