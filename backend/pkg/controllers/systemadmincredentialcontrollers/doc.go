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

// Package systemadmincredentialcontrollers holds the controllers that
// implement the SystemAdminCredentialRequest lifecycle — credential request,
// issuance observation, revocation, and cleanup.
//
// The controllers are:
//
//  1. OperationRequestCredentialDispatch — creates the Cosmos document
//  2. OperationRequestCredentialPoll — maps conditions → ARM provisioning state
//  3. SystemAdminCredentialIssuanceObserver — watches CSR ReadDesire for signed cert
//  4. OperationRevokeCredentialsDispatch — flips credentials to AwaitingRevocation
//  5. OperationRevokeCredentialsPoll — drives CRR and per-credential teardown
//  6. SystemAdminCredentialClusterDeletionCleanup — cluster-deletion gate
//  7. SystemAdminCredentialPostIssuanceCleanup — eager teardown after Issued
//  8. SystemAdminCredentialRevokedGC — deletes revoked docs after 48h
//  9. SystemAdminCredentialDesiresCreator — creates ApplyDesires/ReadDesires for each credential
//  10. SystemAdminCredentialRevocationMarkRequests — marks credential requests for deletion
//  11. SystemAdminCredentialRevocationDesires — creates the CRR/RBAC revocation desires
//  12. SystemAdminCredentialRevocationCompletion — observes the CRR and marks the revocation complete
//  13. SystemAdminCredentialRevocationDeletion — tears down the revocation's desires and doc
package systemadmincredentialcontrollers
