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

import (
	"os"
	"strconv"
	"time"
)

// When updating timeouts, see test/e2e/README.md#updating-e2e-timeouts.

// Provisioning timeouts
const (
	ClusterCreationTimeout      = 20 * time.Minute
	NodePoolCreationTimeout     = 20 * time.Minute
	ExternalAuthCreationTimeout = 15 * time.Minute
	GetAdminRESTConfigTimeout   = 10 * time.Minute
)

// Deletion timeouts
const (
	HCPClusterDeletionTimeout = 45 * time.Minute
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

// ClusterCreateStaggerInterval returns the stagger delay between successive
// cluster creation starts, read from E2E_CLUSTER_CREATE_STAGGER_SECONDS.
// Returns 0 (disabled) when the env var is unset or empty.
func ClusterCreateStaggerInterval() time.Duration {
	s := os.Getenv("E2E_CLUSTER_CREATE_STAGGER_SECONDS")
	if s == "" {
		return 30 * time.Second
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}
