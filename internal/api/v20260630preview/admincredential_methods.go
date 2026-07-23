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

package v20260630preview

import (
	"encoding/json"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20260630preview/generated"
)

func (v version) UnmarshalHCPOpenShiftClusterAdminCredentialRequest(body []byte) (*api.HCPOpenShiftClusterAdminCredentialRequest, error) {
	if len(body) == 0 {
		return nil, nil
	}
	var req generated.HcpOpenShiftClusterAdminCredentialRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	return &api.HCPOpenShiftClusterAdminCredentialRequest{
		CertificateRequest: api.Deref(req.CertificateRequest),
	}, nil
}

func newHCPOpenShiftClusterAdminCredential(from *api.HCPOpenShiftClusterAdminCredential) *generated.HcpOpenShiftClusterAdminCredential {
	return &generated.HcpOpenShiftClusterAdminCredential{
		ExpirationTimestamp: api.PtrOrNil(from.ExpirationTimestamp),
		Kubeconfig:          api.PtrOrNil(from.Kubeconfig),
	}
}

func (v version) MarshalHCPOpenShiftClusterAdminCredential(from *api.HCPOpenShiftClusterAdminCredential) ([]byte, error) {
	return arm.MarshalJSON(newHCPOpenShiftClusterAdminCredential(from))
}
