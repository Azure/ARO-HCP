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

	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	hcprecoveryv1alpha1 "github.com/Azure/ARO-HCP/hcp-recovery/pkg/apis/hcprecovery/v1alpha1"
)

func TestCreateVeleroRestore(t *testing.T) {
	const testRestoreName = "restore-test-recovery-abcd1234"

	tests := []struct {
		name                      string
		recovery                  *hcprecoveryv1alpha1.HCPRecovery
		ctrlObjects               []ctrlclient.Object
		expectDone                bool
		expectAction              bool
		expectStatusUpdate        bool
		expectCreateVeleroRestore bool
		expectErr                 bool
	}{
		{
			name: "restore name not yet persisted - persists to status",
			recovery: func() *hcprecoveryv1alpha1.HCPRecovery {
				r := newRecovery()
				r.Spec.BackupId = testBackupId
				r.UID = "abcd1234-0000-0000-0000-000000000000"
				return r
			}(),
			ctrlObjects:        []ctrlclient.Object{},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name: "restore exists and completed",
			recovery: func() *hcprecoveryv1alpha1.HCPRecovery {
				r := newRecovery()
				r.Spec.BackupId = testBackupId
				r.Status.RestoreName = testRestoreName
				return r
			}(),
			ctrlObjects: []ctrlclient.Object{
				&velerov1api.Restore{
					ObjectMeta: metav1.ObjectMeta{Name: testRestoreName, Namespace: "velero"},
					Status:     velerov1api.RestoreStatus{Phase: velerov1api.RestorePhaseCompleted},
				},
			},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name: "restore exists and failed",
			recovery: func() *hcprecoveryv1alpha1.HCPRecovery {
				r := newRecovery()
				r.Spec.BackupId = testBackupId
				r.Status.RestoreName = testRestoreName
				return r
			}(),
			ctrlObjects: []ctrlclient.Object{
				&velerov1api.Restore{
					ObjectMeta: metav1.ObjectMeta{Name: testRestoreName, Namespace: "velero"},
					Status:     velerov1api.RestoreStatus{Phase: velerov1api.RestorePhaseFailed},
				},
			},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name: "restore exists and partially failed",
			recovery: func() *hcprecoveryv1alpha1.HCPRecovery {
				r := newRecovery()
				r.Spec.BackupId = testBackupId
				r.Status.RestoreName = testRestoreName
				return r
			}(),
			ctrlObjects: []ctrlclient.Object{
				&velerov1api.Restore{
					ObjectMeta: metav1.ObjectMeta{Name: testRestoreName, Namespace: "velero"},
					Status:     velerov1api.RestoreStatus{Phase: velerov1api.RestorePhasePartiallyFailed},
				},
			},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name: "restore exists and in progress",
			recovery: func() *hcprecoveryv1alpha1.HCPRecovery {
				r := newRecovery()
				r.Spec.BackupId = testBackupId
				r.Status.RestoreName = testRestoreName
				return r
			}(),
			ctrlObjects: []ctrlclient.Object{
				&velerov1api.Restore{
					ObjectMeta: metav1.ObjectMeta{Name: testRestoreName, Namespace: "velero"},
					Status:     velerov1api.RestoreStatus{Phase: velerov1api.RestorePhaseInProgress},
				},
			},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name: "restore does not exist - creates restore",
			recovery: func() *hcprecoveryv1alpha1.HCPRecovery {
				r := newRecovery()
				r.Spec.BackupId = testBackupId
				r.Status.RestoreName = testRestoreName
				return r
			}(),
			ctrlObjects:               []ctrlclient.Object{},
			expectDone:                true,
			expectAction:              true,
			expectCreateVeleroRestore: true,
		},
	}

	// This test requires a special controller setup so it runs outside the table.
	t.Run("get error - transient", func(t *testing.T) {
		// An empty scheme causes ctrlClient.Get for Restore to fail
		c := newControllerWithEmptyScheme(nil)
		recovery := newRecovery()
		recovery.Spec.BackupId = testBackupId
		recovery.Status.RestoreName = testRestoreName

		done, action, err := c.createVeleroRestore(context.Background(), recovery)

		if !done {
			t.Error("expected done=true")
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
			c := newControllerWithFullScheme(nil, tt.ctrlObjects)

			done, action, err := c.createVeleroRestore(context.Background(), tt.recovery)

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
				if tt.expectCreateVeleroRestore && action.CreateVeleroRestore == nil {
					t.Error("expected CreateVeleroRestore action, got nil")
				}
				if !tt.expectCreateVeleroRestore && action.CreateVeleroRestore != nil {
					t.Error("expected no CreateVeleroRestore action, got one")
				}
			}
		})
	}
}
