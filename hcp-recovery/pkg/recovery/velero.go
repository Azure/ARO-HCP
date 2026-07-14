// Copyright 2025 Microsoft Corporation
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
	"time"

	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"github.com/vmware-tanzu/velero/pkg/builder"
)

var restoreExcludedResources = []string{
	"pv",
	"pvc",
	"nodes",
	"events",
	"events.events.k8s.io",
	"backups.velero.io",
	"restores.velero.io",
	"resticrepositories.velero.io",
}

func NewRestore(restoreName, backupName string) *velerov1api.Restore {
	restore := builder.ForRestore("velero", restoreName).
		RestorePVs(true).
		Backup(backupName).
		ExistingResourcePolicy("update").
		ExcludedResources(restoreExcludedResources...).
		ItemOperationTimeout(4 * time.Hour)
	return restore.Result()
}
