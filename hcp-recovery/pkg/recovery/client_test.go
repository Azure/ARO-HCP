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

package recovery

import (
	"context"
	"slices"
	"testing"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestListBackupsForCluster(t *testing.T) {
	var testCases = []struct {
		name            string
		hc              *hypershiftv1beta1.HostedCluster
		backups         []velerov1api.Backup
		expectedBackups []string
	}{
		{
			name: "test list backups for cluster",
			hc: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
					Labels:    map[string]string{"api.openshift.com/id": "12345"}},
				Spec: hypershiftv1beta1.HostedClusterSpec{InfraID: "test-cluster"},
			},
			backups: []velerov1api.Backup{
				{
					ObjectMeta: v1.ObjectMeta{
						Name:   "backup-1",
						Labels: map[string]string{"api.openshift.com/id": "12345"}},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name:   "backup-2",
						Labels: map[string]string{"api.openshift.com/id": "123456"}},
				},
			},
			expectedBackups: []string{"backup-1"},
		},
		{
			name: "no matching clusters, list backups empty",
			hc: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
					Labels:    map[string]string{"api.openshift.com/id": "12345"}},
				Spec: hypershiftv1beta1.HostedClusterSpec{InfraID: "test-cluster"},
			},
			backups: []velerov1api.Backup{
				{
					ObjectMeta: v1.ObjectMeta{
						Name:   "backup-1",
						Labels: map[string]string{"api.openshift.com/id": "123456"}},
				},
				{
					ObjectMeta: v1.ObjectMeta{
						Name:   "backup-2",
						Labels: map[string]string{"api.openshift.com/id": "123456"}},
				},
			},
			expectedBackups: []string{},
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			objs := []client.Object{tt.hc}
			for i := range tt.backups {
				objs = append(objs, &tt.backups[i])
			}
			drClient, err := NewFakeClient(objs...)
			if err != nil {
				t.Errorf("error creating fake client: %v", err)
			}

			backups, err := drClient.ListBackupsForCluster(context.Background(), "12345")
			if err != nil {
				t.Errorf("error listing backups for cluster: %v", err)
			}
			for _, backup := range backups {
				if slices.Contains(tt.expectedBackups, backup.Name) == false {
					t.Errorf("expected backup %s not found in list of backups", backup.Name)
				}
			}
			if len(backups) != len(tt.expectedBackups) {
				t.Errorf("expected %d backups, got %d", len(tt.expectedBackups), len(backups))
			}
		})
	}
}

func TestGetBackup(t *testing.T) {
	var testCases = []struct {
		name        string
		backups     []velerov1api.Backup
		lookupName  string
		expectError bool
	}{
		{
			name: "backup found",
			backups: []velerov1api.Backup{
				{
					ObjectMeta: v1.ObjectMeta{
						Name:      "backup-1",
						Namespace: "velero",
						Labels:    map[string]string{"api.openshift.com/id": "12345"},
					},
				},
			},
			lookupName:  "backup-1",
			expectError: false,
		},
		{
			name:        "backup not found",
			backups:     []velerov1api.Backup{},
			lookupName:  "nonexistent-backup",
			expectError: true,
		},
		{
			name: "backup in wrong namespace not found",
			backups: []velerov1api.Backup{
				{
					ObjectMeta: v1.ObjectMeta{
						Name:      "backup-1",
						Namespace: "other-namespace",
					},
				},
			},
			lookupName:  "backup-1",
			expectError: true,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			var objs []client.Object
			for i := range tt.backups {
				objs = append(objs, &tt.backups[i])
			}
			drClient, err := NewFakeClient(objs...)
			if err != nil {
				t.Fatalf("error creating fake client: %v", err)
			}

			backup, err := drClient.GetBackup(context.Background(), tt.lookupName)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if backup == nil {
					t.Fatalf("expected backup but got nil")
				}
				if backup.Name != tt.lookupName {
					t.Errorf("expected backup name %s, got %s", tt.lookupName, backup.Name)
				}
			}
		})
	}
}
