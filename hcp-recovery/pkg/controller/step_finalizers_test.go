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

package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	hcprecoveryv1alpha1 "github.com/Azure/ARO-HCP/hcp-recovery/pkg/apis/hcprecovery/v1alpha1"
)

const (
	testClusterId    = "test-cluster-id"
	testHCName       = "test-hc"
	testHCNamespace  = "ocm-test"
	testHCPNamespace = "ocm-test-test-hc"
	testRecoveryName = "test-recovery"
	testRecoveryNS   = "hcp-recovery"
)

func newTestHostedCluster() *v1beta1.HostedCluster {
	return &v1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testHCName,
			Namespace: testHCNamespace,
			Labels:    map[string]string{"api.openshift.com/id": testClusterId},
		},
	}
}

func newTerminatingNamespace(name string) *v1.Namespace {
	return &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     v1.NamespaceStatus{Phase: v1.NamespaceTerminating},
	}
}

func newActiveNamespace(name string) *v1.Namespace {
	return &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     v1.NamespaceStatus{Phase: v1.NamespaceActive},
	}
}

func newDeployment(namespace, name string, finalizers []string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  namespace,
			Finalizers: finalizers,
		},
		Status: appsv1.DeploymentStatus{
			Replicas: replicas,
		},
	}
}

func newRecovery(conditions ...metav1.Condition) *hcprecoveryv1alpha1.HCPRecovery {
	return &hcprecoveryv1alpha1.HCPRecovery{
		ObjectMeta: metav1.ObjectMeta{
			Name:       testRecoveryName,
			Namespace:  testRecoveryNS,
			Generation: 1,
		},
		Spec: hcprecoveryv1alpha1.HCPRecoverySpec{
			ClusterId: testClusterId,
		},
		Status: hcprecoveryv1alpha1.HCPRecoveryStatus{
			Conditions: conditions,
		},
	}
}

func newController(kubeObjects []runtime.Object, ctrlObjects []ctrlclient.Object) *HCPRecoveryController {
	scheme := runtime.NewScheme()
	_ = v1beta1.AddToScheme(scheme)

	ctrlBuilder := ctrlfake.NewClientBuilder().WithScheme(scheme)
	if ctrlObjects != nil {
		ctrlBuilder = ctrlBuilder.WithObjects(ctrlObjects...)
	}

	return &HCPRecoveryController{
		kubeClient: kubefake.NewClientset(kubeObjects...),
		ctrlClient: ctrlBuilder.Build(),
	}
}

// newControllerWithEmptyScheme creates a controller whose ctrlClient has no
// types registered, causing List calls to fail. This is used to test the
// getHostedCluster error path.
func newControllerWithEmptyScheme(kubeObjects []runtime.Object) *HCPRecoveryController {
	return &HCPRecoveryController{
		kubeClient: kubefake.NewClientset(kubeObjects...),
		ctrlClient: ctrlfake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build(),
	}
}

