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
	ClusterCreationTimeout = 30 * time.Minute
	// NodePoolCreationTimeout has a hard floor that is structural rather than
	// latency-driven, so do NOT re-tune it down to an observed p99.
	//
	// The create helpers set the TagNodePoolMaxCreationDuration experimental tag
	// to this value minus one minute, and that becomes the RP's creation deadline
	// (production defaults to 60m; see defaultCreateOperationCompletionDeadlineDuration
	// in internal/admission/admit_cluster.go). Node pools default to
	// AutoRepair: true, so a machine that fails to register is only rescued after
	// the machine-health-check node startup timeout elapses and a replacement is
	// provisioned and registers. The deadline must cover that whole cycle, not
	// just the detection half.
	//
	// The detection half is currently 10m: the backend stamps
	// MachineHealthCheckNodeStartupTimeoutAnnotation = "10m" on every
	// HostedCluster (ensureHostedClusterAnnotations in
	// backend/pkg/controllers/clusterresources/cluster_resources_controller.go),
	// overriding HyperShift's 20m default. That override landed in Oct 2026; the
	// AROSLSRE-1668 failure that first exposed this floor predates it and ran
	// against the 20m default, where the then-current 20m constant yielded a 19m
	// deadline that expired one minute *before* auto-repair could fire ("node pool
	// creation did not complete before the deadline; ... AllNodesHealthy is False:
	// 1 of 1 machines are not healthy").
	//
	// Raised to 30m by #7308 to allow MHC repair in stage. Any future value must
	// stay above the detection timeout plus a full replacement provisioning cycle,
	// independent of p99 -- and must be rechecked if that annotation changes.
	NodePoolCreationTimeout     = 30 * time.Minute
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
	// NodePoolScalingTimeout has the same structural floor as
	// NodePoolCreationTimeout, for the same reason: the update helpers set the
	// TagNodePoolMaxUpdateDuration experimental tag to this value minus one
	// minute, and that becomes the RP's update deadline. The constant is shared by
	// scale-up, scale-down and autoscaling-enable steps, so it has to satisfy the
	// worst of them: any step that grows a node pool waits on a new machine
	// registering, and so must outlast the machine-health-check detection timeout
	// plus a full replacement provisioning cycle (see NodePoolCreationTimeout --
	// the same defect #7308 fixed on the creation path, which left this constant
	// untouched).
	//
	// The binding floor here is measured, not derived. ARO-29936: enabling
	// autoscaling on an existing non-autoscaling node pool was inactive for 20m03s
	// after the autoscaling spec was acknowledged, and the first Machine appeared
	// 20m09s after the PATCH that starts the test's budget -- so the former 20m
	// value missed by about nine seconds, before any replacement or registration
	// time. Do NOT re-tune this to an observed p99.
	NodePoolScalingTimeout  = 35 * time.Minute
	UpdateHCPClusterTimeout = 10 * time.Minute
)

// Identity assignment
const (
	IdentityContainerAssignmentRetryInterval = 60 * time.Second
)

// API version deployment deadlines (timebombs)
var V20260630PreviewDeploymentDeadline = Must(time.Parse(time.RFC3339, "2026-08-14T00:00:00Z"))
var V20261001DeploymentDeadline = Must(time.Parse(time.RFC3339, "2026-11-01T00:00:00Z"))

// Backup timeouts
const (
	BackupWaitTimeout  = 11 * time.Minute
	BackupWaitInterval = 30 * time.Second
)
