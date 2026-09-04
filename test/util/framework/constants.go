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

package framework

import "time"

// When updating timeouts, see test/e2e/README.md#updating-e2e-timeouts.

// Provisioning timeouts
const (
	// ClusterCreationTimeout is temporarily raised from 20m as a stop-gap while
	// Cluster Service provisioning latency is being investigated (CS steps run
	// strictly serially today and recent telemetry shows several steps, e.g.
	// DNS and RBAC role assignment on the MRG, taking noticeably longer than
	// their historical p50/p90). Revisit and re-tune once the CS-side latency
	// work lands; see test/e2e/README.md#updating-e2e-timeouts.
	ClusterCreationTimeout      = 30 * time.Minute
	NodePoolCreationTimeout     = 20 * time.Minute
	ExternalAuthCreationTimeout = 15 * time.Minute
	GetAdminRESTConfigTimeout   = 10 * time.Minute
)

// Deletion timeouts
const (
	HCPClusterDeletionTimeout   = 25 * time.Minute
	NodePoolDeletionTimeout     = 25 * time.Minute
	ExternalAuthDeletionTimeout = 15 * time.Minute
)

// Resource Update timeouts
const (
	HCPClusterVersionUpgradeTimeout = 45 * time.Minute
	NodePoolVersionUpgradeTimeout   = 45 * time.Minute
	NodePoolScalingTimeout          = 20 * time.Minute
	UpdateHCPClusterTimeout         = 10 * time.Minute
)

// Identity assignment
const (
	IdentityContainerAssignmentRetryInterval = 60 * time.Second
)

// API version deployment deadlines (timebombs)
var V20260630PreviewDeploymentDeadline = Must(time.Parse(time.RFC3339, "2026-08-14T00:00:00Z"))
var V20260901PreviewDeploymentDeadline = Must(time.Parse(time.RFC3339, "2026-10-15T00:00:00Z"))

// Backup timeouts
const (
	BackupWaitTimeout  = 11 * time.Minute
	BackupWaitInterval = 30 * time.Second
)
