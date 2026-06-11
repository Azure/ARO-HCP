// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package backupcontroller

import (
	"encoding/json"
	"fmt"

	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	workv1 "open-cluster-management.io/api/work/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

const (
	backupScheduleManagedByK8sLabelKey   = "aro-hcp.azure.com/backup-managed-by"
	backupScheduleManagedByK8sLabelValue = "backup-schedule-controller"

	veleroNamespace = "velero"
)

// buildScheduleManifestWork constructs a ManifestWork containing one or more Velero Schedules.
// The ManifestWork is an owned resource (ServerSideApply) with FeedbackRules to read status back.
func buildScheduleManifestWork(maestroBundleNamespacedName types.NamespacedName, schedules []*velerov1.Schedule) (*workv1.ManifestWork, error) {
	manifests := make([]workv1.Manifest, 0, len(schedules))
	configs := make([]workv1.ManifestConfigOption, 0, len(schedules))

	for _, schedule := range schedules {
		raw, err := json.Marshal(schedule)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal schedule %s: %w", schedule.Name, err)
		}
		manifests = append(manifests, workv1.Manifest{
			RawExtension: runtime.RawExtension{
				Raw: raw,
			},
		})
		configs = append(configs, workv1.ManifestConfigOption{
			ResourceIdentifier: workv1.ResourceIdentifier{
				Group:     velerov1.SchemeGroupVersion.Group,
				Resource:  "schedules",
				Name:      schedule.Name,
				Namespace: schedule.Namespace,
			},
			UpdateStrategy: &workv1.UpdateStrategy{
				Type: workv1.UpdateStrategyTypeServerSideApply,
			},
			FeedbackRules: []workv1.FeedbackRule{
				{
					Type: workv1.JSONPathsType,
					JsonPaths: []workv1.JsonPath{
						{
							Name: "status",
							Path: ".status",
						},
					},
				},
			},
		})
	}

	return &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      maestroBundleNamespacedName.Name,
			Namespace: maestroBundleNamespacedName.Namespace,
			Labels: map[string]string{
				backupScheduleManagedByK8sLabelKey: backupScheduleManagedByK8sLabelValue,
			},
		},
		Spec: workv1.ManifestWorkSpec{
			Workload:        workv1.ManifestsTemplate{Manifests: manifests},
			ManifestConfigs: configs,
		},
	}, nil
}
