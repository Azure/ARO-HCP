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

package systemadmincredential

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// GenerateCredentialName creates a 16-character random hex suffix from
// a UUIDv4, used as the credential's resource name.
func GenerateCredentialName() string {
	return strings.ReplaceAll(uuid.New().String(), "-", "")[:16]
}

// RevokeOpSuffix extracts the 16-character hex suffix from a revoke
// operation's OperationID.Name. This suffix is used in CRR desire
// and k8s object names.
func RevokeOpSuffix(operationName string) string {
	return strings.ReplaceAll(operationName, "-", "")[:16]
}

// DesireNameCSR returns the desire document name for a credential's CSR.
func DesireNameCSR(credName string) string {
	return fmt.Sprintf("systemAdminCredentialCSR-%s", credName)
}

// DesireNameCSRA returns the desire document name for a credential's CSRA.
func DesireNameCSRA(credName string) string {
	return fmt.Sprintf("systemAdminCredentialCSRA-%s", credName)
}

// DesireNameRBACGiveCSRPerm returns the desire name for the CSR RBAC bundle.
func DesireNameRBACGiveCSRPerm(credName string) string {
	return fmt.Sprintf("systemAdminCredentialRBACGiveCSRPerm-%s", credName)
}

// DesireNameRBACCSRA returns the desire name for the CSRA RBAC bundle.
func DesireNameRBACCSRA(credName string) string {
	return fmt.Sprintf("systemAdminCredentialRBACCSRA-%s", credName)
}

// DesireNameRBACRevocation returns the desire name for the revocation RBAC bundle.
func DesireNameRBACRevocation(credName string) string {
	return fmt.Sprintf("systemAdminCredentialRBACRevocation-%s", credName)
}

// DesireNameRevocation returns the desire name for the CRR apply and read desires.
func DesireNameRevocation(revokeOpSuffix string) string {
	return fmt.Sprintf("systemAdminCredentialRevocation-%s", revokeOpSuffix)
}

// DefaultUsername returns the default username for system-admin credentials.
const DefaultUsername = "system-admin"

// ReadDesireNameServingCA returns the ReadDesire name for the per-cluster
// serving CA Secret mirror.
const ReadDesireNameServingCA = "systemAdminCredentialServingCA"
