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

	hcprecoveryv1alpha1 "github.com/Azure/ARO-HCP/hcp-recovery/pkg/apis/hcprecovery/v1alpha1"
)

func TestPauseHostedCluster(t *testing.T) {
	tests := []struct {
		name               string
		recovery           *hcprecoveryv1alpha1.HCPRecovery
		ctrlObjects        []ctrlclient.Object
		expectDone         bool
		expectAction       bool
		expectStatusUpdate bool
		expectPatch        bool
		expectErr          bool
	}{
		{
			name: "already paused - HostedClusterPaused is True",
			recovery: newRecovery(metav1.Condition{
				Type:   hcprecoveryv1alpha1.ConditionHostedClusterPaused,
				Status: metav1.ConditionTrue,
			}),
			expectDone: false,
		},
		{
			name:               "hosted cluster not found - marks condition True",
			recovery:           newRecovery(),
			ctrlObjects:        []ctrlclient.Object{},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name:     "hosted cluster already has PausedUntil=true",
			recovery: newRecovery(),
			ctrlObjects: func() []ctrlclient.Object {
				hc := newTestHostedCluster()
				pausedTrue := "true"
				hc.Spec.PausedUntil = &pausedTrue
				hc.Annotations = map[string]string{"existing": "annotation"}
				return []ctrlclient.Object{hc}
			}(),
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name:     "hosted cluster not paused - patches hosted cluster",
			recovery: newRecovery(),
			ctrlObjects: func() []ctrlclient.Object {
				hc := newTestHostedCluster()
				hc.Annotations = map[string]string{"existing": "annotation"}
				return []ctrlclient.Object{hc}
			}(),
			expectDone:   true,
			expectAction: true,
			expectPatch:  true,
		},
		{
			name:     "hosted cluster not paused with nil annotations - patches without panic",
			recovery: newRecovery(),
			ctrlObjects: func() []ctrlclient.Object {
				hc := newTestHostedCluster()
				// Annotations is nil — verifies the nil map guard
				return []ctrlclient.Object{hc}
			}(),
			expectDone:   true,
			expectAction: true,
			expectPatch:  true,
		},
	}

	t.Run("getHostedCluster error", func(t *testing.T) {
		c := newControllerWithEmptyScheme(nil)
		recovery := newRecovery()

		done, action, _ := c.pauseHostedCluster(context.Background(), recovery)

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
			if tt.ctrlObjects == nil {
				c = newController(nil, nil)
			} else {
				c = newController([]runtime.Object{}, tt.ctrlObjects)
			}

			done, action, err := c.pauseHostedCluster(context.Background(), tt.recovery)

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
				if tt.expectPatch && action.PatchHostedCluster == nil {
					t.Error("expected PatchHostedCluster action, got nil")
				}
				if !tt.expectPatch && action.PatchHostedCluster != nil {
					t.Error("expected no PatchHostedCluster action, got one")
				}
			}
		})
	}
}
