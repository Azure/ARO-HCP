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

package framework

// Step IDs are machine-readable identifiers for timing steps, suitable for
// post-hoc aggregation. Steps with the same ID represent the same logical
// operation across different test runs, regardless of the human-readable
// name (which may contain dynamic resource names).
const (
	// Deployment steps
	StepIDDeployARMTemplate       = "deploy-arm-template"
	StepIDDeployHCPCluster        = "deploy-hcp-cluster"
	StepIDDeployNodePool          = "deploy-node-pool"
	StepIDDeployCustomerResources = "deploy-customer-resources"

	// Credential steps
	StepIDCollectAdminCredentials = "collect-admin-credentials"
	StepIDRevokeAdminCredentials  = "revoke-admin-credentials"

	// Identity steps
	StepIDAssignIdentityContainers     = "assign-identity-containers"
	StepIDLeaseIdentityContainer       = "lease-identity-container"
	StepIDReleaseIdentities            = "release-identities"
	StepIDValidateIdentityRBACBindings = "validate-identity-rbac-bindings"

	// Deletion / cleanup steps
	StepIDDeleteCreatedResources           = "delete-created-resources"
	StepIDDeleteHCPClusters                = "delete-hcp-clusters"
	StepIDWaitManagedResourceGroupDeletion = "wait-managed-resource-group-deletion"
	StepIDDeleteResourceGroup              = "delete-resource-group"
	StepIDCleanupResourceGroup             = "cleanup-resource-group"
	StepIDCleanupResourceGroupNoRP         = "cleanup-resource-group-no-rp"
	StepIDCleanupAppRegistrations          = "cleanup-app-registrations"

	// Debug / inspection steps
	StepIDCollectDebugInfo = "collect-debug-info"
	StepIDOCAdmInspect     = "oc-adm-inspect"
)
