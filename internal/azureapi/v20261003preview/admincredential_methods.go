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

package v20261003preview

import (
	"encoding/json"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20261003preview/generated"
)

func newHCPOpenShiftClusterAdminCredential(from *coreapi.HCPOpenShiftClusterAdminCredential) *generated.HcpOpenShiftClusterAdminCredential {
	return &generated.HcpOpenShiftClusterAdminCredential{
		ExpirationTimestamp: metadataapi.PtrOrNil(from.ExpirationTimestamp),
		Kubeconfig:          metadataapi.PtrOrNil(from.Kubeconfig),
	}
}

func (v version) MarshalHCPOpenShiftClusterAdminCredential(from *coreapi.HCPOpenShiftClusterAdminCredential) ([]byte, error) {
	return coreapi.MarshalJSON(newHCPOpenShiftClusterAdminCredential(from))
}

func (v version) UnmarshalHCPOpenShiftClusterAdminCredentialRequest(data []byte) (*coreapi.HCPOpenShiftClusterAdminCredentialRequest, error) {
	var versionedRequest generated.HcpOpenShiftClusterAdminCredentialRequest
	if err := json.Unmarshal(data, &versionedRequest); err != nil {
		return nil, err
	}
	return &coreapi.HCPOpenShiftClusterAdminCredentialRequest{
		CertificateSigningRequest: metadataapi.Deref(versionedRequest.CertificateSigningRequest),
	}, nil
}
