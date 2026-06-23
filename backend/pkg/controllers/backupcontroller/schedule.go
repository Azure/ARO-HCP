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

package backupcontroller

import (
	"fmt"
	"time"

	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"github.com/vmware-tanzu/velero/pkg/builder"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/backup"
)

func NewScheduledBackup(clusterID, clusterName, hcNamespace, hcpNamespace, backupName, cronSchedule string, ttl time.Duration, paused bool) *velerov1api.Schedule {
	scheduleName := fmt.Sprintf("%s-%s", clusterID, backupName)
	backup := backup.NewBackup(scheduleName, clusterID, ttl, hcNamespace, hcpNamespace)
	schedule := builder.ForSchedule("velero", scheduleName).
		CronSchedule(cronSchedule).
		Template(backup.Spec).
		ObjectMeta(func(object metav1.Object) {
			object.SetLabels(map[string]string{
				"velero.io/storage-location":                       "default",
				"hypershift.openshift.io/hosted-cluster":           clusterName,
				"hypershift.openshift.io/hosted-cluster-namespace": hcNamespace,
				"api.openshift.com/id":                             clusterID,
			})
		})
	s := schedule.Result()
	s.Spec.Paused = paused
	return s
}