func TestRemoveDeploymentResourceFinalizers(t *testing.T) {
	tests := []struct {
		name               string
		recovery           *hcprecoveryv1alpha1.HCPRecovery
		kubeObjects        []runtime.Object
		ctrlObjects        []ctrlclient.Object
		expectDone         bool
		expectAction       bool
		expectErr          bool
		expectStatusUpdate bool
		expectRemovals     bool
	}{
		{
			name: "already completed - DeploymentFinalizersRemoved is True",
			recovery: newRecovery(metav1.Condition{
				Type:   hcprecoveryv1alpha1.ConditionDeploymentFinalizersRemoved,
				Status: metav1.ConditionTrue,
			}),
			expectDone: false,
		},
		{
			name: "already completed - NamespaceFullyRemoved is True",
			recovery: newRecovery(metav1.Condition{
				Type:   hcprecoveryv1alpha1.ConditionNamespaceFullyRemoved,
				Status: metav1.ConditionTrue,
			}),
			expectDone: false,
		},
		{
			name:               "hosted cluster not found - marks condition True",
			recovery:           newRecovery(),
			ctrlObjects:        []ctrlclient.Object{},
			kubeObjects:        []runtime.Object{},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name:     "namespace not found - marks condition True",
			recovery: newRecovery(),
			ctrlObjects: []ctrlclient.Object{
				newTestHostedCluster(),
			},
			kubeObjects:        []runtime.Object{},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name:     "namespace not terminating - permanent error",
			recovery: newRecovery(),
			ctrlObjects: []ctrlclient.Object{
				newTestHostedCluster(),
			},
			kubeObjects: []runtime.Object{
				newActiveNamespace(testHCPNamespace),
			},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name:     "no deployments exist - marks condition True",
			recovery: newRecovery(),
			ctrlObjects: []ctrlclient.Object{
				newTestHostedCluster(),
			},
			kubeObjects: []runtime.Object{
				newTerminatingNamespace(testHCPNamespace),
			},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name:     "deployments without finalizers - marks condition True",
			recovery: newRecovery(),
			ctrlObjects: []ctrlclient.Object{
				newTestHostedCluster(),
			},
			kubeObjects: []runtime.Object{
				newTerminatingNamespace(testHCPNamespace),
				newDeployment(testHCPNamespace, "cluster-api", nil, 0),
				newDeployment(testHCPNamespace, "capi-provider", nil, 0),
			},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name:     "deployments with finalizers and zero replicas - returns removals",
			recovery: newRecovery(),
			ctrlObjects: []ctrlclient.Object{
				newTestHostedCluster(),
			},
			kubeObjects: []runtime.Object{
				newTerminatingNamespace(testHCPNamespace),
				newDeployment(testHCPNamespace, "cluster-api", []string{"some-finalizer"}, 0),
				newDeployment(testHCPNamespace, "capi-provider", []string{"another-finalizer"}, 0),
			},
			expectDone:     true,
			expectAction:   true,
			expectRemovals: true,
		},
		{
			name:     "deployment with finalizers and non-zero replicas - returns error and requeues",
			recovery: newRecovery(),
			ctrlObjects: []ctrlclient.Object{
				newTestHostedCluster(),
			},
			kubeObjects: []runtime.Object{
				newTerminatingNamespace(testHCPNamespace),
				newDeployment(testHCPNamespace, "cluster-api", []string{"some-finalizer"}, 2),
			},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name:     "one deployment scaled down, one still has replicas - returns error",
			recovery: newRecovery(),
			ctrlObjects: []ctrlclient.Object{
				newTestHostedCluster(),
			},
			kubeObjects: []runtime.Object{
				newTerminatingNamespace(testHCPNamespace),
				newDeployment(testHCPNamespace, "cluster-api", []string{"some-finalizer"}, 0),
				newDeployment(testHCPNamespace, "capi-provider", []string{"some-finalizer"}, 1),
			},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
	}

	// These tests require special controller setup so they run outside the table.
	t.Run("getHostedCluster error - retryable error with status update", func(t *testing.T) {
		// An empty scheme causes ctrlClient.List for HostedClusterList to fail
		c := newControllerWithEmptyScheme(nil)
		recovery := newRecovery()

		done, action, _ := c.removeDeploymentResourceFinalizers(context.Background(), recovery)

		if !done {
			t.Error("expected done=true")
		}
		if action == nil {
			t.Fatal("expected action, got nil")
		}
		if action.StatusUpdate == nil {
			t.Fatal("expected StatusUpdate for error condition")
		}
	})

	t.Run("namespace get non-NotFound error - retryable error with status update", func(t *testing.T) {
		c := newController([]runtime.Object{}, []ctrlclient.Object{newTestHostedCluster()})
		// Inject a reactor that returns a server error for namespace Get
		fakeClient := c.kubeClient.(*kubefake.Clientset)
		fakeClient.PrependReactor("get", "namespaces", func(action clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, fmt.Errorf("internal server error")
		})

		recovery := newRecovery()
		done, action, _ := c.removeDeploymentResourceFinalizers(context.Background(), recovery)

		if !done {
			t.Error("expected done=true")
		}
		if action == nil {
			t.Fatal("expected action, got nil")
		}
		if action.StatusUpdate == nil {
			t.Fatal("expected StatusUpdate for error condition")
		}
	})

	t.Run("hostedCluster nil with condition already set - needsUpdate false", func(t *testing.T) {
		recovery := newRecovery(metav1.Condition{
			Type:               hcprecoveryv1alpha1.ConditionDeploymentFinalizersRemoved,
			Status:             metav1.ConditionFalse,
			Reason:             "SomePreviousError",
			Message:            "some previous error",
			ObservedGeneration: 1,
		})
		// No HostedCluster in ctrlClient → hostedCluster == nil
		c := newController(nil, []ctrlclient.Object{})

		done, action, err := c.removeDeploymentResourceFinalizers(context.Background(), recovery)

		// The condition is False (not True), so the early-return doesn't fire.
		// hostedCluster == nil → sets DeploymentFinalizersRemoved=True → needsUpdate=true
		if !done {
			t.Error("expected done=true")
		}
		if action == nil {
			t.Fatal("expected action")
		}
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c *HCPRecoveryController
			if tt.ctrlObjects == nil && tt.kubeObjects == nil {
				// For early-return cases that don't need clients
				c = newController(nil, nil)
			} else {
				c = newController(tt.kubeObjects, tt.ctrlObjects)
			}

			done, action, err := c.removeDeploymentResourceFinalizers(context.Background(), tt.recovery)

			if done != tt.expectDone {
				t.Errorf("expected done=%v, got %v", tt.expectDone, done)
			}

			if tt.expectAction && action == nil {
				t.Fatal("expected action, got nil")
			}
			if !tt.expectAction && action != nil {
				t.Fatalf("expected no action, got %+v", action)
			}

			if tt.expectErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if action != nil {
				if tt.expectStatusUpdate && action.StatusUpdate == nil {
					t.Error("expected StatusUpdate action, got nil")
				}
				if tt.expectRemovals && len(action.RemoveDeploymentResourceFinalizers) == 0 {
					t.Error("expected RemoveDeploymentResourceFinalizers, got none")
				}
				if !tt.expectRemovals && len(action.RemoveDeploymentResourceFinalizers) > 0 {
					t.Error("expected no RemoveDeploymentResourceFinalizers, got some")
				}
			}
		})
	}
}

