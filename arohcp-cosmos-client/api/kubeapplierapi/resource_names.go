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

package kubeapplierapi

// Well-known desire resource names. These identify Cosmos documents, not the
// Kubernetes objects they target. Keep writers, readers and controller interests
// on the same constants so changing a name cannot silently break notifications.
const (
	ReadDesireNameReadonlyHostedCluster                                    = "readonlyhypershifthostedcluster"
	ReadDesireNameReadonlyNodePool                                         = "readonlyhypershiftnodepool"
	ReadDesireNameReadonlyHypershiftControlPlaneComponentClusterAutoscaler = "readonlyhypershiftcontrolplanecomponentclusterautoscaler"
	ReadDesireNameServingCA                                                = "systemadmincredential-serving-ca"

	// CSR and revocation apply/read desires share a name within distinct resource types.
	SystemAdminCredentialCSRDesireName         = "systemadmincredentialcsr"
	SystemAdminCredentialCSRApprovalDesireName = "systemadmincredentialcsrapproval"
	SystemAdminCredentialRevocationDesireName  = "systemadmincredentialrevocation"
)
