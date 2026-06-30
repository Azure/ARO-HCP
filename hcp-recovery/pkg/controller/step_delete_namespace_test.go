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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	hcprecoveryv1alpha1 "github.com/Azure/ARO-HCP/hcp-recovery/pkg/apis/hcprecovery/v1alpha1"
)

func TestDeleteNamespace(t *testing.T) {
	tests := []struct {
		name               string
		recovery           *hcprecoveryv1alpha1.HCPRecovery
		kubeObjects        []runtime.Object
		ctrlObjects        []ctrlclient.Object
		expectDone         bool
		expectAction       bool
		expectStatusUpdate bool
		expectDeleteNs     bool
		expectErr          bool
	}{
		{
			name: "already completed - NamespaceFullyRemoved is True",
			recovery: newRecovery(metav1.Condition{
				Type:   hcprecoveryv1alpha1.ConditionNamespaceFullyRemoved,
				Status: metav1.ConditionTrue,
			}),
			expectDone: false,
		},
		{
			name: "already completed - HCPNamespaceDeleted is True",
			recovery: newRecovery(metav1.Condition{
				Type:   hcprecoveryv1alpha1.ConditionHCPNamespaceDeleted,
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
			name:     "namespace terminating - marks condition True",
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
			name:     "namespace active - returns delete action",
			recovery: newRecovery(),
			ctrlObjects: []ctrlclient.Object{
				newTestHostedCluster(),
			},
			kubeObjects: []runtime.Object{
				newActiveNamespace(testHCPNamespace),
			},
			expectDone:     true,
			expectAction:   true,
			expectDeleteNs: true,
		},
	}

	t.Run("getHostedCluster error", func(t *testing.T) {
		c := newControllerWithEmptyScheme(nil)
		recovery := newRecovery()

		done, action, _ := c.deleteHcpNamespace(context.Background(), recovery)

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

	t.Run("namespace get non-NotFound error", func(t *testing.T) {
		c := newController([]runtime.Object{}, []ctrlclient.Object{newTestHostedCluster()})
		fakeClient := c.kubeClient.(*kubefake.Clientset)
		fakeClient.PrependReactor("get", "namespaces", func(action clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, fmt.Errorf("internal server error")
		})

		recovery := newRecovery()
		done, action, _ := c.deleteHcpNamespace(context.Background(), recovery)

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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c *HCPRecoveryController
			if tt.ctrlObjects == nil && tt.kubeObjects == nil {
				c = newController(nil, nil)
			} else {
				c = newController(tt.kubeObjects, tt.ctrlObjects)
			}

			done, action, err := c.deleteHcpNamespace(context.Background(), tt.recovery)

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
				if tt.expectDeleteNs && action.DeleteNamespace == nil {
					t.Error("expected DeleteNamespace action, got nil")
				}
				if !tt.expectDeleteNs && action.DeleteNamespace != nil {
					t.Error("expected no DeleteNamespace action, got one")
				}
			}
		})
	}
}

func TestWaitForHcpNamespaceDeletion(t *testing.T) {
	tests := []struct {
		name               string
		recovery           *hcprecoveryv1alpha1.HCPRecovery
		kubeObjects        []runtime.Object
		ctrlObjects        []ctrlclient.Object
		expectDone         bool
		expectAction       bool
		expectStatusUpdate bool
		expectErr          bool
	}{
		{
			name: "already completed - NamespaceFullyRemoved is True",
			recovery: newRecovery(metav1.Condition{
				Type:   hcprecoveryv1alpha1.ConditionNamespaceFullyRemoved,
				Status: metav1.ConditionTrue,
			}),
			expectDone: false,
		},
		{
			name: "already completed - VeleroRestoreCompleted is True",
			recovery: newRecovery(metav1.Condition{
				Type:   hcprecoveryv1alpha1.ConditionVeleroRestoreCompleted,
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
			name:     "namespace still exists - requeues",
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
	}

	t.Run("getHostedCluster error", func(t *testing.T) {
		c := newControllerWithEmptyScheme(nil)
		recovery := newRecovery()

		done, action, _ := c.waitForHcpNamespaceDeletion(context.Background(), recovery)

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

	t.Run("namespace get non-NotFound error", func(t *testing.T) {
		c := newController([]runtime.Object{}, []ctrlclient.Object{newTestHostedCluster()})
		fakeClient := c.kubeClient.(*kubefake.Clientset)
		fakeClient.PrependReactor("get", "namespaces", func(action clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, fmt.Errorf("internal server error")
		})

		recovery := newRecovery()
		done, action, _ := c.waitForHcpNamespaceDeletion(context.Background(), recovery)

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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c *HCPRecoveryController
			if tt.ctrlObjects == nil && tt.kubeObjects == nil {
				c = newController(nil, nil)
			} else {
				c = newController(tt.kubeObjects, tt.ctrlObjects)
			}

			done, action, err := c.waitForHcpNamespaceDeletion(context.Background(), tt.recovery)

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
			}
		})
	}
}
