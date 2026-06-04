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

package certs

import "crypto/x509/pkix"

const (
	// CN must start with "system:sre-break-glass:" to pass the HyperShift
	// CSR signer validation. The RBAC on the HCP cluster binds to this
	// user and group identity.
	DiagnosticsCommonName   = "system:sre-break-glass:aro-diagnostics"
	DiagnosticsOrganization = "system:aro-diagnostics"
)

func BuildDiagnosticsSubject() pkix.Name {
	return pkix.Name{
		CommonName:   DiagnosticsCommonName,
		Organization: []string{DiagnosticsOrganization},
	}
}
