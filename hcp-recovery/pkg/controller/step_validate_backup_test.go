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
	"time"

	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	hcprecoveryv1alpha1 "github.com/Azure/ARO-HCP/hcp-recovery/pkg/apis/hcprecovery/v1alpha1"
)

const testBackupId = "test-backup"

var testBackupCompletionTime = metav1.NewTime(time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC))

func newVeleroBackup(name, clusterId string, phase velerov1api.BackupPhase) *velerov1api.Backup {
	backup := &velerov1api.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "velero",
			Labels:    map[string]string{"api.openshift.com/id": clusterId},
		},
		Status: velerov1api.BackupStatus{
			Phase: phase,
		},
	}
	if phase == velerov1api.BackupPhaseCompleted {
		backup.Status.CompletionTimestamp = &testBackupCompletionTime
	}
	return backup
}

func TestValidateBackup(t *testing.T) {
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
			name: "already completed - BackupValidated is True",
			recovery: func() *hcprecoveryv1alpha1.HCPRecovery {
				r := newRecovery(metav1.Condition{
					Type:               hcprecoveryv1alpha1.ConditionBackupValidated,
					Status:             metav1.ConditionTrue,
					Reason:             "BackupValid",
					Message:            "Backup exists and is in a completed state",
					ObservedGeneration: 1,
				})
				r.Spec.BackupId = testBackupId
				r.Status.RestoredToTimestamp = &testBackupCompletionTime
				return r
			}(),
			ctrlObjects: []ctrlclient.Object{
				newVeleroBackup(testBackupId, testClusterId, velerov1api.BackupPhaseCompleted),
			},
			expectDone: false,
		},
		{
			name: "backup not found",
			recovery: func() *hcprecoveryv1alpha1.HCPRecovery {
				r := newRecovery()
				r.Spec.BackupId = testBackupId
				return r
			}(),
			ctrlObjects:        []ctrlclient.Object{},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name: "backup cluster mismatch",
			recovery: func() *hcprecoveryv1alpha1.HCPRecovery {
				r := newRecovery()
				r.Spec.BackupId = testBackupId
				return r
			}(),
			ctrlObjects: []ctrlclient.Object{
				newVeleroBackup(testBackupId, "wrong-cluster-id", velerov1api.BackupPhaseCompleted),
			},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name: "backup not completed",
			recovery: func() *hcprecoveryv1alpha1.HCPRecovery {
				r := newRecovery()
				r.Spec.BackupId = testBackupId
				return r
			}(),
			ctrlObjects: []ctrlclient.Object{
				newVeleroBackup(testBackupId, testClusterId, velerov1api.BackupPhaseInProgress),
			},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name: "backup valid and completed",
			recovery: func() *hcprecoveryv1alpha1.HCPRecovery {
				r := newRecovery()
				r.Spec.BackupId = testBackupId
				return r
			}(),
			ctrlObjects: []ctrlclient.Object{
				newVeleroBackup(testBackupId, testClusterId, velerov1api.BackupPhaseCompleted),
			},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
	}

	// This test requires a special controller setup so it runs outside the table.
	t.Run("backup retrieval error", func(t *testing.T) {
		// An empty scheme causes ctrlClient.Get for Backup to fail
		c := newControllerWithEmptyScheme(nil)
		recovery := newRecovery()
		recovery.Spec.BackupId = testBackupId

		done, action, _ := c.validateBackup(context.Background(), recovery)

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
			c := newControllerWithFullScheme(nil, tt.ctrlObjects)

			done, action, err := c.validateBackup(context.Background(), tt.recovery)

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

			if tt.name == "backup valid and completed" {
				if action == nil || action.StatusUpdate == nil {
					t.Fatal("expected action with StatusUpdate")
				}
				if action.StatusUpdate.Status.RestoredToTimestamp == nil {
					t.Error("expected RestoredToTimestamp to be set from backup CompletionTimestamp")
				} else if !action.StatusUpdate.Status.RestoredToTimestamp.Equal(&testBackupCompletionTime) {
					t.Errorf("expected RestoredToTimestamp=%v, got %v", testBackupCompletionTime, *action.StatusUpdate.Status.RestoredToTimestamp)
				}
			}
		})
	}
}
