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

package api

// BackupResponse is the JSON representation of a single backup.
type BackupResponse struct {
	Name                string `json:"name"`
	StartTimestamp      string `json:"startTimestamp"`
	CompletionTimestamp string `json:"completionTimestamp"`
	Phase               string `json:"phase"`
	KeyVersion          string `json:"keyVersion"`
}

// GetBackupResponse is the JSON response for GET backup endpoints.
type GetBackupResponse struct {
	ResourceID string         `json:"resourceID"`
	Backup     BackupResponse `json:"backup"`
}

// BackupScheduleResponse is the JSON response for backup schedule endpoints.
type BackupScheduleResponse struct {
	ResourceID string                 `json:"resourceID"`
	State      BackupScheduleState    `json:"state"`
	Schedules  []BackupScheduleDetail `json:"schedules"`
}

// BackupScheduleDetail holds per-schedule status from the Velero Schedule ReadDesire.
type BackupScheduleDetail struct {
	Name                string `json:"name"`
	LastBackupTime      string `json:"lastBackupTime,omitempty"`
	BackupSchedulePhase string `json:"backupSchedulePhase,omitempty"`
	Paused              bool   `json:"paused"`
}

// BackupSchedulePatchRequest is the JSON body for PATCH backup schedule requests.
type BackupSchedulePatchRequest struct {
	State BackupScheduleState `json:"state"`
}

// BackupSchedulePatchResponse is the JSON response for PATCH backup schedule requests.
type BackupSchedulePatchResponse struct {
	ResourceID string              `json:"resourceID"`
	State      BackupScheduleState `json:"state"`
}