func TestRemoveDeploymentResourceFinalizers_NonZeroReplicasNeverMarksTrue(t *testing.T) {
	// This test verifies the bug fix: when deployments have finalizers but
	// replicas > 0, the step must NOT set DeploymentFinalizersRemoved=True.
	c := newController(
		[]runtime.Object{
			newTerminatingNamespace(testHCPNamespace),
			newDeployment(testHCPNamespace, "cluster-api", []string{"some-finalizer"}, 3),
		},
		[]ctrlclient.Object{
			newTestHostedCluster(),
		},
	)

	recovery := newRecovery()
	done, action, _ := c.removeDeploymentResourceFinalizers(context.Background(), recovery)

	if !done {
		t.Fatal("expected done=true (step should signal it needs requeue)")
	}
	if action == nil {
		t.Fatal("expected action with status update for the error condition")
	}
	if len(action.RemoveDeploymentResourceFinalizers) > 0 {
		t.Fatal("must not return finalizer removals when replicas > 0")
	}

	// The status update must NOT contain DeploymentFinalizersRemoved=True
	if action.StatusUpdate != nil && action.StatusUpdate.Status != nil {
		for _, cond := range action.StatusUpdate.Status.Conditions {
			if cond.Type != nil && *cond.Type == hcprecoveryv1alpha1.ConditionDeploymentFinalizersRemoved {
				if cond.Status != nil && *cond.Status == metav1.ConditionTrue {
					t.Fatal("BUG: DeploymentFinalizersRemoved must not be True when replicas > 0")
				}
			}
		}
	}
}

func TestCollectDeploymentFinalizerRemovals(t *testing.T) {
	tests := []struct {
		name           string
		kubeObjects    []runtime.Object
		expectRemovals int
		expectErr      bool
	}{
		{
			name:           "no deployments",
			kubeObjects:    []runtime.Object{},
			expectRemovals: 0,
		},
		{
			name: "deployments without finalizers",
			kubeObjects: []runtime.Object{
				newDeployment(testHCPNamespace, "cluster-api", nil, 0),
				newDeployment(testHCPNamespace, "capi-provider", nil, 0),
			},
			expectRemovals: 0,
		},
		{
			name: "both deployments with finalizers and zero replicas",
			kubeObjects: []runtime.Object{
				newDeployment(testHCPNamespace, "cluster-api", []string{"f1"}, 0),
				newDeployment(testHCPNamespace, "capi-provider", []string{"f2"}, 0),
			},
			expectRemovals: 2,
		},
		{
			name: "one deployment with finalizers and zero replicas",
			kubeObjects: []runtime.Object{
				newDeployment(testHCPNamespace, "cluster-api", []string{"f1"}, 0),
				newDeployment(testHCPNamespace, "capi-provider", nil, 0),
			},
			expectRemovals: 1,
		},
		{
			name: "deployment with finalizers and non-zero replicas returns error",
			kubeObjects: []runtime.Object{
				newDeployment(testHCPNamespace, "cluster-api", []string{"f1"}, 1),
			},
			expectErr: true,
		},
		{
			name: "first deployment ok, second has non-zero replicas returns error",
			kubeObjects: []runtime.Object{
				newDeployment(testHCPNamespace, "cluster-api", []string{"f1"}, 0),
				newDeployment(testHCPNamespace, "capi-provider", []string{"f2"}, 1),
			},
			expectErr: true,
		},
		{
			name: "unrelated deployment is ignored",
			kubeObjects: []runtime.Object{
				newDeployment(testHCPNamespace, "unrelated-deployment", []string{"f1"}, 5),
			},
			expectRemovals: 0,
		},
	}

	t.Run("deployment get non-NotFound error returns error", func(t *testing.T) {
		c := newController(nil, nil)
		fakeClient := c.kubeClient.(*kubefake.Clientset)
		fakeClient.PrependReactor("get", "deployments", func(action clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, fmt.Errorf("internal server error")
		})

		_, err := c.collectDeploymentFinalizerRemovals(context.Background(), testHCPNamespace)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newController(tt.kubeObjects, nil)

			removals, err := c.collectDeploymentFinalizerRemovals(context.Background(), testHCPNamespace)

			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(removals) != tt.expectRemovals {
				t.Errorf("expected %d removals, got %d", tt.expectRemovals, len(removals))
			}

			for _, r := range removals {
				if len(r.object.GetFinalizers()) != 0 {
					t.Errorf("object %s should have finalizers cleared", r.object.GetName())
				}
				if len(r.base.GetFinalizers()) == 0 {
					t.Errorf("base %s should still have original finalizers", r.base.GetName())
				}
			}
		})
	}
}
