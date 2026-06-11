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

package systemadmincredential

import (
	"strings"

	"github.com/google/uuid"
)

// SuffixLength is the number of hex characters every per-credential and
// per-revoke suffix uses. See PLAN.md open question 2: 16 hex chars =
// 64 bits of entropy, plenty of headroom inside a single cluster's
// systemAdminCredentials namespace.
const SuffixLength = 16

// NewCredentialName returns a fresh 16-character hex string suitable for
// use as the `<credName>` segment of a SystemAdminCredential resource ID
// and as the suffix on every per-credential k8s object name. Sourced
// from a UUIDv4 with dashes stripped.
func NewCredentialName() string {
	return strings.ReplaceAll(uuid.New().String(), "-", "")[:SuffixLength]
}

// RevokeOpSuffix derives the 16-character per-revoke suffix used on the
// CRR ApplyDesire / k8s object names. The input is the revoke
// Operation's ID string (typically Operation.OperationID.Name); dashes
// are stripped and the result is truncated to SuffixLength.
//
// Panics if the input is shorter than SuffixLength after dash stripping
// — that should never happen for a real operation ID (they're 36-char
// UUIDs), and the panic catches a programming error early.
func RevokeOpSuffix(operationID string) string {
	stripped := strings.ReplaceAll(operationID, "-", "")
	if len(stripped) < SuffixLength {
		panic("systemadmincredential: operation ID too short to derive revoke suffix")
	}
	return stripped[:SuffixLength]
}
