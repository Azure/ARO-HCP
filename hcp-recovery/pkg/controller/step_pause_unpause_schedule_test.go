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

func newVeleroSchedule(name string, paused bool) *velerov1api.Schedule {
	return &velerov1api.Schedule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "velero",
			Labels:    map[string]string{"api.openshift.com/id": testClusterId},
		},
		Spec: velerov1api.ScheduleSpec{
			Paused: paused,
		},
	}
}

func TestPauseBackupSchedule(t *testing.T) {
	tests := []struct {
		name                     string
		recovery                 *hcprecoveryv1alpha1.HCPRecovery
		ctrlObjects              []ctrlclient.Object
		expectDone               bool
		expectAction             bool
		expectStatusUpdate       bool
		expectPatchSchedules     bool
		expectPatchScheduleCount int
	}{
		{
			name: "already paused - BackupSchedulePaused is True",
			recovery: newRecovery(metav1.Condition{
				Type:   hcprecoveryv1alpha1.ConditionBackupSchedulePaused,
				Status: metav1.ConditionTrue,
			}),
			expectDone: false,
		},
		{
			name:               "no schedules found",
			recovery:           newRecovery(),
			ctrlObjects:        []ctrlclient.Object{},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name:     "all schedules already paused",
			recovery: newRecovery(),
			ctrlObjects: []ctrlclient.Object{
				newVeleroSchedule("schedule-1", true),
				newVeleroSchedule("schedule-2", true),
			},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name:     "unpaused schedules exist - returns patch",
			recovery: newRecovery(),
			ctrlObjects: []ctrlclient.Object{
				newVeleroSchedule("schedule-1", false),
				newVeleroSchedule("schedule-2", false),
			},
			expectDone:               true,
			expectAction:             true,
			expectPatchSchedules:     true,
			expectPatchScheduleCount: 2,
		},
		{
			name:     "mixed paused and unpaused",
			recovery: newRecovery(),
			ctrlObjects: []ctrlclient.Object{
				newVeleroSchedule("schedule-1", true),
				newVeleroSchedule("schedule-2", false),
			},
			expectDone:               true,
			expectAction:             true,
			expectPatchSchedules:     true,
			expectPatchScheduleCount: 1,
		},
	}

	t.Run("list error", func(t *testing.T) {
		c := newControllerWithEmptyScheme(nil)
		recovery := newRecovery()

		done, action, _ := c.pauseBackupSchedule(context.Background(), recovery)

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

			done, action, err := c.pauseBackupSchedule(context.Background(), tt.recovery)

			if done != tt.expectDone {
				t.Errorf("expected done=%v, got %v", tt.expectDone, done)
			}

			if tt.expectAction && action == nil {
				t.Fatal("expected action, got nil")
			}
			if !tt.expectAction && action != nil {
				t.Fatalf("expected no action, got %+v", action)
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if action != nil {
				if tt.expectStatusUpdate && action.StatusUpdate == nil {
					t.Error("expected StatusUpdate action, got nil")
				}
				if tt.expectPatchSchedules {
					if len(action.PatchVeleroSchedules) != tt.expectPatchScheduleCount {
						t.Errorf("expected %d PatchVeleroSchedules, got %d", tt.expectPatchScheduleCount, len(action.PatchVeleroSchedules))
					}
					for _, s := range action.PatchVeleroSchedules {
						if !s.Spec.Paused {
							t.Errorf("expected schedule %s to have Paused=true, got false", s.Name)
						}
					}
				}
				if !tt.expectPatchSchedules && len(action.PatchVeleroSchedules) > 0 {
					t.Errorf("expected no PatchVeleroSchedules, got %d", len(action.PatchVeleroSchedules))
				}
			}
		})
	}
}

func TestUnpauseBackupSchedule(t *testing.T) {
	tests := []struct {
		name                     string
		recovery                 *hcprecoveryv1alpha1.HCPRecovery
		ctrlObjects              []ctrlclient.Object
		expectDone               bool
		expectAction             bool
		expectStatusUpdate       bool
		expectPatchSchedules     bool
		expectPatchScheduleCount int
	}{
		{
			name: "already unpaused - BackupScheduleUnpaused is True",
			recovery: newRecovery(metav1.Condition{
				Type:   hcprecoveryv1alpha1.ConditionBackupScheduleUnpaused,
				Status: metav1.ConditionTrue,
			}),
			expectDone: false,
		},
		{
			name:               "no schedules found",
			recovery:           newRecovery(),
			ctrlObjects:        []ctrlclient.Object{},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name:     "all schedules already unpaused",
			recovery: newRecovery(),
			ctrlObjects: []ctrlclient.Object{
				newVeleroSchedule("schedule-1", false),
				newVeleroSchedule("schedule-2", false),
			},
			expectDone:         true,
			expectAction:       true,
			expectStatusUpdate: true,
		},
		{
			name:     "paused schedules exist - returns patch",
			recovery: newRecovery(),
			ctrlObjects: []ctrlclient.Object{
				newVeleroSchedule("schedule-1", true),
				newVeleroSchedule("schedule-2", true),
			},
			expectDone:               true,
			expectAction:             true,
			expectPatchSchedules:     true,
			expectPatchScheduleCount: 2,
		},
	}

	t.Run("list error", func(t *testing.T) {
		c := newControllerWithEmptyScheme(nil)
		recovery := newRecovery()

		done, action, _ := c.unpauseBackupSchedule(context.Background(), recovery)

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

			done, action, err := c.unpauseBackupSchedule(context.Background(), tt.recovery)

			if done != tt.expectDone {
				t.Errorf("expected done=%v, got %v", tt.expectDone, done)
			}

			if tt.expectAction && action == nil {
				t.Fatal("expected action, got nil")
			}
			if !tt.expectAction && action != nil {
				t.Fatalf("expected no action, got %+v", action)
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if action != nil {
				if tt.expectStatusUpdate && action.StatusUpdate == nil {
					t.Error("expected StatusUpdate action, got nil")
				}
				if tt.expectPatchSchedules {
					if len(action.PatchVeleroSchedules) != tt.expectPatchScheduleCount {
						t.Errorf("expected %d PatchVeleroSchedules, got %d", tt.expectPatchScheduleCount, len(action.PatchVeleroSchedules))
					}
					for _, s := range action.PatchVeleroSchedules {
						if s.Spec.Paused {
							t.Errorf("expected schedule %s to have Paused=false, got true", s.Name)
						}
					}
				}
				if !tt.expectPatchSchedules && len(action.PatchVeleroSchedules) > 0 {
					t.Errorf("expected no PatchVeleroSchedules, got %d", len(action.PatchVeleroSchedules))
				}
			}
		})
	}
}
