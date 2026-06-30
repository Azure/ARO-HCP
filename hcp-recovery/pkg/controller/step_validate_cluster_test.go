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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	hcprecoveryv1alpha1 "github.com/Azure/ARO-HCP/hcp-recovery/pkg/apis/hcprecovery/v1alpha1"
)

func TestValidateHostedCluster(t *testing.T) {
	tests := []struct {
		name               string
		recovery           *hcprecoveryv1alpha1.HCPRecovery
		ctrlObjects        []ctrlclient.Object
		expectDone         bool
		expectAction       bool
		expectStatusUpdate bool
		expectErr          bool
	}{
		{
			name: "already completed - HealthChecked is True",
			recovery: newRecovery(metav1.Condition{
				Type:               hcprecoveryv1alpha1.ConditionHealthChecked,
				Status:             metav1.ConditionTrue,
				Reason:             "HealthCheckPassed",
				Message:            "Post-restore health checks have passed",
				ObservedGeneration: 1,
			}),
			expectDone: false,
		},
		{
			name:               "hosted cluster not found",
			recovery:           newRecovery(),
			ctrlObjects:        []ctrlclient.Object{},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name:     "hosted cluster available",
			recovery: newRecovery(),
			ctrlObjects: func() []ctrlclient.Object {
				hc := newTestHostedCluster()
				hc.Status.Conditions = []metav1.Condition{
					{Type: string(v1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue},
				}
				return []ctrlclient.Object{hc}
			}(),
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name:     "hosted cluster not available",
			recovery: newRecovery(),
			ctrlObjects: func() []ctrlclient.Object {
				hc := newTestHostedCluster()
				hc.Status.Conditions = []metav1.Condition{
					{Type: string(v1beta1.HostedClusterAvailable), Status: metav1.ConditionFalse},
				}
				return []ctrlclient.Object{hc}
			}(),
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name:     "available condition not found",
			recovery: newRecovery(),
			ctrlObjects: func() []ctrlclient.Object {
				hc := newTestHostedCluster()
				return []ctrlclient.Object{hc}
			}(),
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
	}

	// This test requires a special controller setup so it runs outside the table.
	t.Run("getHostedCluster error", func(t *testing.T) {
		// An empty scheme causes ctrlClient.List for HostedClusterList to fail
		c := newControllerWithEmptyScheme(nil)
		recovery := newRecovery()

		done, action, err := c.validateHostedCluster(context.Background(), recovery)

		if done {
			t.Error("expected done=false")
		}
		if action != nil {
			t.Fatalf("expected no action, got %+v", action)
		}
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c *HCPRecoveryController
			if tt.ctrlObjects == nil {
				c = newController(nil, nil)
			} else {
				c = newController([]runtime.Object{}, tt.ctrlObjects)
			}

			done, action, err := c.validateHostedCluster(context.Background(), tt.recovery)

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
